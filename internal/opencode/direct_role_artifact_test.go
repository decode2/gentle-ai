package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectRoleArtifactRecordMatchesExactRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed-direct-run.ts")
	content := []byte("managed")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteDirectRoleArtifactRecord(dir, path, content); err != nil {
		t.Fatal(err)
	}
	record, err := ReadDirectRoleArtifactRecord(dir)
	if err != nil || !DirectRoleArtifactMatches(record, path) {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if err := os.WriteFile(path, []byte("drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if DirectRoleArtifactMatches(record, path) {
		t.Fatal("drifted artifact matched")
	}
}
