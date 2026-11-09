// Command orchestratord runs the Gentle-AI orchestrator daemon: it serves the
// fleet over local HTTP (GET /board, GET /board/live via SSE, POST /sessions)
// and polls the repository's pull requests so workers get their CI and review
// feedback routed back to their own session.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gentleman-programming/gentle-ai/v3/internal/orchestrator"
)

func main() {
	repo := flag.String("repo", "", "OWNER/REPOSITORY the watcher polls via gh (required)")
	repoPath := flag.String("repo-path", "", "local checkout path; enables launching workers in their own worktrees")
	listen := flag.String("listen", "127.0.0.1:8094", "host:port for the local daemon")
	poll := flag.Duration("poll", orchestrator.DefaultPollInterval, "watcher poll cadence")
	printBoard := flag.Bool("print-board", false, "print the text board to stdout on every poll")
	flag.Parse()

	if *repo == "" {
		fmt.Fprintln(os.Stderr, "orchestratord: --repo OWNER/REPOSITORY is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handle, err := orchestrator.Serve(ctx, orchestrator.ServeOptions{
		Repo:         *repo,
		RepoPath:     *repoPath,
		ListenAddr:   *listen,
		PollInterval: *poll,
		PrintBoard:   *printBoard,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "orchestratord: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("orchestratord: fleet board on http://%s/board (live stream at /board/live)\n", handle.Addr)

	if err := <-handle.Done; err != nil && err != ctx.Err() {
		fmt.Fprintf(os.Stderr, "orchestratord: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("orchestratord: stopped")
}
