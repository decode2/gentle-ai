//go:build !linux

package directrun

import (
	"context"
	"time"
)

func runRetainedCommand(context.Context, string, Command, time.Duration) (ExecResult, error) {
	return ExecResult{}, ErrCommandTargetUnsupported
}

func RunRetainedHelper() error { return ErrCommandTargetUnsupported }
