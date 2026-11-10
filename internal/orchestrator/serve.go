package orchestrator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
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
	// RepoPath is the local checkout used to create worker worktrees. When
	// set, POST /sessions accepts launch:true and spawns agent CLIs.
	RepoPath string
	// BaseBranch is what worker worktrees branch from (default main).
	BaseBranch string
	// PrintBoard renders the text board to stdout on every poll.
	PrintBoard bool
	// ConfigPath overrides where orchestrator.json lives (tests).
	ConfigPath string
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
	detector := NewAgentDetector(nil, 0)
	projects := NewProjects()

	journal := NewJournal()
	var launcher *Launcher
	if opts.RepoPath != "" {
		base := opts.BaseBranch
		if base == "" {
			base = "main"
		}
		lifecycle, cancelLifecycle := context.WithCancel(ctx)
		launcher = &Launcher{
			Root:        lifecycle,
			Fleet:       fleet,
			Router:      router,
			Journal:     journal,
			Worktrees:   &WorktreeManager{Repo: opts.RepoPath, Parent: filepath.Join(opts.RepoPath, ".orchestrator-worktrees")},
			Base:        base,
			ReceiptGate: true,
			Projects:    projects,
		}
		// Workers die with the daemon: canceling the lifecycle context kills
		// every running worker process (see Launcher.Root).
		go func() {
			<-ctx.Done()
			cancelLifecycle()
		}()
	}

	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: listen %s: %w", opts.ListenAddr, err)
	}

	handle := &ServeHandle{Addr: ln.Addr().String(), Done: make(chan error, 1)}
	server := &http.Server{Handler: newDaemonServer(fleet, journal, launcher, detector, nil, &configStore{path: configPath(opts)})}
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

// configPath anchors orchestrator.json beside the daemon binary so the
// shell re-reads it on restart; tests can override it.
func configPath(opts ServeOptions) string {
	if opts.ConfigPath != "" {
		return opts.ConfigPath
	}
	return defaultConfigPath()
}
