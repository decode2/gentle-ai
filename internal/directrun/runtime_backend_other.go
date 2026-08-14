//go:build !linux && !windows

package directrun

import (
	"context"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func newRecordBackend(context.Context, *reviewtransaction.RepositoryIdentityLease) (interface {
	RecordBackend
	Close() error
}, error) {
	return nil, ErrBackendUnavailable
}
