package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if handled, stdout, stderr, exitCode := issue3026EngramFixtureDispatch(filepath.Base(os.Args[0]), os.Getenv(issue3026EngramFixtureMarkerEnv), os.Args[1:]); handled {
		_, _ = os.Stdout.WriteString(stdout)
		_, _ = os.Stderr.WriteString(stderr)
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

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

func TestIssue3026EngramRuntimeFixtureIsHermetic(t *testing.T) {
	root := t.TempDir()
	sandbox, err := newSandbox(os.Args[0], root)
	if err != nil {
		t.Fatal(err)
	}
	if err := issue3026InstalledEngramRuntime(sandbox); err != nil {
		t.Fatalf("install Engram runtime fixture: %v", err)
	}
	if err := issue3026InstalledOpenCodeRuntime(sandbox); err != nil {
		t.Fatalf("install OpenCode runtime fixture: %v", err)
	}
	fixtureDir := filepath.Join(sandbox.Root, "runtime", "bin")
	for _, name := range []string{"engram", "opencode"} {
		path := sandboxExecutablePath(fixtureDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat local %s fixture %q: %v", name, path, err)
		}
		if got := pathOnSandboxPath(sandbox.PathOverride, name); got != path {
			t.Fatalf("sandbox PATH resolves %s to %q, want %q", name, got, path)
		}
	}
	if got := sandbox.PathOverride; got != fixtureDir+string(os.PathListSeparator)+filepath.Dir(mustLookPath(t, "git")) {
		t.Fatalf("isolated PATH = %q", got)
	}
	if strings.Contains(strings.ToLower(sandbox.PathOverride), "homebrew") {
		t.Fatalf("isolated PATH retained Homebrew: %q", sandbox.PathOverride)
	}
	for _, name := range []string{"engram", "opencode"} {
		if global := pathOnSandboxPath(os.Getenv("PATH"), name); global != "" && strings.Contains(sandbox.PathOverride, filepath.Dir(global)) {
			t.Fatalf("isolated PATH retained global %s directory %q", name, filepath.Dir(global))
		}
	}
	proxyOff := false
	for _, entry := range sandbox.env() {
		if strings.HasPrefix(entry, "PATH=") {
			if strings.Contains(entry, os.Getenv("HOME")) {
				t.Fatalf("isolated PATH leaked an operator tool directory: %q", entry)
			}
		}
		if entry == "GOPROXY=off" {
			proxyOff = true
		}
	}
	if !sandbox.GoProxyOff || !proxyOff {
		t.Fatal("Engram fixture did not disable Go proxy fallback")
	}
	if !sandbox.Issue3026EngramFixture || !hasSandboxEnv(sandbox, issue3026EngramFixtureMarkerEnv+"="+issue3026EngramFixtureMarkerValue) {
		t.Fatal("Engram fixture marker is not isolated to j100")
	}
}

func TestIssue3026EngramFixtureResponse(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "version", args: []string{"version"}, code: 0, want: "engram 1.18.0\n"},
		{name: "setup help", args: []string{"setup", "--help"}, code: 0, want: "--protocol"},
		{name: "setup invocation rejected", args: []string{"setup", "opencode"}, code: 64, want: "unsupported arguments"},
		{name: "unknown invocation rejected", args: []string{"install"}, code: 64, want: "unsupported arguments"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := issue3026EngramFixtureResponse(tt.args)
			if code != tt.code || !strings.Contains(stdout+stderr, tt.want) {
				t.Fatalf("response = stdout %q stderr %q code %d", stdout, stderr, code)
			}
			if strings.Contains(stdout+stderr, "API_KEY") {
				t.Fatalf("fixture diagnostic leaked sensitive-looking output: %q", stdout+stderr)
			}
		})
	}
}

func TestIssue3026EngramFixtureDispatchRequiresMarker(t *testing.T) {
	for _, tt := range []struct {
		name    string
		program string
		marker  string
		handled bool
	}{
		{name: "marker and engram name", program: executableNameForGOOS("engram", runtime.GOOS), marker: issue3026EngramFixtureMarkerValue, handled: true},
		{name: "missing marker", program: executableNameForGOOS("engram", runtime.GOOS)},
		{name: "renamed binary", program: executableNameForGOOS("bench-copy", runtime.GOOS), marker: issue3026EngramFixtureMarkerValue},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handled, _, _, _ := issue3026EngramFixtureDispatch(tt.program, tt.marker, []string{"version"})
			if handled != tt.handled {
				t.Fatalf("fixture dispatch handled = %v, want %v", handled, tt.handled)
			}
		})
	}
}

func TestIssue3026ClosedRuntimeExecutesOnlyLocalFixtures(t *testing.T) {
	sandbox := issue3026RuntimeSandbox(t)
	engram := pathOnSandboxPath(sandbox.PathOverride, "engram")
	if engram == "" || pathOnSandboxPath(sandbox.PathOverride, "opencode") == "" {
		t.Fatal("closed PATH did not resolve local Engram and OpenCode fixtures")
	}
	if stdout, stderr, exitCode := runSandboxCommand(sandbox, engram, "version"); exitCode != 0 || stdout != "engram 1.18.0\n" || stderr != "" {
		t.Fatalf("local engram version = stdout %q stderr %q exit %d", stdout, stderr, exitCode)
	}
	if _, _, exitCode := runSandboxCommand(sandbox, engram, "setup", "opencode"); exitCode != 64 {
		t.Fatalf("unsupported local engram setup exit = %d, want 64", exitCode)
	}
	git := pathOnSandboxPath(sandbox.PathOverride, "git")
	if stdout, stderr, exitCode := runSandboxCommand(sandbox, git, "--version"); git == "" || exitCode != 0 || stdout == "" || stderr != "" {
		t.Fatalf("closed PATH git = stdout %q stderr %q exit %d", stdout, stderr, exitCode)
	}
	if err := os.Remove(engram); err != nil {
		t.Fatalf("remove local Engram fixture: %v", err)
	}
	if got := pathOnSandboxPath(sandbox.PathOverride, "engram"); got != "" {
		t.Fatalf("missing fixture resolved to %q instead of failing locally", got)
	}
}

func issue3026RuntimeSandbox(t *testing.T) *Sandbox {
	t.Helper()
	root := t.TempDir()
	sandbox, err := newSandbox(os.Args[0], root)
	if err != nil {
		t.Fatal(err)
	}
	if err := issue3026InstalledEngramRuntime(sandbox); err != nil {
		t.Fatal(err)
	}
	if err := issue3026InstalledOpenCodeRuntime(sandbox); err != nil {
		t.Fatal(err)
	}
	return sandbox
}

func runSandboxCommand(sandbox *Sandbox, command string, args ...string) (string, string, int) {
	cmd := exec.Command(command, args...)
	cmd.Env = sandbox.env()
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	if err != nil {
		return stdout.String(), stderr.String(), -1
	}
	return stdout.String(), stderr.String(), 0
}

func hasSandboxEnv(sandbox *Sandbox, want string) bool {
	for _, entry := range sandbox.env() {
		if entry == want {
			return true
		}
	}
	return false
}

func pathOnSandboxPath(path, name string) string {
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		candidate := sandboxExecutablePath(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func mustLookPath(t *testing.T, command string) string {
	t.Helper()
	path, err := exec.LookPath(command)
	if err != nil {
		t.Fatalf("look up %s: %v", command, err)
	}
	return path
}

func TestExecutableNameForGOOS(t *testing.T) {
	for _, tt := range []struct {
		name string
		goos string
		want string
	}{
		{name: "opencode", goos: "linux", want: "opencode"},
		{name: "engram", goos: "linux", want: "engram"},
		{name: "opencode", goos: "darwin", want: "opencode"},
		{name: "engram", goos: "darwin", want: "engram"},
		{name: "opencode", goos: "windows", want: "opencode.exe"},
		{name: "engram", goos: "windows", want: "engram.exe"},
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
