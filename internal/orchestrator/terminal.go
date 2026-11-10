package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// TerminalHub owns the PTY-backed terminal for one session: at most one shell
// at a time, shared by every attached websocket client (the same session
// viewed from two windows shares one terminal), torn down with the daemon.
type TerminalHub struct {
	mu        sync.Mutex
	terminals map[SessionID]*sessionTerminal
}

type sessionTerminal struct {
	hub    *TerminalHub
	id     SessionID
	dir    string
	cmd    *exec.Cmd
	ptmx   *os.File
	cancel context.CancelFunc

	writeMu sync.Mutex
	clients int
}

// NewTerminalHub returns an empty hub.
func NewTerminalHub() *TerminalHub {
	return &TerminalHub{terminals: make(map[SessionID]*sessionTerminal)}
}

// Open upgrades the websocket request and bridges it to the session's PTY:
// the first client starts `SHELL -i` inside the worktree; further clients
// attach to the same terminal.
func (h *TerminalHub) Open(w http.ResponseWriter, r *http.Request, id SessionID, dir string) error {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusInternalError, "terminal closed")

	term, err := h.acquire(id, dir)
	if err != nil {
		_ = conn.Close(websocket.StatusUnsupportedData, err.Error())
		return err
	}

	term.attach()
	defer term.detach()

	// Reader: client keystrokes into the PTY.
	go func() {
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if _, err := term.ptmx.Write(data); err != nil {
				return
			}
		}
	}()

	// Writer: PTY output to the client, one line/burst at a time.
	buf := make([]byte, 4096)
	for {
		n, err := term.ptmx.Read(buf)
		if err != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "shell exited")
			return nil
		}
		if err := conn.Write(r.Context(), websocket.MessageBinary, buf[:n]); err != nil {
			return nil
		}
	}
}

// acquire returns the session's terminal, starting it when this is the first
// attach. The worktree anchors the shell; the daemon's lifecycle bounds it.
func (h *TerminalHub) acquire(id SessionID, dir string) (*sessionTerminal, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if term, ok := h.terminals[id]; ok {
		return term, nil
	}
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("embedded terminal requires a Windows ConPTY build (pending); use the worktree terminal button for now")
	}
	if dir == "" {
		return nil, fmt.Errorf("session has no worktree")
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("worktree not accessible: %w", err)
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, shell, "-i")
	cmd.Dir = dir
	applyProcessPlatformTraits(cmd)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("pty start: %w", err)
	}

	term := &sessionTerminal{hub: h, id: id, dir: dir, cmd: cmd, ptmx: ptmx, cancel: cancel}
	h.terminals[id] = term
	go func() {
		// When the shell exits, drop the terminal so the next attach starts
		// a fresh one.
		_ = cmd.Wait()
		h.mu.Lock()
		if h.terminals[id] == term {
			delete(h.terminals, id)
		}
		h.mu.Unlock()
		_ = ptmx.Close()
		cancel()
	}()
	return term, nil
}

func (t *sessionTerminal) attach() {
	t.writeMu.Lock()
	t.clients++
	t.writeMu.Unlock()
}

func (t *sessionTerminal) detach() {
	t.writeMu.Lock()
	t.clients--
	// The last client leaving keeps the shell alive: the terminal belongs to
	// the session, not to a window. Stop is the explicit way to end it.
	t.writeMu.Unlock()
}

// Stop kills the session's terminal shell, if any.
func (h *TerminalHub) Stop(id SessionID) {
	h.mu.Lock()
	term, ok := h.terminals[id]
	if ok {
		delete(h.terminals, id)
	}
	h.mu.Unlock()
	if ok {
		_ = term.cmd.Process.Kill()
	}
}
