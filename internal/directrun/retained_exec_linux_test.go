//go:build linux

package directrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	args := os.Args
	if len(args) >= 2 && args[1] == "_direct-run-retained" {
		if err := RunRetainedHelper(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRetainedScriptInterpreterRejectsUnsafeHeaders(t *testing.T) {
	cases := []string{
		"", "#!/usr/bin/env node --inspect\n", "#!/usr/bin/env -S node\n", "#!/usr/bin/env node\r\n",
		"#!/usr/bin/env unknown\n", "#!/bin/sh\n", "#!/usr/bin/env python3 -O\n", "#!#!/usr/bin/env node\n",
		"#!" + strings.Repeat("x", retainedShebangLimit) + "\n",
	}
	for _, content := range cases {
		t.Run(content, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "script")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString(content); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			fd, err := unix.Open(file.Name(), unix.O_RDONLY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			if _, err := retainedScriptInterpreterWithKnownPath("npm", fd, retainedKnownPath); err == nil {
				t.Fatal("accepted unsafe script")
			}
		})
	}
}

func TestRetainedScriptChainsSurviveReplacement(t *testing.T) {
	for _, test := range []struct {
		name, target, shebang string
		argv                  []string
		interpreterCandidates []string
	}{
		{"npm-test", "npm", "#!/usr/bin/env node\n", []string{"npm", "test"}, []string{"/usr/bin/node", "/bin/node"}},
		{"npm-run-test", "npm", "#!/usr/bin/env node\n", []string{"npm", "run", "test"}, []string{"/usr/bin/node", "/bin/node"}},
		{"pytest-direct-python", "pytest", "#!/usr/bin/python3\n", []string{"pytest", "-q"}, []string{"/usr/bin/python3"}},
		{"pytest-env-python", "pytest", "#!/usr/bin/env python3\n", []string{"pytest", "--maxfail=1"}, []string{"/usr/bin/python3", "/bin/python3"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 1
			if test.target == "npm" {
				attempts = 10
			}
			for attempt := 0; attempt < attempts; attempt++ {
				runRetainedScriptReplacement(t, test.target, test.shebang, test.argv, test.interpreterCandidates)
			}
		})
	}
}

func TestRetainedScriptTargetsRejectUnsupportedForms(t *testing.T) {
	for _, test := range []struct {
		name, target, script string
	}{
		{"malformed", "npm", "not a script\n"},
		{"interpreter-arguments", "npm", "#!/usr/bin/env node --inspect\n"},
		{"nested-interpreter", "npm", "#!#!/usr/bin/env node\n"},
		{"unknown-env", "npm", "#!/usr/bin/env deno\n"},
		{"unknown-interpreter", "pytest", "#!/bin/python\n"},
		{"unsupported-runtime", "npm", "#!/usr/bin/env python3\n"},
		{"oversized", "npm", "#!" + strings.Repeat("x", retainedShebangLimit) + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, "script")
			if err := os.WriteFile(script, []byte(test.script), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), retainedExecTestHookKey{}, retainedExecTestHook{
				program: func(string) (retainedProgram, error) {
					return retainedProgramForWithKnownPath(test.target, retainedTestKnownPath(map[string]string{"/usr/bin/npm": script, "/usr/bin/pytest": script}))
				},
			})
			_, err := runRetainedCommand(ctx, root, Command{Argv: []string{"npm", "test"}, CWD: root}, time.Second)
			if !errors.Is(err, ErrCommandTargetUnsupported) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, name := range []string{"pnpm", "yarn", "bun"} {
		t.Run("contract-denied-"+name, func(t *testing.T) {
			_, err := runRetainedCommand(context.Background(), t.TempDir(), Command{Argv: []string{name, "test"}, CWD: t.TempDir()}, time.Second)
			if !errors.Is(err, ErrCommandTargetUnsupported) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func runRetainedScriptReplacement(t *testing.T, target, shebang string, argv, interpreterCandidates []string) {
	t.Helper()
	root := t.TempDir()
	script := filepath.Join(root, target)
	interpreter := staticRetainedScriptInterpreter(t, root, "interpreter", target)
	marker := target + "-script"
	if err := os.WriteFile(script, []byte(shebang+marker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "cwd")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "cwd-marker"), []byte("original-cwd"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper := buildRetainedProductionBinary(t, root, "helper")
	home := filepath.Join(root, "private-home")
	paths := map[string]string{"/usr/bin/" + target: script}
	for _, candidate := range interpreterCandidates {
		paths[candidate] = interpreter
	}
	var output retainedFixtureOutput
	hook := retainedExecTestHook{
		executable: func() (string, error) { return helper, nil },
		program: func(name string) (retainedProgram, error) {
			return retainedProgramForWithKnownPath(name, retainedTestKnownPath(paths))
		},
		tempHome: func() (string, error) { return home, os.Mkdir(home, 0o700) },
		afterRetention: func() {
			for _, path := range []string{helper, script, interpreter} {
				if err := os.Rename(path, path+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("replacement"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Rename(cwd, cwd+"-retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(cwd, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		observeOutput: func(stdout, stderr []byte) { output.stdout, output.stderr = stdout, stderr },
	}
	ctx := context.WithValue(context.Background(), retainedExecTestHookKey{}, hook)
	result, err := runRetainedCommand(ctx, root, Command{Argv: argv, CWD: cwd}, time.Second)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("argv=%q result=%#v stdout=%q stderr=%q err=%v", argv, result, output.stdout, output.stderr, err)
	}
	runtime := "python"
	if target == "npm" {
		runtime = "node"
	}
	want := runtime + ":" + marker + ":original-cwd:" + strings.Join(argv[1:], ",") + "\n"
	if result.OutputSHA256 != DigestSHA256([]byte(want)) {
		t.Fatalf("digest=%s want=%s stdout=%q stderr=%q", result.OutputSHA256, DigestSHA256([]byte(want)), output.stdout, output.stderr)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("private HOME remains: %v", err)
	}
}

func retainedTestKnownPath(paths map[string]string) func(...string) string {
	return func(candidates ...string) string {
		for _, candidate := range candidates {
			if path := paths[candidate]; path != "" {
				return path
			}
		}
		return ""
	}
}

func staticRetainedScriptInterpreter(t *testing.T, directory, name, target string) string {
	t.Helper()
	source := filepath.Join(directory, name+".go")
	runtime := "python"
	if target == "npm" {
		runtime = "node"
	}
	program := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 || os.Args[0] != "/proc/self/fd/7" || os.Args[1] != "/proc/self/fd/6" || os.Getenv("PATH") != "/usr/bin:/bin" || os.Getenv("HOME") == "" || os.Getenv("GIT_CONFIG_GLOBAL") != "/dev/null" {
		os.Exit(31)
	}
	script, err := os.ReadFile(os.Args[1])
	if err != nil || !strings.Contains(string(script), %q) {
		os.Exit(32)
	}
	cwd, err := os.ReadFile("cwd-marker")
	if err != nil || string(cwd) != "original-cwd" {
		os.Exit(33)
	}
	fmt.Printf(%q+":"+%q+":"+string(cwd)+":"+strings.Join(os.Args[2:], ",")+"\n")
}
`, target+"-script", runtime, target+"-script")
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	interpreter := filepath.Join(directory, name)
	build := exec.Command("go", "build", "-o", interpreter, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build script interpreter: %v: %s", err, output)
	}
	return interpreter
}

func TestRetainedOutputStopsAtBound(t *testing.T) {
	output := &retainedOutput{}
	if _, err := output.Write(make([]byte, retainedOutputLimit+1)); err != nil {
		t.Fatal(err)
	}
	if !output.overflow || len(output.bytes()) != retainedOutputLimit {
		t.Fatalf("overflow=%v size=%d", output.overflow, len(output.bytes()))
	}
}

func TestRetainedFaultsCleanPrivateHome(t *testing.T) {
	for _, test := range []struct {
		name string
		hook func(*retainedExecTestHook)
		want error
	}{
		{"procfd", func(h *retainedExecTestHook) { h.procFS = func() bool { return false } }, ErrCommandTargetUnsupported},
		{"extra-files", func(h *retainedExecTestHook) {
			h.extraFiles = func(int, retainedProgram, int) ([]*os.File, error) { return nil, errors.New("fault") }
		}, ErrOperationUnavailable},
		{"start", func(h *retainedExecTestHook) { h.start = func(*exec.Cmd) error { return errors.New("fault") } }, ErrOperationUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			hook := retainedExecTestHook{tempHome: func() (string, error) { return home, os.Mkdir(home, 0o700) }}
			test.hook(&hook)
			ctx := context.WithValue(context.Background(), retainedExecTestHookKey{}, hook)
			_, err := runRetainedCommand(ctx, root, Command{Argv: []string{"go", "version"}, CWD: root}, time.Second)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v", err)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("HOME remains: %v", err)
			}
		})
	}
}

func TestRetainedCancellationBeforeAndDuringRetention(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runRetainedCommand(ctx, root, Command{Argv: []string{"go", "version"}, CWD: root}, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("before start: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	called := false
	ctx = context.WithValue(ctx, retainedExecTestHookKey{}, retainedExecTestHook{
		afterRetention: func() { cancel() },
		tempHome:       func() (string, error) { called = true; return "", errors.New("must not run") },
	})
	_, err = runRetainedCommand(ctx, root, Command{Argv: []string{"go", "version"}, CWD: root}, time.Second)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("during retention error=%v home=%v", err, called)
	}
}

func TestRetainedOutputFailureIsSanitized(t *testing.T) {
	root := t.TempDir()
	target := staticRetainedFaultFixture(t, root, "target")
	home := filepath.Join(root, "home")
	var observed retainedFixtureOutput
	hook := retainedExecTestHook{
		executable: func() (string, error) { return buildRetainedProductionBinary(t, root, "helper"), nil },
		program: func(string) (retainedProgram, error) {
			fd, err := unix.Open(target, unix.O_RDONLY|unix.O_CLOEXEC, 0)
			return retainedProgram{target: fd, script: -1, interpreter: -1, mode: "elf"}, err
		},
		tempHome:      func() (string, error) { return home, os.Mkdir(home, 0o700) },
		observeOutput: func(stdout, stderr []byte) { observed.stdout, observed.stderr = stdout, stderr },
	}
	ctx := context.WithValue(context.Background(), retainedExecTestHookKey{}, hook)
	result, err := runRetainedCommand(ctx, root, Command{Argv: []string{"go", "version"}, CWD: root}, time.Second)
	if !errors.Is(err, ErrOperationLimit) || result != (ExecResult{}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if strings.Contains(err.Error(), "secret") || len(observed.stdout) != retainedOutputLimit || len(observed.stderr) != retainedOutputLimit {
		t.Fatalf("err=%v stdout=%d stderr=%d", err, len(observed.stdout), len(observed.stderr))
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("HOME remains: %v", err)
	}
}

func TestRetainedTimeoutAndCancellationKillProcessGroup(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			target := staticRetainedSleepFixture(t, root, "target")
			helper := buildRetainedProductionBinary(t, root, "helper")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hook := retainedExecTestHook{
				executable: func() (string, error) { return helper, nil },
				program: func(string) (retainedProgram, error) {
					fd, err := unix.Open(target, unix.O_RDONLY|unix.O_CLOEXEC, 0)
					return retainedProgram{target: fd, script: -1, interpreter: -1, mode: "elf"}, err
				},
			}
			ctx = context.WithValue(ctx, retainedExecTestHookKey{}, hook)
			done := make(chan error, 1)
			go func() {
				_, err := runRetainedCommand(ctx, root, Command{Argv: []string{"go", "version"}, CWD: root}, 100*time.Millisecond)
				done <- err
			}()
			pidPath := filepath.Join(root, "child-pid")
			var pid int
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if data, err := os.ReadFile(pidPath); err == nil {
					_, _ = fmt.Sscanf(string(data), "%d", &pid)
					break
				}
				time.Sleep(time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("descendant did not start")
			}
			if mode == "cancel" {
				cancel()
			}
			err := <-done
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
			deadline = time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatalf("descendant remains: %v", unix.Kill(pid, 0))
		})
	}
}

func staticRetainedSleepFixture(t *testing.T, directory, name string) string {
	t.Helper()
	source := filepath.Join(directory, "sleep.go")
	code := `package main
import ("os"; "os/exec"; "os/signal"; "strconv"; "syscall"; "time")
func main() { if os.Getenv("RETAINED_CHILD") == "1" { for { time.Sleep(time.Second) } }; child := exec.Command("/proc/self/exe"); child.Env = append(os.Environ(), "RETAINED_CHILD=1"); if child.Start() != nil { os.Exit(2) }; _ = os.WriteFile("child-pid", []byte(strconv.Itoa(child.Process.Pid)), 0600); signal.Ignore(syscall.SIGTERM); for { time.Sleep(time.Second) } }`
	if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, name)
	build := exec.Command("go", "build", "-o", target, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sleep fixture: %v: %s", err, output)
	}
	return target
}

func staticRetainedFaultFixture(t *testing.T, directory, name string) string {
	t.Helper()
	source := filepath.Join(directory, "fault.go")
	code := `package main
import ("fmt"; "os"; "strings")
func main() { fmt.Fprint(os.Stdout, strings.Repeat("secret-out", 1<<18)); fmt.Fprint(os.Stderr, strings.Repeat("secret-err", 1<<18)) }`
	if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, name)
	build := exec.Command("go", "build", "-o", target, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fault fixture: %v: %s", err, output)
	}
	return target
}

func TestRetainedHelperRejectsInvalidDispatch(t *testing.T) {
	if !retainedProcFSAvailable() {
		t.Skip("procfd unavailable")
	}
	if _, err := retainedProgramFor("pnpm"); !errors.Is(err, ErrCommandTargetUnsupported) && err == nil {
		t.Fatal("pnpm target accepted")
	}
}

func TestRetainedELFDescriptorsSurvivePathReplacement(t *testing.T) {
	for attempt := 0; attempt < 10; attempt++ {
		t.Run("attempt", func(t *testing.T) {
			runRetainedReplacement(t, staticRetainedFixture(t, t.TempDir(), "target"), "fixture\n")
		})
	}
}

func TestRetainedDynamicELFDescriptorsSurvivePathReplacement(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a host dynamic ELF")
	}
	output := runRetainedReplacement(t, copyRetainedELF(t, t.TempDir(), "target"), "")
	if output.exitCode != 0 {
		t.Logf("copied dynamic ELF argv=%q exit=%d stdout=%q stderr=%q", []string{"go", "version"}, output.exitCode, output.stdout, output.stderr)
		t.Skip("host echo is a multicall utility and requires its own argv[0]")
	}
	if got, want := output.stdout, "go version\n"; string(got) != want || len(output.stderr) != 0 {
		t.Fatalf("copied dynamic ELF stdout=%q stderr=%q", got, output.stderr)
	}
}

type retainedFixtureOutput struct {
	exitCode       int
	stdout, stderr []byte
}

func runRetainedReplacement(t *testing.T, target, wantOutput string) retainedFixtureOutput {
	t.Helper()
	root := filepath.Dir(target)
	originalCWD := filepath.Join(root, "cwd")
	if err := os.Mkdir(originalCWD, 0o700); err != nil {
		t.Fatal(err)
	}
	helper := buildRetainedProductionBinary(t, root, "helper")
	home := filepath.Join(root, "private-home")
	var output retainedFixtureOutput
	hook := retainedExecTestHook{
		executable: func() (string, error) { return helper, nil },
		program: func(string) (retainedProgram, error) {
			fd, err := unix.Open(target, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			return retainedProgram{target: fd, script: -1, interpreter: -1, mode: "elf"}, err
		},
		tempHome: func() (string, error) { return home, os.Mkdir(home, 0o700) },
		afterRetention: func() {
			// Replacing the retained names here is synchronized with all opens.
			for _, path := range []string{helper, target} {
				if err := os.Rename(path, path+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("not an executable"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Rename(originalCWD, originalCWD+"-retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(originalCWD, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		observeOutput: func(stdout, stderr []byte) {
			output.stdout = stdout
			output.stderr = stderr
		},
	}
	ctx := context.WithValue(context.Background(), retainedExecTestHookKey{}, hook)
	result, err := runRetainedCommand(ctx, root, Command{Argv: []string{"go", "version"}, CWD: originalCWD}, time.Second)
	output.exitCode = result.ExitCode
	if err != nil {
		t.Fatalf("retained fixture argv=%q exit=%d digest=%s err=%v", []string{"go", "version"}, result.ExitCode, result.OutputSHA256, err)
	}
	if wantOutput != "" && result.ExitCode != 0 {
		t.Fatalf("retained fixture argv=%q exit=%d stdout=%q stderr=%q", []string{"go", "version"}, result.ExitCode, output.stdout, output.stderr)
	}
	want := DigestSHA256([]byte(wantOutput))
	if wantOutput != "" && result.OutputSHA256 != want {
		t.Fatalf("digest=%s want=%s", result.OutputSHA256, want)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("private HOME remains: %v", err)
	}
	return output
}

func staticRetainedFixture(t *testing.T, directory, name string) string {
	t.Helper()
	source := filepath.Join(directory, "fixture.go")
	if err := os.WriteFile(source, []byte(`package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[0] == "go" && os.Args[1] == "version" {
		fmt.Print("fixture\n")
		return
	}
	fmt.Fprintf(os.Stderr, "fixture argv=%q\n", os.Args)
	os.Exit(23)
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, name)
	build := exec.Command("go", "build", "-o", target, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build static fixture: %v: %s", err, output)
	}
	return target
}

func copyRetainedELF(t *testing.T, directory, name string) string {
	t.Helper()
	var source string
	for _, candidate := range []string{"/usr/bin/echo", "/bin/echo"} {
		if retainedKnownPath(candidate) != "" {
			source = candidate
			break
		}
	}
	if source == "" {
		t.Skip("no system ELF fixture")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, name)
	if err := os.WriteFile(target, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return target
}

func buildRetainedProductionBinary(t *testing.T, directory, name string) string {
	t.Helper()
	target := filepath.Join(directory, name)
	build := exec.Command("go", "build", "-o", target, "../../cmd/gentle-ai")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v: %s", err, output)
	}
	return target
}
