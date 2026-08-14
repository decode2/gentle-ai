//go:build darwin

package directrun

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetainedExecIsUnsupportedBeforeLaunch(t *testing.T) {
	_, err := runRetainedCommand(context.Background(), "/repo", Command{Argv: []string{"go", "version"}, CWD: "/repo"}, time.Second)
	if !errors.Is(err, ErrCommandTargetUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
