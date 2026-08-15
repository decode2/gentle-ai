//go:build linux

package directrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		{"raw-patch", `{"query":"patch"}`, false}, {"arguments", `{"query":"git_diff","argv":["status"]}`, false}, {"environment", `{"query":"git_diff","env":{"GIT_DIR":"x"}}`, false}, {"ref", `{"query":"git_diff","ref":"HEAD~1"}`, false}, {"patch-extra", `{"query":"git_diff","patch":"raw"}`, false}, {"path-override", `{"query":"git_status","path":"x"}`, false},
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
		{"malformed-unmerged", func() error {
			_, err := parseGitStatus([]byte("# branch.head main\x00u U. N... 100644 100644 100644 100644 a b c conflict.txt\x00"))
			return err
		}},
		{"unexpected-unmerged", func() error {
			_, err := parseGitStatus([]byte("# branch.head main\x001 UU N... 100644 a b c conflict.txt\x00"))
			return err
		}},
		{"overflow", func() error { _, err := gitNumber("999999999999999999999999"); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse(); err == nil {
				t.Fatal("accepted malformed Git output")
			}
		})
	}
}

func TestRetainedGitInspectorReportsPorcelainUnmergedEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a real Git repository")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	writeGitFile(t, repo, "conflict.txt", "base\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "base")
	git(t, repo, "checkout", "-b", "side")
	writeGitFile(t, repo, "conflict.txt", "side\n")
	git(t, repo, "commit", "-am", "side")
	git(t, repo, "checkout", "main")
	writeGitFile(t, repo, "conflict.txt", "main\n")
	git(t, repo, "commit", "-am", "main")
	gitExpectFailure(t, repo, "merge", "side")

	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := newRetainedGitInspector(lease)
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close()
	inspection, err := inspector.inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status := mustGitStatusResult(t, inspection)
	for _, entry := range status.Entries {
		if entry.Path == "conflict.txt" && entry.Index == "U" && entry.Worktree == "U" && entry.Unmerged {
			return
		}
	}
	t.Fatalf("unmerged conflict missing from %#v", status.Entries)
}

func TestRetainedGitInspectorIgnoresHostileEnvironmentAndConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a real Git repository")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	writeGitFile(t, repo, "note.txt", "safe\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	writeGitFile(t, repo, "note.txt", "safe\nchanged\n")
	marker := filepath.Join(t.TempDir(), "hostile-ran")
	git(t, repo, "config", "alias.status", "!touch "+marker)
	git(t, repo, "config", "diff.external", "touch "+marker)
	git(t, repo, "config", "core.hooksPath", filepath.Dir(marker))
	writeGitFile(t, repo, ".gitattributes", "*.txt diff=hostile\n")
	git(t, repo, "config", "diff.hostile.textconv", "touch "+marker)
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := newRetainedGitInspector(lease)
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close()
	home := t.TempDir()
	writeGitFile(t, home, ".gitconfig", "[alias]\nstatus = !touch "+marker+"\n")
	for key, value := range map[string]string{
		"HOME": home, "GIT_DIR": filepath.Join(t.TempDir(), "forged"), "GIT_WORK_TREE": t.TempDir(),
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "alias.status", "GIT_CONFIG_VALUE_0": "!touch " + marker,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": t.TempDir(), "GIT_TRACE": marker, "GIT_TRACE_PACKET": marker, "GIT_PAGER": "false", "GIT_ASKPASS": "/bin/false",
	} {
		t.Setenv(key, value)
	}
	result, err := inspector.inspect(context.Background())
	if err != nil || !hasGitPath(result.Unstaged, "note.txt") {
		t.Fatalf("inspection=%#v err=%v", result, err)
	}
	payload, _ := json.Marshal(result)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) || strings.Contains(string(payload), marker) || strings.Contains(string(payload), "safe\nchanged") {
		t.Fatalf("hostile configuration executed or leaked: marker=%v payload=%s", err, payload)
	}
}

func TestRetainedGitInspectorFailsClosedWhenRepositoryChangesBeforeFinalValidation(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		repo := t.TempDir()
		git(t, repo, "init", "-b", "main")
		lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		inspector, err := newRetainedGitInspector(lease)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(context.Background(), retainedGitTestHookKey{}, retainedGitTestHook{beforeFinalValidation: func() {
			if err := os.Rename(repo, repo+"-replaced"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(repo, 0o700); err != nil {
				t.Fatal(err)
			}
		}})
		result, err := inspector.inspect(ctx)
		inspector.Close()
		if !errors.Is(err, ErrOperationUnavailable) || result.Evidence != "" || len(result.Status.Changes) != 0 || len(result.Staged) != 0 || len(result.Unstaged) != 0 {
			t.Fatalf("attempt=%d result=%#v err=%v", attempt, result, err)
		}
	}
}

func TestRetainedGitRuntimeReturnsSanitizedErrorAfterIdentityDrift(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	runtime, err := OpenRuntime(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handoff, err := (Handoff{Schema: HandoffSchema, Identity: "git-drift", Worker: WorkerIdentity{Role: WorkerRole, ID: "worker-3026"}, AllowedEditRoots: []string{repo}, TargetBehavior: "inspect Git", AcceptanceCriteria: []string{"structured state"}, Verification: []Command{{Argv: []string{"go", "version"}, CWD: repo}}}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	record, err := runtime.Issue(context.Background(), handoff)
	if err != nil {
		t.Fatal(err)
	}
	record, err = runtime.RegisterTask(context.Background(), record.Handoff.Identity, record.Revision, "parent-3026", "call-3026", WorkerRole)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Schema: OperationSchema, Identity: record.Handoff.Identity, Operation: "direct_inspect", RequestID: "request-git-drift", SessionID: "child-3026", HandoffRevision: string(record.Handoff.Revision), BindingRevision: string(record.Revision), ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: WorkerRole, Payload: []byte(`{"query":"git_status"}`)}
	ctx := context.WithValue(context.Background(), retainedGitTestHookKey{}, retainedGitTestHook{beforeFinalValidation: func() {
		if err := os.Rename(repo, repo+"-replaced"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(repo, 0o700); err != nil {
			t.Fatal(err)
		}
	}})
	response, err := runtime.Execute(ctx, request)
	if err != nil || response.Status != "error" || response.Result != nil || response.Error == nil || response.Error.Code != "backend_failure" || response.Error.Message != "operation failed" || response.Operation != request.Operation || response.RequestID != request.RequestID {
		t.Fatalf("response=%#v err=%v", response, err)
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

func gitExpectFailure(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("git %v unexpectedly succeeded: %s", args, output)
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
