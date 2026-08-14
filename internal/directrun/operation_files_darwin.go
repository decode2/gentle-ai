//go:build darwin

package directrun

import (
	"context"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// Darwin intentionally fails closed until its descriptor implementation receives native coverage.
func newPlatformOperationFiles(context.Context, *reviewtransaction.RepositoryIdentityLease, Handoff) (operationFiles, error) {
	return nil, ErrOperationUnsupported
}
