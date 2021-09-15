package conn

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"

	"github.com/ydb-platform/ydb-go-genproto/protos/Ydb"
	"github.com/ydb-platform/ydb-go-genproto/protos/Ydb_Issue"

	"github.com/ydb-platform/ydb-go-sdk/v3/cluster/balancer/conn/addr"
	"github.com/ydb-platform/ydb-go-sdk/v3/cluster/balancer/conn/runtime"
	"github.com/ydb-platform/ydb-go-sdk/v3/errors"
	"github.com/ydb-platform/ydb-go-sdk/v3/internal"
	"github.com/ydb-platform/ydb-go-sdk/v3/operation"
	"github.com/ydb-platform/ydb-go-sdk/v3/timeutil"
	"github.com/ydb-platform/ydb-go-sdk/v3/trace"
)

type Conn interface {
	grpc.ClientConnInterface

	Addr() addr.Addr
	Runtime() runtime.Runtime
	Close() error
	SetDriver(d Driver)
}

func (c *conn) Address() string {
	return c.Addr().String()
}

type conn struct {
	sync.Mutex

	dial    func(context.Context, string, int) (*grpc.ClientConn, error)
	addr    addr.Addr
	runtime runtime.Runtime
	done    chan struct{}

	driver Driver

	timer    timeutil.Timer
	ttl      time.Duration
	grpcConn *grpc.ClientConn
}

func (c *conn) SetDriver(d Driver) {
	c.Lock()
	c.driver = d
	c.Unlock()
}

func (c *conn) Addr() addr.Addr {
	return c.addr
}

func (c *conn) Runtime() runtime.Runtime {
	return c.runtime
}

func (c *conn) Conn(ctx context.Context) (*grpc.ClientConn, error) {
	c.Lock()
	defer c.Unlock()
	if c.grpcConn == nil || isBroken(c.grpcConn) {
		raw, err := c.dial(ctx, c.addr.Host, c.addr.Port)
		if err != nil {
			return nil, err
		}
		c.grpcConn = raw
	}
	c.timer.Reset(c.ttl)
	return c.grpcConn, nil
}

func isBroken(raw *grpc.ClientConn) bool {
	if raw == nil {
		return true
	}
	state := raw.GetState()
	return state == connectivity.Shutdown || state == connectivity.TransientFailure
}

func (c *conn) IsReady() bool {
	c.Lock()
	defer c.Unlock()
	return c != nil && c.grpcConn != nil && c.grpcConn.GetState() == connectivity.Ready
}

func (c *conn) waitClose() {
	for {
		select {
		case <-c.done:
			return
		case <-c.timer.C():
			c.Lock()
			if c.grpcConn != nil {
				_ = c.grpcConn.Close()
				c.grpcConn = nil
			}
			c.Unlock()
		}
	}
}

func (c *conn) Close() error {
	if !c.timer.Stop() {
		panic("cant stop timer")
	}
	c.Lock()
	defer c.Unlock()
	close(c.done)
	c.done = nil
	if c.grpcConn != nil {
		return c.grpcConn.Close()
	}
	return nil
}

func (c *conn) pessimize(ctx context.Context, err error) {
	c.driver.Trace(ctx).OnPessimization(
		trace.PessimizationStartInfo{
			Context: ctx,
			Address: c.Addr().String(),
			Cause:   err,
		},
	)(
		trace.PessimizationDoneInfo{
			Error: c.driver.Pessimize(c.addr),
		},
	)

}

func (c *conn) Invoke(ctx context.Context, method string, request interface{}, response interface{}, opts ...grpc.CallOption) (err error) {
	var (
		cancel context.CancelFunc
		opId   string
		issues []*Ydb_Issue.IssueMessage
	)
	if t := c.driver.RequestTimeout(); t > 0 {
		ctx, cancel = context.WithTimeout(ctx, t)
	}
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	if t := c.driver.OperationTimeout(); t > 0 {
		ctx = operation.WithOperationTimeout(ctx, t)
	}
	if t := c.driver.OperationCancelAfter(); t > 0 {
		ctx = operation.WithOperationCancelAfter(ctx, t)
	}

	// Get credentials (token actually) for the request.
	var md metadata.MD
	md, err = c.driver.Meta(ctx)
	if err != nil {
		return
	}
	if len(md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	params := operation.ContextParams(ctx)
	if !params.Empty() {
		operation.SetOperationParams(request, params)
	}

	start := timeutil.Now()
	c.runtime.OperationStart(start)
	t := c.driver.Trace(ctx)
	if t.OnOperation != nil {
		operationDone := t.OnOperation(
			trace.OperationStartInfo{
				Context: ctx,
				Address: c.Addr().String(),
				Method:  trace.Method(method),
				Params:  params,
			},
		)
		if operationDone != nil {
			defer func() {
				operationDone(
					trace.OperationDoneInfo{
						OpID:   opId,
						Issues: issues,
						Error:  err,
					},
				)
				err := errors.ErrIf(errors.IsTimeoutError(err), err)
				c.runtime.OperationDone(
					start, timeutil.Now(),
					err,
				)
			}()
		}
	}

	raw, err := c.Conn(ctx)
	if err != nil {
		err = errors.MapGRPCError(err)
		if errors.MustPessimizeEndpoint(err) {
			c.pessimize(ctx, err)
		}
		return
	}

	err = raw.Invoke(ctx, method, request, response, opts...)

	if err != nil {
		err = errors.MapGRPCError(err)
		if errors.MustPessimizeEndpoint(err) {
			c.pessimize(ctx, err)
		}
		return
	}
	if opResponse, ok := response.(internal.OpResponse); ok {
		opId = opResponse.GetOperation().GetId()
		issues = opResponse.GetOperation().GetIssues()
		switch {
		case !opResponse.GetOperation().GetReady():
			err = errors.ErrOperationNotReady

		case opResponse.GetOperation().GetStatus() != Ydb.StatusIds_SUCCESS:
			err = errors.NewOpError(opResponse.GetOperation())
		}
	}

	return
}

func (c *conn) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (_ grpc.ClientStream, err error) {
	// Remember raw context to pass it for the tracing functions.
	rawCtx := ctx

	var cancel context.CancelFunc
	if t := c.driver.StreamTimeout(); t > 0 {
		ctx, cancel = context.WithTimeout(ctx, t)
		defer func() {
			if err != nil {
				cancel()
			}
		}()
	}

	// Get credentials (token actually) for the request.
	md, err := c.driver.Meta(ctx)
	if err != nil {
		return
	}
	if len(md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	c.runtime.StreamStart(timeutil.Now())
	t := c.driver.Trace(ctx)
	var streamRecv func(trace.StreamRecvDoneInfo) func(trace.StreamDoneInfo)
	if t.OnStream != nil {
		streamRecv = t.OnStream(trace.StreamStartInfo{
			Context: ctx,
			Address: c.Addr().String(),
			Method:  trace.Method(method),
		})
	}
	defer func() {
		if err != nil {
			c.runtime.StreamDone(timeutil.Now(), err)
		}
	}()

	raw, err := c.Conn(ctx)
	if err != nil {
		err = errors.MapGRPCError(err)
		if errors.MustPessimizeEndpoint(err) {
			c.pessimize(ctx, err)
		}
		return
	}

	s, err := raw.NewStream(ctx, desc, method, append(opts, grpc.MaxCallRecvMsgSize(50*1024*1024))...)
	if err != nil {
		return nil, errors.MapGRPCError(err)
	}

	return &grpcClientStream{
		ctx:    rawCtx,
		c:      c,
		s:      s,
		cancel: cancel,
		recv:   streamRecv,
	}, nil
}

func NewConn(addr addr.Addr, dial func(context.Context, string, int) (*grpc.ClientConn, error), ttl time.Duration) Conn {
	if ttl <= 0 {
		ttl = time.Minute
	}
	c := &conn{
		addr:    addr,
		dial:    dial,
		ttl:     ttl,
		timer:   timeutil.NewTimer(ttl),
		done:    make(chan struct{}),
		runtime: runtime.New(),
	}
	go c.waitClose()
	return c
}
