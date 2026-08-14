//go:build windows

package directrun

import (
	"context"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// Windows intentionally fails closed until the opaque relative-handle authority is available.
func newPlatformOperationFiles(context.Context, *reviewtransaction.RepositoryIdentityLease, Handoff) (operationFiles, error) {
	return nil, ErrOperationUnsupported
}
