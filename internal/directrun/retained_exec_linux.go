//go:build linux

package directrun

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const retainedOutputLimit = 1 << 20
const retainedShebangLimit = 256

type retainedProgram struct {
	target      int
	script      int
	interpreter int
	mode        string
}

// retainedCapture is private process evidence. Public direct_exec deliberately
// reduces it to a digest before crossing its operation boundary.
type retainedCapture struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

// retainedExecTestHook is available only inside this package. Keeping it on the
// request context makes replacement tests deterministic without global state.
type retainedExecTestHook struct {
	executable     func() (string, error)
	program        func(string) (retainedProgram, error)
	tempHome       func() (string, error)
	afterRetention func()
	observeOutput  func(stdout, stderr []byte)
	procFS         func() bool
	extraFiles     func(int, retainedProgram, int) ([]*os.File, error)
	start          func(*exec.Cmd) error
}

type retainedExecTestHookKey struct{}

var defaultRetainedExecTestHook = retainedExecTestHook{
	executable: os.Executable,
	program:    retainedProgramFor,
	tempHome: func() (string, error) {
		return os.MkdirTemp("/tmp", "gentle-ai-direct-home-")
	},
	afterRetention: func() {
	},
	procFS:     retainedProcFSAvailable,
	extraFiles: retainedExtraFiles,
	start:      func(child *exec.Cmd) error { return child.Start() },
}

// runRetainedCommand never passes a mutable executable or cwd pathname to the
// child. The helper fchdirs and execs the already-open descriptors.
func runRetainedCommand(ctx context.Context, repo string, command Command, timeout time.Duration) (ExecResult, error) {
	hook := retainedExecHook(ctx)
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}
	if command.validate(0) != nil || !hook.procFS() {
		return ExecResult{}, ErrCommandTargetUnsupported
	}
	program, err := hook.program(command.Argv[0])
	if err != nil {
		return ExecResult{}, ErrCommandTargetUnsupported
	}
	defer retainedCloseProgram(program)
	cwd, err := unix.Open(command.CWD, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ExecResult{}, ErrOperationUnavailable
	}
	defer unix.Close(cwd)
	relative, relativeErr := filepath.Rel(filepath.Clean(repo), filepath.Clean(command.CWD))
	if relativeErr != nil || relative == ".." || len(relative) >= 3 && relative[:3] == "../" {
		return ExecResult{}, ErrOperationUnavailable
	}
	capture, err := runRetainedProgram(ctx, hook, program, cwd, command.Argv, timeout)
	if err != nil {
		return ExecResult{}, err
	}
	return NewExecResult(capture.exitCode, append(capture.stdout, capture.stderr...))
}

func retainedCloseProgram(program retainedProgram) {
	_ = unix.Close(program.target)
	if program.script >= 0 {
		_ = unix.Close(program.script)
		_ = unix.Close(program.interpreter)
	}
}

// runRetainedProgram executes only a descriptor-backed, caller-owned program
// and cwd. Its bytes never leave this package except to trusted internal users.
func runRetainedProgram(ctx context.Context, hook retainedExecTestHook, program retainedProgram, cwd int, argv []string, timeout time.Duration) (retainedCapture, error) {
	if len(argv) == 0 || !retainedAllowedArgv(argv) {
		return retainedCapture{}, ErrCommandTargetUnsupported
	}
	selfPath, err := hook.executable()
	if err != nil {
		return retainedCapture{}, ErrCommandTargetUnsupported
	}
	self, err := unix.Open(selfPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return retainedCapture{}, ErrCommandTargetUnsupported
	}
	defer unix.Close(self)
	hook.afterRetention()
	if err := ctx.Err(); err != nil {
		return retainedCapture{}, err
	}
	// Do not inherit TMPDIR: it is worker-controllable process state.
	home, err := hook.tempHome()
	if err != nil {
		return retainedCapture{}, ErrOperationUnavailable
	}

	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	child := exec.Command(retainedProcFD(3), append([]string{"_direct-run-retained", program.mode}, argv...)...)
	child.Args = append([]string{"gentle-ai-retained", "_direct-run-retained", program.mode}, argv...)
	extra, err := hook.extraFiles(self, program, cwd)
	if err != nil {
		_ = os.RemoveAll(home)
		return retainedCapture{}, ErrOperationUnavailable
	}
	child.ExtraFiles = extra
	child.Env = retainedEnvironment(home)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, stderr := &retainedOutput{}, &retainedOutput{}
	child.Stdout, child.Stderr = stdout, stderr
	if err := hook.start(child); err != nil {
		retainedCloseExtraFiles(extra)
		_ = os.RemoveAll(home)
		return retainedCapture{}, ErrOperationUnavailable
	}
	retainedCloseExtraFiles(extra)
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-commandContext.Done():
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)
		select {
		case waitErr = <-done:
		case <-time.After(250 * time.Millisecond):
			_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
			waitErr = <-done
		}
	}
	// The direct child may have exited before its process-group descendants.
	// Reap the child above, then terminate any remaining in-group processes.
	if err := syscall.Kill(-child.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = os.RemoveAll(home)
		return retainedCapture{}, ErrOperationUnavailable
	}
	if err := os.RemoveAll(home); err != nil {
		return retainedCapture{}, ErrOperationUnavailable
	}
	if hook.observeOutput != nil {
		hook.observeOutput(stdout.bytes(), stderr.bytes())
	}
	if stdout.overflow || stderr.overflow {
		return retainedCapture{}, ErrOperationLimit
	}
	if commandContext.Err() != nil {
		return retainedCapture{}, commandContext.Err()
	}
	exitCode := 0
	if waitErr != nil {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			return retainedCapture{}, ErrOperationUnavailable
		}
		exitCode = exit.ExitCode()
	}
	return retainedCapture{stdout: stdout.bytes(), stderr: stderr.bytes(), exitCode: exitCode}, nil
}

func retainedEnvironment(home string) []string {
	return []string{
		"HOME=" + home, "PATH=/usr/bin:/bin", "LC_ALL=C", "XDG_CONFIG_HOME=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false", "GCM_INTERACTIVE=Never",
		"GIT_PAGER=cat", "PAGER=cat", "GIT_EXTERNAL_DIFF=", "GIT_DIFF_EXTERNAL=",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_ALTERNATE_OBJECT_DIRECTORIES=",
		"GIT_TRACE=0", "GIT_TRACE_PERFORMANCE=0", "GIT_TRACE_PACKET=0", "GIT_TRACE_SETUP=0",
	}
}

func retainedAllowedArgv(argv []string) bool {
	return allowedCommand(argv) || retainedGitArgv(argv)
}

func retainedGitArgv(argv []string) bool {
	if len(argv) < 2 || argv[0] != "git" {
		return false
	}
	return strings.Join(argv[1:], "\x00") == "status\x00--porcelain=v2\x00-z\x00--branch" ||
		strings.Join(argv[1:], "\x00") == "diff\x00--name-status\x00-z\x00--find-renames\x00--no-ext-diff\x00--no-textconv" ||
		strings.Join(argv[1:], "\x00") == "diff\x00--cached\x00--name-status\x00-z\x00--find-renames\x00--no-ext-diff\x00--no-textconv" ||
		strings.Join(argv[1:], "\x00") == "diff\x00--numstat\x00-z\x00--find-renames\x00--no-ext-diff\x00--no-textconv" ||
		strings.Join(argv[1:], "\x00") == "diff\x00--cached\x00--numstat\x00-z\x00--find-renames\x00--no-ext-diff\x00--no-textconv"
}

func retainedExecHook(ctx context.Context) retainedExecTestHook {
	hook := defaultRetainedExecTestHook
	testHook, ok := ctx.Value(retainedExecTestHookKey{}).(retainedExecTestHook)
	if !ok {
		return hook
	}
	if testHook.executable != nil {
		hook.executable = testHook.executable
	}
	if testHook.program != nil {
		hook.program = testHook.program
	}
	if testHook.tempHome != nil {
		hook.tempHome = testHook.tempHome
	}
	if testHook.afterRetention != nil {
		hook.afterRetention = testHook.afterRetention
	}
	if testHook.observeOutput != nil {
		hook.observeOutput = testHook.observeOutput
	}
	if testHook.procFS != nil {
		hook.procFS = testHook.procFS
	}
	if testHook.extraFiles != nil {
		hook.extraFiles = testHook.extraFiles
	}
	if testHook.start != nil {
		hook.start = testHook.start
	}
	return hook
}

func retainedExtraFiles(self int, program retainedProgram, cwd int) ([]*os.File, error) {
	descriptors := []struct {
		fd   int
		name string
	}{{self, "self"}, {program.target, "target"}, {cwd, "cwd"}}
	if program.script >= 0 {
		descriptors = append(descriptors, struct {
			fd   int
			name string
		}{program.script, "script"}, struct {
			fd   int
			name string
		}{program.interpreter, "interpreter"})
	}
	files := make([]*os.File, 0, len(descriptors))
	for _, descriptor := range descriptors {
		duplicate, err := unix.Dup(descriptor.fd)
		if err != nil {
			retainedCloseExtraFiles(files)
			return nil, err
		}
		files = append(files, os.NewFile(uintptr(duplicate), descriptor.name))
	}
	return files, nil
}

func retainedCloseExtraFiles(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

func retainedProgramFor(name string) (retainedProgram, error) {
	return retainedProgramForWithKnownPath(name, retainedKnownPath)
}

func retainedProgramForWithKnownPath(name string, knownPath func(...string) string) (retainedProgram, error) {
	path := ""
	switch name {
	case "go":
		path = filepath.Join(runtime.GOROOT(), "bin", "go")
	case "git":
		path = knownPath("/usr/bin/git", "/bin/git")
	case "npm":
		path = knownPath("/usr/bin/npm", "/bin/npm")
	case "pytest":
		path = knownPath("/usr/bin/pytest", "/bin/pytest", "/usr/local/bin/pytest")
	default:
		return retainedProgram{}, errors.New("unsupported target")
	}
	if path == "" {
		return retainedProgram{}, errors.New("target unavailable")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return retainedProgram{}, err
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o111 == 0 {
		unix.Close(fd)
		return retainedProgram{}, errors.New("unsafe target")
	}
	if stat.Mode&0o111 != 0 && retainedELF(fd) {
		return retainedProgram{target: fd, script: -1, interpreter: -1, mode: "elf"}, nil
	}
	interpreter, err := retainedScriptInterpreterWithKnownPath(name, fd, knownPath)
	if err != nil {
		unix.Close(fd)
		return retainedProgram{}, err
	}
	script, err := unix.Dup(fd)
	if err != nil {
		unix.Close(interpreter)
		unix.Close(fd)
		return retainedProgram{}, err
	}
	return retainedProgram{target: fd, script: script, interpreter: interpreter, mode: "script"}, nil
}

func retainedKnownPath(candidates ...string) string {
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func retainedELF(fd int) bool {
	var header [4]byte
	_, err := unix.Pread(fd, header[:], 0)
	return err == nil && string(header[:]) == "\x7fELF"
}

func retainedScriptInterpreterWithKnownPath(target string, fd int, knownPath func(...string) string) (int, error) {
	buf := make([]byte, retainedShebangLimit)
	n, err := unix.Pread(fd, buf, 0)
	if err != nil || !strings.HasPrefix(string(buf[:n]), "#!") {
		return -1, errors.New("malformed script")
	}
	line, _, found := strings.Cut(string(buf[:n]), "\n")
	if !found || len(line) >= retainedShebangLimit || strings.Contains(line, "\r") {
		return -1, errors.New("malformed script")
	}
	var interpreter string
	switch {
	case target == "npm" && line == "#!/usr/bin/env node":
		interpreter = knownPath("/usr/bin/node", "/bin/node")
	case target == "pytest" && (line == "#!/usr/bin/python" || line == "#!/usr/bin/python3"):
		interpreter = knownPath(strings.TrimPrefix(line, "#!"))
	case target == "pytest" && (line == "#!/usr/bin/env python" || line == "#!/usr/bin/env python3"):
		if line == "#!/usr/bin/env python" {
			interpreter = knownPath("/usr/bin/python", "/bin/python")
		} else {
			interpreter = knownPath("/usr/bin/python3", "/bin/python3")
		}
	default:
		return -1, errors.New("unsupported script interpreter")
	}
	if interpreter == "" {
		return -1, errors.New("interpreter unavailable")
	}
	interpreterFD, err := unix.Open(interpreter, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil || !retainedELF(interpreterFD) {
		if err == nil {
			unix.Close(interpreterFD)
		}
		return -1, errors.New("unsafe interpreter")
	}
	return interpreterFD, nil
}

func retainedProcFSAvailable() bool {
	fd, err := unix.Open("/proc/self/fd", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer unix.Close(fd)
	return true
}
func retainedProcFD(fd int) string { return "/proc/self/fd/" + strconv.Itoa(fd) }

// RunRetainedHelper is only reachable through the descriptor-backed internal
// dispatch above. There is intentionally no pathname fallback.
func RunRetainedHelper() error {
	if !retainedProcFSAvailable() {
		return ErrCommandTargetUnsupported
	}
	args := os.Args
	if len(args) < 4 || args[1] != "_direct-run-retained" || !retainedAllowedArgv(args[3:]) {
		return ErrOperationUnsupported
	}
	if err := unix.Fchdir(5); err != nil {
		return ErrCommandTargetUnsupported
	}
	if args[2] == "elf" {
		if err := syscall.Exec(retainedProcFD(4), args[3:], os.Environ()); err != nil {
			return ErrCommandTargetUnsupported
		}
		return nil
	}
	if args[2] != "script" {
		return ErrCommandTargetUnsupported
	}
	interpreterArgs := append([]string{retainedProcFD(7), retainedProcFD(6)}, args[4:]...)
	if err := syscall.Exec(retainedProcFD(7), interpreterArgs, os.Environ()); err != nil {
		return ErrCommandTargetUnsupported
	}
	return nil
}

type retainedOutput struct {
	data     []byte
	overflow bool
}

func (w *retainedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.data)+n > retainedOutputLimit {
		w.overflow = true
		remaining := retainedOutputLimit - len(w.data)
		if remaining > 0 {
			w.data = append(w.data, p[:remaining]...)
		}
		return n, nil
	}
	w.data = append(w.data, p...)
	return n, nil
}
func (w *retainedOutput) bytes() []byte { return append([]byte(nil), w.data...) }

var _ io.Writer = (*retainedOutput)(nil)
