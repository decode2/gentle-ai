package orchestrator

import (
	"context"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// terminalIO is the platform-neutral handle of a worker's pseudo terminal:
// an os.File PTY on Unix, a ConPTY on Windows.
type terminalIO interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

// TerminalHub owns the PTY-backed terminal for one session: at most one shell
// at a time, shared by every attached websocket client (the same session
// viewed from two windows shares one terminal), torn down with the daemon.
type TerminalHub struct {
	mu        sync.Mutex
	terminals map[SessionID]*sessionTerminal
}

// sessionTerminal is one live interactive shell anchored in a worktree.
type sessionTerminal struct {
	hub    *TerminalHub
	id     SessionID
	dir    string
	io     terminalIO
	cancel context.CancelFunc
	exited chan struct{} // closed when the shell dies

	writeMu sync.Mutex
	clients int
}

// NewTerminalHub returns an empty hub.
func NewTerminalHub() *TerminalHub {
	return &TerminalHub{terminals: make(map[SessionID]*sessionTerminal)}
}

// Open upgrades the websocket request and bridges it to the session's PTY:
// the first client starts the shell inside the worktree; further clients
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
			if _, err := term.io.Write(data); err != nil {
				return
			}
		}
	}()

	// Writer: PTY output to the client, one burst at a time.
	buf := make([]byte, 4096)
	for {
		n, err := term.io.Read(buf)
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
	if term, ok := h.terminals[id]; ok {
		h.mu.Unlock()
		return term, nil
	}
	h.mu.Unlock()

	if dir == "" {
		return nil, errNoWorktree
	}
	if statErr := statDir(dir); statErr != nil {
		return nil, errNoWorktree
	}

	ctx, cancel := context.WithCancel(context.Background())
	term, err := newSessionTerminal(ctx, dir)
	if err != nil {
		cancel()
		return nil, err
	}
	term.hub, term.id, term.dir, term.cancel = h, id, dir, cancel

	h.mu.Lock()
	h.terminals[id] = term
	h.mu.Unlock()

	// When the shell exits — or the daemon dies — drop the terminal so the
	// next attach starts a fresh one.
	go func() {
		select {
		case <-term.exited:
		case <-ctx.Done():
			_ = term.io.Close()
		}
		h.mu.Lock()
		if h.terminals[id] == term {
			delete(h.terminals, id)
		}
		h.mu.Unlock()
		_ = term.io.Close()
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
		_ = term.io.Close()
	}
}
