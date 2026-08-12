package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinary writes an executable that answers a fixed argv with a fixed
// message, so the capability probe can be tested without a real gentle-ai.
func fakeBinary(t *testing.T, script string) *Sandbox {
	t.Helper()
	root := t.TempDir()
	sandbox, err := newSandbox(filepath.Join(root, "fake"), root)
	if err != nil {
		t.Fatalf("newSandbox: %v", err)
	}
	if err := os.MkdirAll(sandbox.Repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.WriteFile(sandbox.Binary, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return sandbox
}

// A build that HAS the flag fails on state, not on shape. The probe must read
// that as supported: "sdd-attempt requires --cwd" is the repository's answer,
// not the CLI's.
func TestProbeCapabilityAcceptsAStateFailure(t *testing.T) {
	sandbox := fakeBinary(t, `echo "Error: sdd-attempt requires --cwd" >&2; exit 1`)
	capability := &Capability{
		Verb:  []string{"sdd-attempt", "finish"},
		Probe: []string{"sdd-attempt", "finish", "--expected-binding-revision=probe"},
	}
	supported, reason := newCapabilityProbe(sandbox).supported(capability)
	if !supported {
		t.Fatalf("supported = false (%s), want true: a state failure is not a missing surface", reason)
	}
}

// A build that LACKS the flag rejects the shape, and the journey must record
// `unsupported` rather than a state failure it never had.
func TestProbeCapabilityRejectsAMissingFlag(t *testing.T) {
	sandbox := fakeBinary(t, `echo "Error: flag provided but not defined: -expected-binding-revision" >&2; exit 1`)
	capability := &Capability{
		Verb:  []string{"sdd-attempt", "finish"},
		Probe: []string{"sdd-attempt", "finish", "--expected-binding-revision=probe"},
	}
	supported, reason := newCapabilityProbe(sandbox).supported(capability)
	if supported {
		t.Fatal("supported = true, want false: the binary rejected the shape of the command")
	}
	if reason == "" {
		t.Fatal("an unsupported surface must say which argv it probed")
	}
}

func TestHelpProbeRejectsALegacySurface(t *testing.T) {
	sandbox := fakeBinary(t, `echo "Error: flag provided but not defined: -help" >&2; exit 1`)
	supported, _ := newCapabilityProbe(sandbox).supported(&Capability{Verb: []string{"legacy", "finish"}})
	if supported {
		t.Fatal("the help read should have reported unsupported for this fake")
	}
}

// Probe and the default help read must not share a cache slot, or one verb's
// answer would silently decide the other's.
func TestProbeAndHelpProbeDoNotShareACacheEntry(t *testing.T) {
	sandbox := fakeBinary(t, `
case "$*" in
  *--help*) echo "Error: flag provided but not defined: -help" >&2; exit 1 ;;
  *)        echo "Error: sdd-attempt requires --cwd" >&2; exit 1 ;;
esac`)
	probe := newCapabilityProbe(sandbox)
	if supported, _ := probe.supported(&Capability{Verb: []string{"legacy", "finish"}}); supported {
		t.Fatal("the help read should have reported unsupported for this fake")
	}
	supported, reason := probe.supported(&Capability{
		Verb:  []string{"legacy", "finish"},
		Probe: []string{"legacy", "finish", "--expected-binding-revision=probe"},
	})
	if !supported {
		t.Fatalf("supported = false (%s), want true: the probe answer must not come from the help cache", reason)
	}
}

// readBack must never charge its git subprocesses to the next counted
// invocation, or an uncounted proof would inflate a measured dimension.
func TestReadBackBlanksGitTrace(t *testing.T) {
	sandbox := fakeBinary(t, `echo "GIT_TRACE=[$GIT_TRACE]"`)
	observation := sandbox.readBack("sdd-attempt", "status")
	if observation.Stdout != "GIT_TRACE=[]\n" {
		t.Fatalf("readBack stdout = %q, want a blanked GIT_TRACE", observation.Stdout)
	}
	counted := sandbox.invoke([]string{"sdd-attempt", "status"})
	if counted.Stdout == "GIT_TRACE=[]\n" {
		t.Fatal("a counted invocation lost GIT_TRACE, so git_subprocesses would stop being observable")
	}
}

func TestSandboxEnvIncludesBenchReceiptMutationPath(t *testing.T) {
	sandbox, err := newSandbox("gentle-ai", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sandbox.BenchReceiptMutationPath = filepath.Join(sandbox.Root, "receipt.json")
	for _, entry := range sandbox.env() {
		if entry == "GENTLE_AI_BENCH_MUTATE_RECEIPT="+sandbox.BenchReceiptMutationPath {
			return
		}
	}
	t.Fatal("sandbox environment has no benchmark receipt mutation path")
}

func TestSandboxEnvKeepsInheritedPathByDefault(t *testing.T) {
	sandbox, err := newSandbox("gentle-ai", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.PathPrepend != "" {
		t.Fatalf("default sandbox PathPrepend = %q, want empty", sandbox.PathPrepend)
	}
	if _, err := os.Stat(filepath.Join(sandbox.Root, "runtime")); !os.IsNotExist(err) {
		t.Fatalf("default sandbox runtime fixture path exists: %v", err)
	}
	for _, entry := range sandbox.env() {
		if strings.HasPrefix(entry, "PATH=") {
			if got := strings.TrimPrefix(entry, "PATH="); got != os.Getenv("PATH") {
				t.Fatalf("default sandbox PATH = %q, want inherited PATH %q", got, os.Getenv("PATH"))
			}
			return
		}
	}
	t.Fatal("sandbox environment has no PATH")
}

func TestSandboxEnvPrependsOnlySandboxLocalPath(t *testing.T) {
	sandbox, err := newSandbox("gentle-ai", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixtureDir := filepath.Join(sandbox.Root, "runtime", "bin")
	sandbox.PathPrepend = fixtureDir
	want := fixtureDir + string(os.PathListSeparator) + os.Getenv("PATH")
	for _, entry := range sandbox.env() {
		if strings.HasPrefix(entry, "PATH=") {
			if got := strings.TrimPrefix(entry, "PATH="); got != want {
				t.Fatalf("sandbox PATH = %q, want %q", got, want)
			}
			relative, err := filepath.Rel(sandbox.Root, fixtureDir)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Fatalf("PATH prepend %q is outside sandbox root %q", fixtureDir, sandbox.Root)
			}
			return
		}
	}
	t.Fatal("sandbox environment has no PATH")
}

func TestIssue3026OpenCodeRuntimeFixtureUsesSandboxExecutableNaming(t *testing.T) {
	root := t.TempDir()
	sandbox, err := newSandbox(filepath.Join(root, "test-binary"), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sandbox.Binary, []byte("available test binary"), 0o755); err != nil {
		t.Fatalf("write test binary: %v", err)
	}
	if err := issue3026InstalledOpenCodeRuntime(sandbox); err != nil {
		t.Fatalf("install runtime fixture: %v", err)
	}
	path := sandboxExecutablePath(sandbox.PathPrepend, "opencode")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat runtime fixture %q: %v", path, err)
	}
	if info.IsDir() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
		t.Fatalf("runtime fixture = %#v, want an executable file", info)
	}
	if got := sandbox.PathPrepend; got == "" || filepath.Dir(path) != got {
		t.Fatalf("runtime fixture path = %q, prepend = %q", path, got)
	}
}

func TestExecutableNameForGOOS(t *testing.T) {
	for _, tt := range []struct {
		name string
		goos string
		want string
	}{
		{name: "opencode", goos: "linux", want: "opencode"},
		{name: "opencode", goos: "darwin", want: "opencode"},
		{name: "opencode", goos: "windows", want: "opencode.exe"},
		{name: "opencode.exe", goos: "windows", want: "opencode.exe"},
	} {
		t.Run(tt.goos+"/"+tt.name, func(t *testing.T) {
			if got := executableNameForGOOS(tt.name, tt.goos); got != tt.want {
				t.Fatalf("executableNameForGOOS(%q, %q) = %q, want %q", tt.name, tt.goos, got, tt.want)
			}
		})
	}
}

func TestIssue3026InstallDiagnosticIncludesBoundedStderr(t *testing.T) {
	stderr := "WARNING: prerequisite check\nError: execute install pipeline: OpenCode is not installed\nAPI_KEY=do-not-leak\n" + strings.Repeat("x", issue3026InstallDiagnosticMaxBytes)
	got := issue3026BoundedStderr(stderr)
	if !strings.Contains(got, "WARNING: prerequisite check") || !strings.Contains(got, "Error: execute install pipeline") {
		t.Fatalf("bounded diagnostic = %q, want the useful stderr lines", got)
	}
	if strings.Contains(got, "do-not-leak") || !strings.Contains(got, "API_KEY=<redacted>") {
		t.Fatalf("bounded diagnostic = %q, want credential value redacted", got)
	}
	if !strings.Contains(got, "[stderr truncated]") || len(got) > issue3026InstallDiagnosticMaxBytes {
		t.Fatalf("bounded diagnostic length/content = %d/%q, want capped output with truncation marker", len(got), got)
	}
}

func TestSandboxEnvKeepsTempFilesInsideTheSandbox(t *testing.T) {
	sandbox, err := newSandbox("gentle-ai", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(sandbox.Root, "tmp")
	found := map[string]bool{}
	for _, entry := range sandbox.env() {
		if entry == "TMP="+want {
			found["TMP"] = true
			continue
		}
		if entry == "TEMP="+want {
			found["TEMP"] = true
			continue
		}
		if entry == "TMPDIR="+want {
			found["TMPDIR"] = true
			continue
		}
		if strings.HasPrefix(entry, "TMP=") || strings.HasPrefix(entry, "TEMP=") || strings.HasPrefix(entry, "TMPDIR=") {
			t.Fatalf("temporary directory = %q, want %q", entry, want)
		}
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("sandbox temp directory %q: %v", want, err)
	}
	if !found["TMP"] || !found["TEMP"] || !found["TMPDIR"] {
		t.Fatalf("sandbox temp variables = %v, want TMP, TEMP and TMPDIR", found)
	}
}

func TestSandboxEnvKeepsWindowsHomeInsideTheSandbox(t *testing.T) {
	sandbox, err := newSandbox("gentle-ai", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range sandbox.env() {
		if entry == "USERPROFILE="+sandbox.Home {
			return
		}
	}
	t.Fatalf("sandbox environment has no USERPROFILE=%q", sandbox.Home)
}

func TestSelectedAuthorityCaptureHelpersSelectTheSandboxLineage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*journeyRun) error
	}{
		{name: "results", run: sddCaptureSelectedAuthorityLenses},
		{name: "evidence", run: sddCaptureSelectedAuthorityEvidence},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := fakeBinary(t, `printf '%s\n' '{"next_transition":{"kind":"complete"}}'`)
			sandbox.Lineage = "review-sdd-newer"
			run := &journeyRun{sandbox: sandbox, accumulator: newAccumulator(), step: tt.name}

			if err := tt.run(run); err != nil {
				t.Fatalf("capture helper: %v", err)
			}
			if len(run.accumulator.records) != 1 {
				t.Fatalf("invocations = %d, want 1", len(run.accumulator.records))
			}
			args := run.accumulator.records[0].Args
			if len(args) < 2 || args[len(args)-2] != "--lineage" || args[len(args)-1] != sandbox.Lineage {
				t.Fatalf("status args = %q, want selected lineage %q", args, sandbox.Lineage)
			}
		})
	}
}
