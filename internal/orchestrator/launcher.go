package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// workProcess wraps one running worker process.
type workProcess struct {
	cmd *exec.Cmd
}

// Launcher turns a session request into a running worker: it creates the
// task's worktree, registers the session, spawns the agent CLI there, and
// records everything in the journal. Worker stdout lines that start with
// "QUESTION:" route to the session's inbox so the human sees them.
type Launcher struct {
	// Root is the launcher's lifecycle context: workers derive their process
	// context from it, so canceling Root (daemon shutdown, app exit) kills
	// every running worker. When nil, StartTask's ctx is used.
	Root      context.Context
	Fleet     *Fleet
	Router    *Router
	Journal   *Journal
	Worktrees *WorktreeManager
	// Base is the branch workers start from (never a protected branch as
	// the worker's own branch; the manager refuses that).
	Base string
	// Command builds the worker process; tests replace it. The default
	// spawns the agent CLI with the task text.
	Command func(ctx context.Context, s Session, dir string) *workProcess
	// Engram publishes fleet memory when set (PRD F5); failures degrade.
	Engram *EngramBridge
	// ReceiptGate requires the frozen evidence receipt before a launched
	// session may graduate to ready-to-merge (F4). Default on.
	ReceiptGate bool
	// Projects resolves per-project worktree managers for multi-repo fleets;
	// nil or an unknown project falls back to the fixed Worktrees.
	Projects *Projects

	mu       sync.Mutex
	running  map[SessionID]*workProcess
	stopping map[SessionID]bool
}

// agentArgTemplates maps an agent id to its headless invocation prefix; the
// task text is always the last argument. Per-agent argv refinements belong
// to the adapters phase; these cover the common CLIs honestly.
var agentArgTemplates = map[string][]string{
	"pi":     {"--print", "--mode", "text"},
	"claude": {"-p"},
	"codex":  {"exec"},
	"gemini": {},
	"aider":  {"--message"},
}

func defaultAgentCommand(ctx context.Context, s Session, dir string) *workProcess {
	args := append([]string(nil), agentArgTemplates[s.Agent]...)
	args = append(args, workerPrompt(s))
	invocation := agentInvocation(resolveAgentExecutable(s.Agent), runtime.GOOS)
	cmd := exec.CommandContext(ctx, invocation.exe, append(invocation.prefix, args...)...)
	cmd.Dir = dir
	applyProcessPlatformTraits(cmd)
	return &workProcess{cmd: cmd}
}

// agentInvocation turns a resolved executable path into the process to run:
// npm shims (.cmd/.bat) cannot exec directly on Windows, they need
// "cmd /c"; everything else (and every unix path) runs as-is.
func agentInvocation(resolved, goos string) struct {
	exe    string
	prefix []string
} {
	lower := strings.ToLower(resolved)
	if goos == "windows" && (strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")) {
		return struct {
			exe    string
			prefix []string
		}{exe: "cmd", prefix: []string{"/c", resolved}}
	}
	return struct {
		exe    string
		prefix []string
	}{exe: resolved}
}

// resolveAgentExecutable is the seam for tests: LookPath by default.
var resolveAgentExecutable = func(command string) string {
	path, err := exec.LookPath(command)
	if err != nil {
		return command
	}
	return path
}

// StartTask validates, creates the worktree, registers the session, and
// launches the worker in the background. It returns after launch, not after
// the worker finishes; the journal carries the run.
func (l *Launcher) StartTask(ctx context.Context, session Session) error {
	if err := validateBranch(session.Branch); err != nil {
		return err
	}
	manager := l.Worktrees
	if l.Projects != nil && session.Project != "" {
		resolved, ok := l.Projects.ManagerFor(session.Project)
		if !ok {
			return fmt.Errorf("orchestrator: unknown project %q", session.Project)
		}
		manager = resolved
	}
	if _, err := l.Fleet.Get(session.ID); err == nil {
		return ErrDuplicateSession
	}

	path, err := manager.Create(session.Branch, l.Base)
	if err != nil {
		return err
	}
	session.Worktree = path
	session.Base = l.Base
	if err := l.Fleet.Start(session); err != nil {
		_ = l.Worktrees.Remove(session.Branch)
		return err
	}
	l.Journal.Append(session.ID, "launched", path)
	if l.Engram != nil {
		_ = l.Engram.StartSession(ctx, session)
	}

	l.mu.Lock()
	if l.running == nil {
		l.running = make(map[SessionID]*workProcess)
		l.stopping = make(map[SessionID]bool)
	}
	workerCtx := l.Root
	if workerCtx == nil {
		workerCtx = ctx
	}
	command := l.Command
	if command == nil {
		command = defaultAgentCommand
	}
	wp := command(workerCtx, session, session.Worktree)
	l.running[session.ID] = wp
	l.mu.Unlock()

	l.Fleet.Update(session.ID, func(f *Facts) {
		f.Activity.Running = true
		if l.ReceiptGate {
			f.ReceiptRequired = true
		}
	})
	go l.run(session, wp)
	return nil
}

// run records the worker's output until exit. The process was already
// registered by StartTask; a stop that arrived first wins the journal.
func (l *Launcher) run(session Session, wp *workProcess) {
	l.mu.Lock()
	stopping := l.stopping[session.ID]
	l.mu.Unlock()

	if stopping {
		l.Journal.Append(session.ID, "stopped", "stopped before launch")
		l.freezeReceipt(session, "Detenido sin iniciar")
		l.finish(session, "worker stopped before launch")
		return
	}

	applyProcessPlatformTraits(wp.cmd)
	stdout, err := wp.cmd.StdoutPipe()
	if err == nil {
		err = wp.cmd.Start()
	}
	if err != nil {
		l.Journal.Append(session.ID, "exited", fmt.Sprintf("launch failed: %v", err))
		l.finish(session, fmt.Sprintf("worker launch failed: %v", err))
		return
	}

	// A stop that raced the launch wins here: kill the just-started process
	// so the wait below observes it.
	l.mu.Lock()
	stopping = l.stopping[session.ID]
	l.mu.Unlock()
	if stopping {
		_ = wp.cmd.Process.Kill()
	}

	var wg sync.WaitGroup
	scan := func(reader interface{ Read([]byte) (int, error) }) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			l.Journal.Append(session.ID, "output", line)
			if question, ok := strings.CutPrefix(line, "QUESTION:"); ok {
				_ = l.Router.Route(Event{Kind: EventQuestion, Session: session.ID, Note: strings.TrimSpace(question)})
				continue
			}
			if decision, ok := strings.CutPrefix(line, "DECISION:"); ok {
				text := strings.TrimSpace(decision)
				l.Journal.Append(session.ID, "decision", text)
				if l.Engram != nil {
					_ = l.Engram.PublishDecision(context.Background(), session, text)
				}
				continue
			}
			if summary, ok := strings.CutPrefix(line, "SUMMARY:"); ok {
				l.Journal.Append(session.ID, "summary", strings.TrimSpace(summary))
				continue
			}
		}
	}
	wg.Add(2)
	go scan(stdout)
	stderr, _ := wp.cmd.StderrPipe()
	if stderr != nil {
		go scan(stderr)
	} else {
		wg.Done()
	}
	wg.Wait()

	waitErr := wp.cmd.Wait()
	text := "ok"
	if waitErr != nil {
		text = waitErr.Error()
	}
	l.mu.Lock()
	wasStopping := l.stopping[session.ID]
	delete(l.running, session.ID)
	l.mu.Unlock()
	if wasStopping {
		l.Journal.Append(session.ID, "stopped", text)
		l.freezeReceipt(session, "Detenido")
		l.finish(session, "worker stopped: "+text)
	} else {
		l.Journal.Append(session.ID, "exited", text)
		l.freezeReceipt(session, "Exitado")
		l.finish(session, "worker exited ("+text+"); task: "+session.Task+"; branch: "+session.Branch)
	}
}

// freezeReceipt stamps the session's evidence receipt at worker exit: the
// package's facts at this moment are what the receipt attests to. Freezing
// releases the evidence gate (F4).
func (l *Launcher) freezeReceipt(session Session, status string) {
	ev := Evidence{
		SessionID: string(session.ID),
		Task:      session.Task,
		Agent:     session.Agent,
		Branch:    session.Branch,
		Status:    status,
	}
	receipt := evidenceReceipt(ev)
	l.Journal.Append(session.ID, "receipt", receipt)
	_, _ = l.Fleet.Update(session.ID, func(f *Facts) { f.ReceiptValid = true })
	if l.Engram != nil {
		_ = l.Engram.PublishOutcome(context.Background(), session.ID, session.Task, "receipt frozen "+receipt)
	}
}

// finish marks the worker idle in the fleet and records the outcome in
// fleet memory when the bridge is configured.
func (l *Launcher) finish(session Session, summary string) {
	_, _ = l.Fleet.Update(session.ID, func(f *Facts) {
		f.Activity.Running = false
		f.Activity.Detail = ""
	})
	if l.Engram != nil {
		_ = l.Engram.PublishOutcome(context.Background(), session.ID, session.Task, summary)
	}
}

// Stop kills a running worker and its whole process tree. If the process
// has not started yet, the stopping flag makes the launch path kill it
// right after start. The journal records the stop; the worktree and branch
// survive for inspection.
func (l *Launcher) Stop(id SessionID) error {
	l.mu.Lock()
	wp, ok := l.running[id]
	if ok {
		l.stopping[id] = true
	}
	var process *os.Process
	if ok && wp.cmd.Process != nil {
		process = wp.cmd.Process
	}
	l.mu.Unlock()

	if !ok {
		return ErrUnknownSession
	}
	if process == nil {
		return nil
	}
	if err := killProcessTree(wp.cmd, process); err != nil {
		return err
	}
	// Unblock the output scanners: with the tree gone the pipes close on
	// their own, but a grandchild holding stdout would otherwise stall the
	// journal and the receipt freeze.
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for wp.cmd.ProcessState == nil && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		closeWorkerPipes(wp.cmd)
	}()
	return nil
}
