//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
