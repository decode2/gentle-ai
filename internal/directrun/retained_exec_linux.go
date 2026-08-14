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

// runRetainedCommand never passes a mutable executable or cwd pathname to the
// child. The helper fchdirs and execs the already-open descriptors.
func runRetainedCommand(ctx context.Context, repo string, command Command, timeout time.Duration) (ExecResult, error) {
	if command.validate(0) != nil || !retainedProcFSAvailable() {
		return ExecResult{}, ErrCommandTargetUnsupported
	}
	program, err := retainedProgramFor(command.Argv[0])
	if err != nil {
		return ExecResult{}, ErrCommandTargetUnsupported
	}
	defer unix.Close(program.target)
	if program.script >= 0 {
		defer unix.Close(program.script)
		defer unix.Close(program.interpreter)
	}
	cwd, err := unix.Open(command.CWD, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ExecResult{}, ErrOperationUnavailable
	}
	defer unix.Close(cwd)
	relative, relativeErr := filepath.Rel(filepath.Clean(repo), filepath.Clean(command.CWD))
	if relativeErr != nil || relative == ".." || len(relative) >= 3 && relative[:3] == "../" {
		return ExecResult{}, ErrOperationUnavailable
	}
	selfPath, err := os.Executable()
	if err != nil {
		return ExecResult{}, ErrCommandTargetUnsupported
	}
	self, err := unix.Open(selfPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ExecResult{}, ErrCommandTargetUnsupported
	}
	defer unix.Close(self)
	// Do not inherit TMPDIR: it is worker-controllable process state.
	home, err := os.MkdirTemp("/tmp", "gentle-ai-direct-home-")
	if err != nil {
		return ExecResult{}, ErrOperationUnavailable
	}

	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	child := exec.Command(retainedProcFD(3), append([]string{"_direct-run-retained", program.mode}, command.Argv...)...)
	child.Args = append([]string{"gentle-ai-retained", "_direct-run-retained", program.mode}, command.Argv...)
	extra := []*os.File{os.NewFile(uintptr(self), "self"), os.NewFile(uintptr(program.target), "target"), os.NewFile(uintptr(cwd), "cwd")}
	if program.script >= 0 {
		extra = append(extra, os.NewFile(uintptr(program.script), "script"), os.NewFile(uintptr(program.interpreter), "interpreter"))
	}
	child.ExtraFiles = extra
	child.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OPTIONAL_LOCKS=0"}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, stderr := &retainedOutput{}, &retainedOutput{}
	child.Stdout, child.Stderr = stdout, stderr
	if err := child.Start(); err != nil {
		_ = os.RemoveAll(home)
		return ExecResult{}, ErrOperationUnavailable
	}
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
		return ExecResult{}, ErrOperationUnavailable
	}
	if err := os.RemoveAll(home); err != nil {
		return ExecResult{}, ErrOperationUnavailable
	}
	if stdout.overflow || stderr.overflow {
		return ExecResult{}, ErrOperationLimit
	}
	if commandContext.Err() != nil {
		return ExecResult{}, commandContext.Err()
	}
	exitCode := 0
	if waitErr != nil {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			return ExecResult{}, ErrOperationUnavailable
		}
		exitCode = exit.ExitCode()
	}
	return NewExecResult(exitCode, append(stdout.bytes(), stderr.bytes()...))
}

func retainedProgramFor(name string) (retainedProgram, error) {
	path := ""
	switch name {
	case "go":
		path = filepath.Join(runtime.GOROOT(), "bin", "go")
	case "git":
		path = retainedKnownPath("/usr/bin/git", "/bin/git")
	case "npm":
		path = retainedKnownPath("/usr/bin/npm", "/bin/npm")
	case "pytest":
		path = retainedKnownPath("/usr/bin/pytest", "/bin/pytest", "/usr/local/bin/pytest")
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
	interpreter, err := retainedScriptInterpreter(name, fd)
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

func retainedScriptInterpreter(target string, fd int) (int, error) {
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
		interpreter = retainedKnownPath("/usr/bin/node", "/bin/node")
	case target == "pytest" && (line == "#!/usr/bin/python" || line == "#!/usr/bin/python3"):
		interpreter = strings.TrimPrefix(line, "#!")
	case target == "pytest" && (line == "#!/usr/bin/env python" || line == "#!/usr/bin/env python3"):
		if line == "#!/usr/bin/env python" {
			interpreter = retainedKnownPath("/usr/bin/python", "/bin/python")
		} else {
			interpreter = retainedKnownPath("/usr/bin/python3", "/bin/python3")
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
	if err := unix.Fchdir(5); err != nil {
		return ErrCommandTargetUnsupported
	}
	args := os.Args
	if len(args) < 4 || args[1] != "_direct-run-retained" || !allowedCommand(args[3:]) {
		return ErrOperationUnsupported
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
