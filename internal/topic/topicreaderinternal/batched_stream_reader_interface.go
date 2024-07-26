package topicreaderinternal

import (
	"context"

	"github.com/ydb-platform/ydb-go-sdk/v3/internal/topic/topicreadercommon"
)

//go:generate mockgen -source batched_stream_reader_interface.go -destination batched_stream_reader_mock_test.go -package topicreaderinternal -write_package_comment=false

type batchedStreamReader interface {
	WaitInit(ctx context.Context) error
	ReadMessageBatch(ctx context.Context, opts ReadMessageBatchOptions) (*topicreadercommon.PublicBatch, error)
	Commit(ctx context.Context, commitRange topicreadercommon.CommitRange) error
	CloseWithError(ctx context.Context, err error) error
	PopBatchTx(ctx context.Context, tx *TransactionWrapper, opts ReadMessageBatchOptions) (*topicreadercommon.PublicBatch, error)
}
