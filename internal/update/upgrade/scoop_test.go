package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/system"
	"github.com/gentleman-programming/gentle-ai/internal/update"
)

func TestWindowsPathWithin(t *testing.T) {
	tests := []struct {
		name, candidate, root string
		want                  bool
	}{
		{"drive child", `C:\Users\me\scoop\apps\gentle-ai\2.1.4\gentle-ai.exe`, `c:/users/me/scoop/apps/gentle-ai/2.1.4`, true},
		{"same path", `C:\Scoop\Apps\gentle-ai`, `c:\scoop\apps\gentle-ai\`, true},
		{"extended drive", `\\?\C:\scoop\apps\gentle-ai\current\gentle-ai.exe`, `c:\scoop\apps\gentle-ai\current`, true},
		{"UNC child", `\\server\share\scoop\apps\gentle-ai\current\gentle-ai.exe`, `\\SERVER\SHARE\scoop\apps\gentle-ai\current`, true},
		{"extended UNC", `\\?\UNC\server\share\scoop\gentle-ai.exe`, `\\server\share\scoop`, true},
		{"sibling prefix", `C:\scoop\apps\gentle-ai-other\gentle-ai.exe`, `C:\scoop\apps\gentle-ai`, false},
		{"cross drive", `D:\scoop\gentle-ai.exe`, `C:\scoop`, false},
		{"relative", `scoop\gentle-ai.exe`, `C:\scoop`, false},
		{"device path", `\\.\C:\scoop\gentle-ai.exe`, `C:\scoop`, false},
		{"escape root", `C:\..\scoop\gentle-ai.exe`, `C:\scoop`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowsPathWithin(tc.candidate, tc.root); got != tc.want {
				t.Fatalf("windowsPathWithin(%q, %q) = %v, want %v", tc.candidate, tc.root, got, tc.want)
			}
		})
	}
}

func TestScoopRoots(t *testing.T) {
	env := map[string]string{
		"SCOOP":       `D:\custom-scoop`,
		"USERPROFILE": `C:\Users\me`,
	}
	getenv := func(key string) string { return env[key] }
	got := scoopRoots(getenv, func() (string, error) { return `C:\fallback`, nil })
	want := []string{`D:\custom-scoop`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scoopRoots() = %#v, want %#v", got, want)
	}

	delete(env, "SCOOP")
	got = scoopRoots(getenv, func() (string, error) { return `C:\fallback`, nil })
	want = []string{`C:\Users\me\scoop`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default scoopRoots() = %#v, want %#v", got, want)
	}
}

func TestScoopOwnsExecutable(t *testing.T) {
	manifest := []byte(`{"version":"2.1.4","homepage":"https://github.com/Gentleman-Programming/gentle-ai"}`)
	eval := func(path string) (string, error) {
		switch path {
		case `D:\scoop\apps\gentle-ai`:
			return `D:\scoop\apps\gentle-ai`, nil
		case `D:\scoop\apps\gentle-ai\current`:
			return `D:\scoop\apps\gentle-ai\2.1.4`, nil
		default:
			return path, nil
		}
	}
	read := func(path string) ([]byte, error) { return manifest, nil }

	if !scoopOwnsExecutable(`D:\scoop\apps\gentle-ai\2.1.4\gentle-ai.exe`, []string{`D:\scoop`}, read, eval) {
		t.Fatal("expected current Scoop executable to be owned")
	}
	if scoopOwnsExecutable(`C:\Users\me\AppData\Local\gentle-ai\bin\gentle-ai.exe`, []string{`D:\scoop`}, read, eval) {
		t.Fatal("AppData executable must not be Scoop-owned")
	}
	if scoopOwnsExecutable(`D:\scoop\shims\gentle-ai.exe`, []string{`D:\scoop`}, read, eval) {
		t.Fatal("generated shim is not the running package executable")
	}
	for _, invalid := range [][]byte{
		[]byte(`not-json`),
		[]byte(`{"version":"","homepage":"https://github.com/Gentleman-Programming/gentle-ai"}`),
		[]byte(`{"version":"2.1.4","homepage":"https://example.com"}`),
	} {
		readInvalid := func(string) ([]byte, error) { return invalid, nil }
		if scoopOwnsExecutable(`D:\scoop\apps\gentle-ai\2.1.4\gentle-ai.exe`, []string{`D:\scoop`}, readInvalid, eval) {
			t.Fatal("invalid manifest must not prove ownership")
		}
	}
	if scoopOwnsExecutable(`D:\scoop\apps\gentle-ai\2.1.3\gentle-ai.exe`, []string{`D:\scoop`}, read, eval) {
		t.Fatal("stale version directory must not prove current ownership")
	}
	escapedCurrent := func(path string) (string, error) {
		if strings.HasSuffix(path, `\current`) {
			return `D:\other\2.1.4`, nil
		}
		return path, nil
	}
	if scoopOwnsExecutable(`D:\other\2.1.4\gentle-ai.exe`, []string{`D:\scoop`}, read, escapedCurrent) {
		t.Fatal("escaped current junction must not prove ownership")
	}
	failedEval := func(string) (string, error) { return "", errors.New("no junction access") }
	if scoopOwnsExecutable(`D:\scoop\apps\gentle-ai\2.1.4\gentle-ai.exe`, []string{`D:\scoop`}, read, failedEval) {
		t.Fatal("resolution failure must be inconclusive")
	}
}

func TestEffectiveMethodWindowsGentleAIScoopOwnership(t *testing.T) {
	original := scoopOwnershipDetector
	t.Cleanup(func() { scoopOwnershipDetector = original })
	profile := system.PlatformProfile{OS: "windows"}
	tool := update.ToolInfo{Name: "gentle-ai", InstallMethod: update.InstallBinary}

	scoopOwnershipDetector = func() bool { return true }
	if got := effectiveMethod(tool, profile); got != update.InstallScoop {
		t.Fatalf("owned method = %q, want scoop", got)
	}
	scoopOwnershipDetector = func() bool { return false }
	if got := effectiveMethod(tool, profile); got != update.InstallInstaller {
		t.Fatalf("fallback method = %q, want installer", got)
	}

	scoopOwnershipDetector = func() bool {
		t.Fatal("detector called for non-target tool")
		return false
	}
	if got := effectiveMethod(update.ToolInfo{Name: "engram", InstallMethod: update.InstallBinary}, profile); got != update.InstallBinary {
		t.Fatalf("non-target method = %q, want binary", got)
	}
}

func TestScoopUpgradeTemporarilyIgnoresRunningProcess(t *testing.T) {
	tests := []struct {
		name, prior, fail, wantErr string
		wantRestore                []string
	}{
		{"unset", "'IGNORE_RUNNING_PROCESSES' is not set", "", "", []string{"config", "rm", "IGNORE_RUNNING_PROCESSES"}},
		{"false", "False", "", "", []string{"config", "IGNORE_RUNNING_PROCESSES", "false"}},
		{"true", "True", "", "", []string{"config", "IGNORE_RUNNING_PROCESSES", "true"}},
		{"update failure", "False", "update", "scoop update gentle-ai", []string{"config", "IGNORE_RUNNING_PROCESSES", "false"}},
		{"restore failure", "False", "restore", "restore Scoop setting", []string{"config", "IGNORE_RUNNING_PROCESSES", "false"}},
		{"false success", "False", "skip", "running process", []string{"config", "IGNORE_RUNNING_PROCESSES", "false"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := scoopExecCommand
			t.Cleanup(func() { scoopExecCommand = original })
			var calls [][]string
			scoopExecCommand = func(_ context.Context, name string, args ...string) *exec.Cmd {
				calls = append(calls, append([]string{name}, args...))
				switch len(calls) {
				case 1:
					return mockCmd("echo", tt.prior)
				case 3:
					if tt.fail == "update" {
						return mockCmd("false")
					}
					if tt.fail == "skip" {
						return mockCmd("echo", "Running process detected, skip updating.")
					}
				case 4:
					if tt.fail == "restore" {
						return mockCmd("false")
					}
				}
				return mockCmd("true")
			}
			err := scoopUpgrade(context.Background())
			if (err == nil) != (tt.wantErr == "") || err != nil && !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("scoopUpgrade() error = %v, want containing %q", err, tt.wantErr)
			}
			want := [][]string{{"scoop", "config", "IGNORE_RUNNING_PROCESSES"}, {"scoop", "config", "IGNORE_RUNNING_PROCESSES", "true"}, {"scoop", "update", "gentle-ai"}, append([]string{"scoop"}, tt.wantRestore...)}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("commands = %#v, want %#v", calls, want)
			}
		})
	}
}
func TestScoopUpgradeHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scoopUpgrade(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("scoopUpgrade() error = %v, want context.Canceled", err)
	}
}

func TestScoopUpgradeCancelsSubprocessAndRestoresConfig(t *testing.T) {
	original := scoopExecCommand
	t.Cleanup(func() { scoopExecCommand = original })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updateStarted := make(chan struct{})
	var restoreContextLive, restoreHasDeadline bool
	scoopExecCommand = func(commandCtx context.Context, _ string, args ...string) *exec.Cmd {
		if reflect.DeepEqual(args, []string{"update", "gentle-ai"}) {
			close(updateStarted)
		}
		if reflect.DeepEqual(args, []string{"config", "IGNORE_RUNNING_PROCESSES", "false"}) {
			restoreContextLive = commandCtx.Err() == nil
			_, restoreHasDeadline = commandCtx.Deadline()
		}
		cmd := exec.CommandContext(commandCtx, os.Args[0], "-test.run=TestScoopCommandHelper", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_SCOOP_HELPER=1", "SCOOP_HELPER_ARGS="+strings.Join(args, "|"))
		return cmd
	}

	done := make(chan error, 1)
	go func() { done <- scoopUpgrade(ctx) }()
	<-updateStarted
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scoopUpgrade() error = %v, want context.Canceled", err)
	}
	if !restoreContextLive {
		t.Fatal("restore command must receive a live cleanup context")
	}
	if !restoreHasDeadline {
		t.Fatal("restore context must have a cleanup deadline")
	}
}

func TestScoopCommandHelper(t *testing.T) {
	if os.Getenv("GO_WANT_SCOOP_HELPER") != "1" {
		return
	}
	switch os.Getenv("SCOOP_HELPER_ARGS") {
	case "config|IGNORE_RUNNING_PROCESSES":
		fmt.Print("False")
	case "update|gentle-ai":
		select {}
	}
	os.Exit(0)
}

func TestScoopUpgradeReportsCommandFailure(t *testing.T) {
	original := scoopExecCommand
	scoopExecCommand = func(context.Context, string, ...string) *exec.Cmd { return mockCmd("false") }
	t.Cleanup(func() { scoopExecCommand = original })
	if err := scoopUpgrade(context.Background()); err == nil {
		t.Fatal("expected Scoop command failure")
	}
}

func TestRenderUpgradeReportShowsScoopDryRunCommand(t *testing.T) {
	report := UpgradeReport{DryRun: true, Results: []ToolUpgradeResult{{
		ToolName: "gentle-ai", OldVersion: "2.1.3", NewVersion: "2.1.4",
		Method: update.InstallScoop, Status: UpgradeSkipped,
	}}}
	if got := RenderUpgradeReport(report); !strings.Contains(got, "would run: scoop update gentle-ai") {
		t.Fatalf("dry-run report missing Scoop command:\n%s", got)
	}
}
