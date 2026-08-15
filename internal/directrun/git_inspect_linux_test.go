//go:build linux

package directrun

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func TestRetainedGitInspectorCapturesStructuredWorktreeEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a real Git repository")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	writeGitFile(t, repo, "tracked file.txt", "one\n")
	writeGitFile(t, repo, "unicode-café.txt", "one\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	writeGitFile(t, repo, "tracked file.txt", "one\ntwo\n")
	git(t, repo, "add", "tracked file.txt")
	writeGitFile(t, repo, "tracked file.txt", "one\ntwo\nthree\n")
	writeGitFile(t, repo, "staged.txt", "staged\n")
	git(t, repo, "add", "staged.txt")
	if err := os.Rename(filepath.Join(repo, "unicode-café.txt"), filepath.Join(repo, "renamed café.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "rm", "--cached", "unicode-café.txt")
	git(t, repo, "add", "renamed café.txt")
	if err := os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "binary.dat")
	writeGitFile(t, repo, "untracked space.txt", "untracked\n")

	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := newRetainedGitInspector(lease)
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close()
	result, err := inspector.inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Head != "main" || result.Evidence == "" {
		t.Fatalf("status=%#v evidence=%q", result.Status, result.Evidence)
	}
	if !hasGitPath(result.Status.Untracked, "untracked space.txt") || !hasGitPath(result.Staged, "staged.txt") || !hasGitPath(result.Staged, "renamed café.txt") || !hasGitPath(result.Staged, "binary.dat") || !hasGitPath(result.Unstaged, "tracked file.txt") {
		t.Fatalf("result=%#v", result)
	}
	if !gitChangeFor(result.Staged, "binary.dat").Binary {
		t.Fatalf("binary change=%#v", gitChangeFor(result.Staged, "binary.dat"))
	}
	if rename := gitChangeFor(result.Staged, "renamed café.txt"); rename.OldPath != "unicode-café.txt" {
		t.Fatalf("rename=%#v", rename)
	}
	for _, public := range []any{mustGitStatusResult(t, result), mustGitDiffResult(t, result)} {
		payload, err := json.Marshal(public)
		if err != nil {
			t.Fatal(err)
		}
		response := Response{Schema: OperationSchema, Operation: "direct_inspect", RequestID: "request-3026", Status: "ok", Result: payload}
		if err := response.Validate(); err != nil {
			t.Fatalf("public result rejected: %v: %s", err, payload)
		}
		canonical, err := response.CanonicalJSON()
		if err != nil || !bytes.Equal(canonical, mustJSON(t, response)) {
			t.Fatalf("noncanonical response: %v", err)
		}
	}
}

func TestGitInspectPayloadsAreStrict(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		want          bool
	}{
		{"status", `{"query":"git_status"}`, true}, {"diff", `{"query":"git_diff"}`, true}, {"tree", `{"query":"tree"}`, true},
		{"raw-patch", `{"query":"patch"}`, false}, {"arguments", `{"query":"git_diff","argv":["status"]}`, false}, {"path-override", `{"query":"git_status","path":"x"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := Request{Schema: OperationSchema, Identity: "identity-3026", Operation: "direct_inspect", RequestID: "request-3026", SessionID: "session-3026", HandoffRevision: "handoff-3026", BindingRevision: "binding-3026", ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: WorkerRole, Payload: []byte(test.payload)}
			if (request.Validate() == nil) != test.want {
				t.Fatalf("payload %s accepted=%v", test.payload, !test.want)
			}
		})
	}
}

func mustGitStatusResult(t *testing.T, value gitInspection) gitStatusResult {
	t.Helper()
	result, err := value.statusResult()
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustGitDiffResult(t *testing.T, value gitInspection) gitDiffResult {
	t.Helper()
	result, err := value.diffResult()
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGitParsersRejectMalformedRecords(t *testing.T) {
	for _, test := range []struct {
		name  string
		parse func() error
	}{
		{"unterminated-status", func() error { _, err := parseGitStatus([]byte("# branch.head main")); return err }},
		{"unknown-status", func() error { _, err := parseGitStatus([]byte("x nope\x00")); return err }},
		{"bad-utf8", func() error { _, err := parseGitStatus([]byte("? \xff\x00")); return err }},
		{"missing-rename-source", func() error {
			_, err := parseGitStatus([]byte("2 R. N... 100644 100644 100644 a b R100 path\x00"))
			return err
		}},
		{"bad-numstat", func() error { _, err := parseGitNumstat([]byte("x\t1\tpath\x00")); return err }},
		{"overflow", func() error { _, err := gitNumber("999999999999999999999999"); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse(); err == nil {
				t.Fatal("accepted malformed Git output")
			}
		})
	}
}

func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func writeGitFile(t *testing.T, repo, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasGitPath(changes interface{}, path string) bool {
	switch values := changes.(type) {
	case []string:
		for _, value := range values {
			if value == path {
				return true
			}
		}
	case []gitChange:
		for _, value := range values {
			if value.Path == path {
				return true
			}
		}
	}
	return false
}

func gitChangeFor(changes []gitChange, path string) gitChange {
	for _, change := range changes {
		if change.Path == path {
			return change
		}
	}
	return gitChange{}
}
