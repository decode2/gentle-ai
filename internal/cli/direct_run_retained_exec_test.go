//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/directrun"
)

func TestDirectRunRetainedExecProductionBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the production binary")
	}
	binary := filepath.Join(t.TempDir(), "gentle-ai")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/gentle-ai")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production binary: %v: %s", err, output)
	}
	repo := directRunRepo(t)
	handoff := directRunHandoff(t, repo, "retained-exec")
	var record directrun.Record
	runRetainedDirectJSON(t, binary, repo, "issue", mustHandoffJSON(t, handoff), &record)
	runRetainedDirectJSON(t, binary, repo, "register", directRunRegisterInput(record), &record)
	request := directRunRequest(t, record, "child-retained", "direct_exec", `{"command_index":0,"timeout_ms":30000}`)
	var response directrun.Response
	runRetainedDirectJSON(t, binary, repo, "execute", mustRequestJSON(t, request), &response)
	if response.Status != "ok" {
		t.Fatalf("retained exec response = %#v error=%#v", response, response.Error)
	}
	var result directrun.ExecResult
	if err := json.Unmarshal(response.Result, &result); err != nil || result.ExitCode != 0 || len(result.OutputSHA256) != 64 {
		t.Fatalf("retained exec result = %s: %v", response.Result, err)
	}
}

func TestDirectRunStructuredGitProductionBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the production binary")
	}
	binary := filepath.Join(t.TempDir(), "gentle-ai")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/gentle-ai")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production binary: %v: %s", err, output)
	}
	repo := directRunRepo(t)
	directRunGit(t, repo, "config", "user.email", "test@example.invalid")
	directRunGit(t, repo, "config", "user.name", "Test")
	directRunGit(t, repo, "add", ".")
	directRunGit(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "note.txt"), []byte("private-source-must-not-cross\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handoff := directRunHandoff(t, repo, "retained-git")
	var record directrun.Record
	runRetainedDirectJSON(t, binary, repo, "issue", mustHandoffJSON(t, handoff), &record)
	runRetainedDirectJSON(t, binary, repo, "register", directRunRegisterInput(record), &record)
	for _, query := range []string{"git_status", "git_diff"} {
		request := directRunRequest(t, record, "child-retained-git", "direct_inspect", `{"query":"`+query+`"}`)
		var response directrun.Response
		runRetainedDirectJSON(t, binary, repo, "execute", mustRequestJSON(t, request), &response)
		if response.Status != "ok" || response.Operation != "direct_inspect" || response.RequestID != request.RequestID || response.Validate() != nil || strings.Contains(string(response.Result), "private-source-must-not-cross") || strings.Contains(string(response.Result), "patch") {
			t.Fatalf("%s response=%s", query, response.Result)
		}
		var result map[string]any
		if err := json.Unmarshal(response.Result, &result); err != nil || result["evidence_sha256"] == "" || result["entries"] == nil {
			t.Fatalf("%s result=%s err=%v", query, response.Result, err)
		}
	}
	valid := directRunRequest(t, record, "child-retained-git", "direct_inspect", `{"query":"git_diff"}`)
	invalid := append([]byte(nil), mustRequestJSON(t, valid)...)
	invalid = bytes.Replace(invalid, []byte(`{"query":"git_diff"}`), []byte(`{"query":"git_diff","patch":"private-source-must-not-cross"}`), 1)
	runRetainedDirectFailure(t, binary, repo, "execute", invalid)
}

func runRetainedDirectJSON(t *testing.T, binary, repo, operation string, input []byte, output any) {
	t.Helper()
	command := exec.Command(binary, "direct-run", operation, "--cwd", repo)
	command.Stdin = bytes.NewReader(input)
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	data, err := command.Output()
	if err != nil {
		t.Fatalf("%s failed: %v", operation, err)
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), output); err != nil {
		t.Fatalf("decode %s: %v: %q", operation, err, data)
	}
}

func runRetainedDirectFailure(t *testing.T, binary, repo, operation string, input []byte) {
	t.Helper()
	command := exec.Command(binary, "direct-run", operation, "--cwd", repo)
	command.Stdin = bytes.NewReader(input)
	data, err := command.Output()
	if err == nil || len(bytes.TrimSpace(data)) != 0 {
		t.Fatalf("%s accepted malformed input: err=%v output=%q", operation, err, data)
	}
}

func directRunGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
