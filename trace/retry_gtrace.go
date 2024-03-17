// Code generated by gtrace. DO NOT EDIT.

package trace

import (
	"context"
)

// retryComposeOptions is a holder of options
type retryComposeOptions struct {
	panicCallback func(e interface{})
}

// RetryOption specified Retry compose option
type RetryComposeOption func(o *retryComposeOptions)

// WithRetryPanicCallback specified behavior on panic
func WithRetryPanicCallback(cb func(e interface{})) RetryComposeOption {
	return func(o *retryComposeOptions) {
		o.panicCallback = cb
	}
}

// Compose returns a new Retry which has functional fields composed both from t and x.
func (t *Retry) Compose(x *Retry, opts ...RetryComposeOption) *Retry {
	var ret Retry
	options := retryComposeOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	{
		h1 := t.OnRetry
		h2 := x.OnRetry
		ret.OnRetry = func(r RetryLoopStartInfo) func(RetryLoopIntermediateInfo) func(RetryLoopDoneInfo) {
			if options.panicCallback != nil {
				defer func() {
					if e := recover(); e != nil {
						options.panicCallback(e)
					}
				}()
			}
			var r1, r2 func(RetryLoopIntermediateInfo) func(RetryLoopDoneInfo)
			if h1 != nil {
				r1 = h1(r)
			}
			if h2 != nil {
				r2 = h2(r)
			}
			return func(r RetryLoopIntermediateInfo) func(RetryLoopDoneInfo) {
				if options.panicCallback != nil {
					defer func() {
						if e := recover(); e != nil {
							options.panicCallback(e)
						}
					}()
				}
				var r3, r4 func(RetryLoopDoneInfo)
				if r1 != nil {
					r3 = r1(r)
				}
				if r2 != nil {
					r4 = r2(r)
				}
				return func(r RetryLoopDoneInfo) {
					if options.panicCallback != nil {
						defer func() {
							if e := recover(); e != nil {
								options.panicCallback(e)
							}
						}()
					}
					if r3 != nil {
						r3(r)
					}
					if r4 != nil {
						r4(r)
					}
				}
			}
		}
	}
	return &ret
}
func (t *Retry) onRetry(r RetryLoopStartInfo) func(RetryLoopIntermediateInfo) func(RetryLoopDoneInfo) {
	fn := t.OnRetry
	if fn == nil {
		return func(RetryLoopIntermediateInfo) func(RetryLoopDoneInfo) {
			return func(RetryLoopDoneInfo) {
				return
			}
		}
	}
	res := fn(r)
	if res == nil {
		return func(RetryLoopIntermediateInfo) func(RetryLoopDoneInfo) {
			return func(RetryLoopDoneInfo) {
				return
			}
		}
	}
	return func(r RetryLoopIntermediateInfo) func(RetryLoopDoneInfo) {
		res := res(r)
		if res == nil {
			return func(RetryLoopDoneInfo) {
				return
			}
		}
		return res
	}
}
func RetryOnRetry(t *Retry, c *context.Context, call call, label string, idempotent bool, nestedCall bool) func(error) func(attempts int, _ error) {
	var p RetryLoopStartInfo
	p.Context = c
	p.Call = call
	p.Label = label
	p.Idempotent = idempotent
	p.NestedCall = nestedCall
	res := t.onRetry(p)
	return func(e error) func(int, error) {
		var p RetryLoopIntermediateInfo
		p.Error = e
		res := res(p)
		return func(attempts int, e error) {
			var p RetryLoopDoneInfo
			p.Attempts = attempts
			p.Error = e
			res(p)
		}
	}
}
