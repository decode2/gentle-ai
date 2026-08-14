//go:build linux

package directrun

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
		"", "#!/usr/bin/env node --inspect\nconsole.log(1)\n", "#!/usr/bin/env unknown\n", "#!/bin/sh\necho unsafe\n",
		"#!/usr/bin/env node\r\nconsole.log(1)\n",
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
			if _, err := retainedScriptInterpreter("npm", fd); err == nil {
				t.Fatal("accepted unsafe script")
			}
		})
	}
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
