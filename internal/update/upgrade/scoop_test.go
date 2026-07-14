package upgrade

import (
	"context"
	"errors"
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

func TestScoopUpgradeRunsExactCommand(t *testing.T) {
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	var gotName string
	var gotArgs []string
	var gotCmd *exec.Cmd
	execCommand = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, append([]string(nil), args...)
		gotCmd = mockCmd("true")
		return gotCmd
	}
	if err := scoopUpgrade(context.Background()); err != nil {
		t.Fatalf("scoopUpgrade() error = %v", err)
	}
	if gotName != "scoop" || !reflect.DeepEqual(gotArgs, []string{"update", "gentle-ai"}) {
		t.Fatalf("command = %q %v, want scoop update gentle-ai", gotName, gotArgs)
	}
	if gotCmd.Stdin != nil {
		t.Fatal("scoop command stdin must be nil")
	}
}

func TestScoopUpgradeHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scoopUpgrade(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("scoopUpgrade() error = %v, want context.Canceled", err)
	}
}

func TestScoopUpgradeReportsCommandFailure(t *testing.T) {
	original := execCommand
	execCommand = func(string, ...string) *exec.Cmd { return mockCmd("false") }
	t.Cleanup(func() { execCommand = original })
	if err := scoopUpgrade(context.Background()); err == nil {
		t.Fatal("expected Scoop command failure")
	}
}

func TestScoopUpgradeReportsRunningProcessSkip(t *testing.T) {
	original := execCommand
	execCommand = func(string, ...string) *exec.Cmd {
		return mockCmd("echo", "Running process detected, skip updating.")
	}
	t.Cleanup(func() { execCommand = original })

	err := scoopUpgrade(context.Background())
	if err == nil || !strings.Contains(err.Error(), "running process") {
		t.Fatalf("scoopUpgrade() error = %v, want running process failure", err)
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
