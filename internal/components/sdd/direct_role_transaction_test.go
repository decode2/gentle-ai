package sdd

import (
	"encoding/json"
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
	target string
}

func TestDirectRoleInstallCreatesManagedArtifact(t *testing.T) {
	home := t.TempDir()
	if _, err := Inject(home, opencodeAdapter(), model.SDDModeSingle); err != nil {
		t.Fatal(err)
	}
	paths := directRolePaths(home)
	if refreshable, reason := opencode.ManagedDirectRunArtifactRefreshable(filepath.Dir(paths[1]), paths[1]); !refreshable {
		t.Fatalf("install did not create managed artifact: %s", reason)
	}
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

func TestDirectRoleSyncPreservesAmbiguousArtifacts(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, home, launcher, sidecar string)
		warning string
	}{
		{"both absent", func(t *testing.T, _, launcher, sidecar string) { t.Helper(); os.Remove(launcher); os.Remove(sidecar) }, "both launcher and sidecar are absent"},
		{"launcher without sidecar", func(t *testing.T, _, _, sidecar string) { t.Helper(); os.Remove(sidecar) }, "launcher exists without ownership sidecar"},
		{"sidecar without launcher", func(t *testing.T, _, launcher, _ string) { t.Helper(); os.Remove(launcher) }, "sidecar exists without launcher"},
		{"malformed sidecar", func(t *testing.T, _, _, sidecar string) {
			t.Helper()
			writeDirectRoleFile(t, sidecar, []byte("{"), 0o600)
		}, "sidecar is malformed"},
		{"wrong owner", func(t *testing.T, _, _, sidecar string) {
			t.Helper()
			writeArtifactRecord(t, sidecar, map[string]any{"schema": "gentle-ai.opencode-direct-role-artifact/v1", "owner": "other", "kind": "managed-direct-run-plugin", "path": "ignored", "mode": 420, "fingerprint": "sha256:abc"})
		}, "sidecar owner does not match"},
		{"wrong kind", func(t *testing.T, _, _, sidecar string) {
			t.Helper()
			writeArtifactRecord(t, sidecar, map[string]any{"schema": "gentle-ai.opencode-direct-role-artifact/v1", "owner": opencode.ManagedOwner, "kind": "other", "path": "ignored", "mode": 420, "fingerprint": "sha256:abc"})
		}, "sidecar kind does not match"},
		{"wrong path", func(t *testing.T, _, _, sidecar string) {
			t.Helper()
			writeArtifactRecord(t, sidecar, map[string]any{"schema": "gentle-ai.opencode-direct-role-artifact/v1", "owner": opencode.ManagedOwner, "kind": "managed-direct-run-plugin", "path": "other", "mode": 420, "fingerprint": "sha256:abc"})
		}, "sidecar path does not match launcher"},
		{"launcher symlink", func(t *testing.T, home, launcher, _ string) {
			t.Helper()
			os.Remove(launcher)
			target := filepath.Join(home, "user.ts")
			writeDirectRoleFile(t, target, []byte("// user"), 0o644)
			if err := os.Symlink(target, launcher); err != nil {
				t.Fatal(err)
			}
		}, "launcher is not a regular file"},
		{"launcher directory", func(t *testing.T, _, launcher, _ string) {
			t.Helper()
			os.Remove(launcher)
			if err := os.Mkdir(launcher, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "launcher is not a regular file"},
		{"launcher mode drift", func(t *testing.T, _, launcher, _ string) {
			t.Helper()
			if err := os.Chmod(launcher, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "launcher mode drift"},
		{"launcher fingerprint drift", func(t *testing.T, _, launcher, _ string) {
			t.Helper()
			writeDirectRoleFile(t, launcher, []byte("// user drift\n"), 0o644)
		}, "launcher fingerprint drift"},
		{"sidecar symlink", func(t *testing.T, home, _, sidecar string) {
			t.Helper()
			os.Remove(sidecar)
			target := filepath.Join(home, "sidecar.json")
			writeDirectRoleFile(t, target, []byte("{}\n"), 0o600)
			if err := os.Symlink(target, sidecar); err != nil {
				t.Fatal(err)
			}
		}, "sidecar is not a regular file"},
		{"sidecar mode drift", func(t *testing.T, _, _, sidecar string) {
			t.Helper()
			if err := os.Chmod(sidecar, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "sidecar mode drift"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := Inject(home, opencodeAdapter(), model.SDDModeSingle); err != nil {
				t.Fatal(err)
			}
			paths := directRolePaths(home)
			tt.setup(t, home, paths[1], paths[2])
			before := snapshotDirectRoleFiles(t, paths[1:3])
			result, err := Inject(home, opencodeAdapter(), model.SDDModeSingle, InjectOptions{RoleReconciliationMode: RoleReconciliationSync})
			if err != nil {
				t.Fatal(err)
			}
			assertDirectRoleFiles(t, paths[1:3], before)
			if !containsString(result.OwnershipWarnings, "preserved managed direct-run artifact: "+tt.warning) {
				t.Fatalf("warnings = %v, want %q", result.OwnershipWarnings, tt.warning)
			}
		})
	}
}

func TestDirectRoleSyncPreservesArtifactWithoutManagedRoles(t *testing.T) {
	home := t.TempDir()
	if _, err := Inject(home, opencodeAdapter(), model.SDDModeSingle); err != nil {
		t.Fatal(err)
	}
	paths := directRolePaths(home)
	writeDirectRoleFile(t, paths[0], []byte(`{"agent":{}}`+"\n"), 0o644)
	before := snapshotDirectRoleFiles(t, paths[1:3])
	result, err := Inject(home, opencodeAdapter(), model.SDDModeSingle, InjectOptions{RoleReconciliationMode: RoleReconciliationSync})
	if err != nil {
		t.Fatal(err)
	}
	assertDirectRoleFiles(t, paths[1:3], before)
	if !containsString(result.OwnershipWarnings, "preserved managed direct-run artifact: no ownership-valid direct roles remain") {
		t.Fatalf("warnings = %v", result.OwnershipWarnings)
	}
}

func TestDirectRoleSyncRefreshesOnlyProvenManagedArtifact(t *testing.T) {
	home := t.TempDir()
	if _, err := Inject(home, opencodeAdapter(), model.SDDModeSingle); err != nil {
		t.Fatal(err)
	}
	paths := directRolePaths(home)
	var writes []string
	_, err := Inject(home, opencodeAdapter(), model.SDDModeSingle, InjectOptions{
		RoleReconciliationMode: RoleReconciliationSync,
		directRoleJournalWriter: func(path string, data []byte, mode os.FileMode) (filemerge.WriteResult, error) {
			writes = append(writes, path)
			return filemerge.WriteFileAtomic(path, data, mode)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(writes, paths[1]) || !containsString(writes, paths[2]) {
		t.Fatalf("sync writes = %v, want proven artifact pair", writes)
	}
}

func writeDirectRoleFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func writeArtifactRecord(t *testing.T, path string, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeDirectRoleFile(t, path, append(data, '\n'), 0o600)
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
		state := directRoleFileState{exists: true, mode: info.Mode()}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				t.Fatal(err)
			}
			state.target = target
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			state.data = data
		}
		states[i] = state
	}
	return states
}

func assertDirectRoleFiles(t *testing.T, paths []string, want []directRoleFileState) {
	t.Helper()
	for i, path := range paths {
		got := snapshotDirectRoleFiles(t, []string{path})[0]
		if got.exists != want[i].exists || string(got.data) != string(want[i].data) || got.mode != want[i].mode || got.target != want[i].target {
			t.Fatalf("%s = %#v, want %#v", path, got, want[i])
		}
	}
}
