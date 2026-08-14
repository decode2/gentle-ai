//go:build linux

package directrun

import (
	"context"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func newRecordBackend(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease) (interface {
	RecordBackend
	Close() error
}, error) {
	return newLinuxRecordBackend(ctx, lease)
}
