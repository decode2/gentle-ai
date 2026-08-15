//go:build !linux

package directrun

import (
	"context"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

type retainedGitInspector struct{}

func newRetainedGitInspector(*reviewtransaction.RepositoryIdentityLease) (*retainedGitInspector, error) {
	return &retainedGitInspector{}, nil
}

func (*retainedGitInspector) Close() {}
func (*retainedGitInspector) inspect(context.Context) (gitInspection, error) {
	return gitInspection{}, ErrCommandTargetUnsupported
}

type gitChange struct{}
type gitStatus struct{}
type gitInspection struct{}

func (gitInspection) statusResult() (gitStatusResult, error) {
	return gitStatusResult{}, ErrCommandTargetUnsupported
}
func (gitInspection) diffResult() (gitDiffResult, error) {
	return gitDiffResult{}, ErrCommandTargetUnsupported
}

type gitStatusResult struct{}
type gitDiffResult struct{}
