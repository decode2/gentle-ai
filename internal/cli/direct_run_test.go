package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/directrun"
)

func TestRunDirectRunJSONLifecycleAndAbortAuthorization(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handoff, err := directrun.NewHandoff("run-3026", "worker-3026", []string{repo}, "change note", []string{"note is updated"}, []directrun.Command{{Argv: []string{"go", "version"}, CWD: repo}})
	if err != nil {
		t.Fatal(err)
	}
	issue, _ := handoff.CanonicalJSON()
	var record directrun.Record
	runDirectJSON(t, repo, "issue", issue, &record)
	if record.State != directrun.RecordIssued {
		t.Fatalf("issue state = %q", record.State)
	}
	register, _ := json.Marshal(struct {
		Identity        string           `json:"identity"`
		Revision        directrun.Digest `json:"revision"`
		ParentSessionID string           `json:"parent_session_id"`
		ParentCallID    string           `json:"parent_call_id"`
		Agent           string           `json:"agent"`
	}{record.Handoff.Identity, record.Revision, "parent-3026", "call-3026", directrun.WorkerRole})
	runDirectJSON(t, repo, "register", register, &record)
	if record.State != directrun.RecordRegistered {
		t.Fatalf("register state = %q", record.State)
	}
	inspect, _ := json.Marshal(struct {
		Identity string `json:"identity"`
	}{record.Handoff.Identity})
	var inspected directrun.Record
	runDirectJSON(t, repo, "inspect", inspect, &inspected)
	if inspected.Revision != record.Revision {
		t.Fatal("inspect returned a different record")
	}
	abort := directrun.AbortRequest{Schema: directrun.AbortRequestSchema, Identity: record.Handoff.Identity, Revision: record.Revision, HandoffRevision: record.Handoff.Revision, ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: directrun.WorkerRole, RepositoryIdentity: record.RepositoryIdentity, Reason: directrun.AbortCancelled}
	payload, _ := abort.CanonicalJSON()
	runDirectJSON(t, repo, "abort", payload, &record)
	if record.State != directrun.RecordAborted {
		t.Fatalf("abort state = %q", record.State)
	}
}

func TestRunDirectRunRejectsNoncanonicalAndUnauthorizedAbort(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	for _, input := range [][]byte{nil, []byte(`{"identity":"x"}`), []byte(`{} {}`), []byte(` {}`), bytes.Repeat([]byte("x"), 1<<20+1)} {
		var stdout bytes.Buffer
		if err := RunDirectRun([]string{"inspect", "--cwd", repo}, bytes.NewReader(input), &stdout); !errors.Is(err, directrun.ErrInvalidTransition) || stdout.Len() != 0 {
			t.Fatalf("input %q: err=%v stdout=%q", input[:minDirect(len(input), 16)], err, stdout.String())
		}
	}
	var stdout bytes.Buffer
	if err := RunDirectRun([]string{"abort", "--cwd", repo}, bytes.NewReader([]byte(`{"schema":"gentle-ai.direct-run-abort/v1","identity":"run-3026","revision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","handoff_revision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","parent_session_id":"parent-3026","parent_call_id":"call-3026","agent":"gentle-worker","repository_identity":"repo","child_session_id":"","reason":"cancelled"}`)), &stdout); err == nil || stdout.Len() != 0 {
		t.Fatalf("unauthorized abort = %v stdout=%q", err, stdout.String())
	}
}

func runDirectJSON(t *testing.T, repo, command string, input []byte, output any) {
	t.Helper()
	var stdout bytes.Buffer
	if err := RunDirectRun([]string{command, "--cwd", repo}, bytes.NewReader(input), &stdout); err != nil {
		t.Fatalf("%s: %v", command, err)
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), output); err != nil {
		t.Fatalf("%s output %q: %v", command, stdout.String(), err)
	}
}
func minDirect(a, b int) int {
	if a < b {
		return a
	}
	return b
}
