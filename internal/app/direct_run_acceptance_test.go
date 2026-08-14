//go:build linux

package app

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

func TestMainBinaryDirectRunAdmissionBoundary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "gentle-ai")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/gentle-ai")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	root := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, filepath.Join(root, "env", key))
	}
	repo := filepath.Join(root, "repo")
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handoff, err := (directrun.Handoff{Schema: directrun.HandoffSchema, Identity: "binary-run", Worker: directrun.WorkerIdentity{Role: directrun.WorkerRole, ID: "worker-3026"}, AllowedEditRoots: []string{repo}, TargetBehavior: "update note", AcceptanceCriteria: []string{"note updated"}, Verification: []directrun.Command{{Argv: []string{"go", "version"}, CWD: repo}}}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := handoff.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runDirectBinary(binary, repo, payload, "direct-run", "issue", "--cwd", repo)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("explicit cwd issue: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	var record directrun.Record
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &record); err != nil || record.State != directrun.RecordIssued {
		t.Fatalf("issue stdout=%q decode=%v", stdout, err)
	}
	canonical, err := record.CanonicalJSON()
	if err != nil || string(bytes.TrimSpace(stdout)) != string(canonical) {
		t.Fatalf("stdout is not canonical record: %q", stdout)
	}

	omitted, err := (directrun.Handoff{Schema: directrun.HandoffSchema, Identity: "binary-omitted", Worker: directrun.WorkerIdentity{Role: directrun.WorkerRole, ID: "worker-3026"}, AllowedEditRoots: []string{repo}, TargetBehavior: "read note", AcceptanceCriteria: []string{"note readable"}, Verification: []directrun.Command{{Argv: []string{"go", "version"}, CWD: repo}}}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	omittedPayload, _ := omitted.CanonicalJSON()
	stdout, stderr, err = runDirectBinary(binary, repo, omittedPayload, "direct-run", "issue")
	if err != nil || len(stderr) != 0 || !json.Valid(bytes.TrimSpace(stdout)) {
		t.Fatalf("omitted cwd issue: %v stdout=%q stderr=%q", err, stdout, stderr)
	}

	for _, test := range []struct {
		name  string
		input []byte
		args  []string
	}{
		{"missing stdin", nil, []string{"direct-run", "inspect", "--cwd", repo}},
		{"unknown operation", []byte(`{}`), []string{"direct-run", "unknown", "--cwd", repo}},
		{"unknown flag", []byte(`{}`), []string{"direct-run", "inspect", "--bad", repo}},
		{"duplicate cwd", []byte(`{}`), []string{"direct-run", "inspect", "--cwd", repo, "--cwd", repo}},
		{"foreign cwd", []byte(`{}`), []string{"direct-run", "inspect", "--cwd", root}},
		{"missing cwd", []byte(`{}`), []string{"direct-run", "inspect", "--cwd", filepath.Join(root, "missing")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := runDirectBinary(binary, repo, test.input, test.args...)
			if err == nil || len(stdout) != 0 || !strings.HasPrefix(string(stderr), "Error: ") || strings.ContainsAny(string(stderr), "\n\r") && strings.Count(string(stderr), "\n") != 1 {
				t.Fatalf("failure err=%v stdout=%q stderr=%q", err, stdout, stderr)
			}
			for _, secret := range []string{root, os.Getenv("HOME"), "sha256:", "token", "credential"} {
				if secret != "" && strings.Contains(string(stderr), secret) {
					t.Fatalf("failure leaked %q in %q", secret, stderr)
				}
			}
		})
	}
}

func runDirectBinary(binary, dir string, input []byte, args ...string) ([]byte, []byte, error) {
	command := exec.Command(binary, args...)
	command.Dir = dir
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
