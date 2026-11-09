package orchestrator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// DefaultFixCycles bounds how many automatic fix attempts a session gets
// before the router escalates to the human.
const DefaultFixCycles = 3

// DefaultPollInterval is the watcher cadence when none is configured.
const DefaultPollInterval = 30 * time.Second

// ServeOptions configures Serve.
type ServeOptions struct {
	// Repo is the OWNER/REPOSITORY handle the watcher polls via gh.
	Repo string
	// ListenAddr is the host:port for the local daemon, e.g. 127.0.0.1:8080.
	ListenAddr string
	// PollInterval is the watcher cadence; zero falls back to the default.
	PollInterval time.Duration
	// Source replaces the default GhPullSource; used by tests.
	Source PullSource
	// PrintBoard renders the text board to stdout on every poll.
	PrintBoard bool
}

// ServeHandle is the running daemon: Addr is live for the whole run and Done
// receives the drain result once the context is canceled.
type ServeHandle struct {
	Addr string
	Done chan error
}

// Serve wires the Phase 0 fleet — fleet, router, watcher, HTTP daemon with
// its live board stream — binds opts.ListenAddr (":0" picks a free port) and
// returns immediately with the handle. Cancel ctx to stop; Done reports
// ctx.Err() after the server drained.
func Serve(ctx context.Context, opts ServeOptions) (*ServeHandle, error) {
	fleet := NewFleet()
	router := NewRouter(DefaultFixCycles)

	source := opts.Source
	if source == nil {
		source = GhPullSource{Repo: opts.Repo}
	}
	interval := opts.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	watcher := NewWatcher(fleet, router, source, interval)

	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: listen %s: %w", opts.ListenAddr, err)
	}

	handle := &ServeHandle{Addr: ln.Addr().String(), Done: make(chan error, 1)}
	server := &http.Server{Handler: NewDaemonHTTP(fleet)}
	go func() {
		serveErr := server.Serve(ln)
		if serveErr != nil && serveErr != http.ErrServerClosed {
			handle.Done <- serveErr
			return
		}
		handle.Done <- nil
	}()

	go func() {
		for {
			pollErr := watcher.Poll(ctx)
			if opts.PrintBoard {
				if pollErr != nil {
					fmt.Printf("watcher poll: %v\n", pollErr)
				}
				fmt.Print(RenderBoard(fleet.Board()))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return handle, nil
}
