//go:build !linux

package directrun

import (
	"context"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

type retainedGitInspector struct{}

func newRetainedGitInspector(*reviewtransaction.RepositoryIdentityLease) (*retainedGitInspector, error) {
	return nil, ErrCommandTargetUnsupported
}

func (*retainedGitInspector) Close() {}
func (*retainedGitInspector) inspect(context.Context) (gitInspection, error) {
	return gitInspection{}, ErrCommandTargetUnsupported
}

type gitChange struct{}
type gitStatus struct{}
type gitInspection struct{}
