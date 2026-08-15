package sdd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/opencode"
)

type directRoleFileState struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func TestDirectRoleInstallFailureRestoresCoupledState(t *testing.T) {
	for _, point := range []directRoleFailurePoint{directRoleBeforeConfig, directRoleAfterConfig, directRoleBeforeLauncher, directRoleAfterLauncher, directRoleAfterSidecar} {
		t.Run(string(point), func(t *testing.T) {
			home := t.TempDir()
			paths := directRolePaths(home)
			before := snapshotDirectRoleFiles(t, paths)
			_, err := Inject(home, opencodeAdapter(), model.SDDModeSingle, InjectOptions{directRoleFailure: func(got directRoleFailurePoint) error {
				if got == point {
					return errors.New("injected direct-role failure")
				}
				return nil
			}})
			if err == nil || !strings.Contains(err.Error(), "injected direct-role failure") {
				t.Fatalf("Inject() error = %v, want injected failure", err)
			}
			assertDirectRoleFiles(t, paths, before)
		})
	}
}

func TestDirectRoleSyncFailureRestoresExactBytesAndModes(t *testing.T) {
	home := t.TempDir()
	if _, err := Inject(home, opencodeAdapter(), model.SDDModeSingle); err != nil {
		t.Fatal(err)
	}
	paths := directRolePaths(home)
	if err := os.WriteFile(paths[1], []byte("// user stale launcher\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths[1], 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotDirectRoleFiles(t, paths)
	_, err := Inject(home, opencodeAdapter(), model.SDDModeSingle, InjectOptions{
		RoleReconciliationMode: RoleReconciliationSync,
		directRoleFailure: func(point directRoleFailurePoint) error {
			if point == directRoleAfterSidecar {
				return errors.New("injected sync failure")
			}
			return nil
		},
	})
	if err == nil {
		t.Fatal("Inject() error = nil, want failure")
	}
	assertDirectRoleFiles(t, paths, before)
}

func TestDirectRoleRollbackFailureIsReported(t *testing.T) {
	home := t.TempDir()
	settings := directRolePaths(home)[0]
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rollback := false
	_, err := Inject(home, opencodeAdapter(), model.SDDModeSingle, InjectOptions{
		directRoleFailure: func(point directRoleFailurePoint) error {
			if point == directRoleAfterLauncher {
				rollback = true
				return errors.New("injected boundary failure")
			}
			return nil
		},
		directRoleJournalWriter: func(path string, data []byte, mode os.FileMode) (filemerge.WriteResult, error) {
			if rollback {
				return filemerge.WriteResult{}, errors.New("injected restore failure")
			}
			return filemerge.WriteFileAtomic(path, data, mode)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected boundary failure") || !strings.Contains(err.Error(), "injected restore failure") {
		t.Fatalf("Inject() error = %v, want boundary and rollback failures", err)
	}
}

func directRolePaths(home string) []string {
	settings := filepath.Join(home, ".config", "opencode", "opencode.json")
	plugins := filepath.Join(filepath.Dir(settings), "plugins")
	return []string{settings, filepath.Join(plugins, "managed-direct-run.ts"), opencode.DirectRoleArtifactRecordPath(plugins), filepath.Join(filepath.Dir(settings), ".gentle-ai-default-agent.json")}
}

func snapshotDirectRoleFiles(t *testing.T, paths []string) []directRoleFileState {
	t.Helper()
	states := make([]directRoleFileState, len(paths))
	for i, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		states[i] = directRoleFileState{exists: true, data: data, mode: info.Mode().Perm()}
	}
	return states
}

func assertDirectRoleFiles(t *testing.T, paths []string, want []directRoleFileState) {
	t.Helper()
	for i, path := range paths {
		got := snapshotDirectRoleFiles(t, []string{path})[0]
		if got.exists != want[i].exists || string(got.data) != string(want[i].data) || got.mode != want[i].mode {
			t.Fatalf("%s = %#v, want %#v", path, got, want[i])
		}
	}
}
