//go:build windows

package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/UserExistsError/conpty"
)

var errNoWorktree = errors.New("session has no accessible worktree")

func statDir(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	_, err = os.Stat(absolute)
	return err
}

// newSessionTerminal starts cmd.exe attached to a real Windows ConPTY, so
// the embedded terminal runs the interactive shell like on Unix. The ConPTY
// child inherits the daemon's kill-on-close Job Object.
func newSessionTerminal(ctx context.Context, dir string) (*sessionTerminal, error) {
	p, err := conpty.Start("cmd.exe", conpty.ConPtyDimensions(120, 32))
	if err != nil {
		return nil, err
	}

	term := &sessionTerminal{io: p, exited: make(chan struct{})}
	go func() {
		_, _ = p.Wait(ctx)
		close(term.exited)
	}()
	return term, nil
}
