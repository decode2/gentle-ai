package uninstall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/sdd"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/opencode"
)

type directRoleUninstallFileState struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func TestDirectRoleUninstallFailureRestoresCoupledState(t *testing.T) {
	for _, point := range []directRoleUninstallFailurePoint{directRoleUninstallBeforeSettings, directRoleUninstallAfterSettings, directRoleUninstallBeforeLauncher, directRoleUninstallAfterLauncher, directRoleUninstallAfterSidecar} {
		t.Run(string(point), func(t *testing.T) {
			svc, adapter := installedDirectRoleUninstallService(t)
			paths := directRoleUninstallPaths(svc.homeDir)
			before := snapshotDirectRoleUninstallFiles(t, paths)
			svc.directRoleFailure = func(got directRoleUninstallFailurePoint) error {
				if got == point {
					return errors.New("injected uninstall failure")
				}
				return nil
			}
			op := directRoleUninstallOperation(t, svc, adapter)
			if _, _, err := op.apply(op.path); err == nil || !strings.Contains(err.Error(), "injected uninstall failure") {
				t.Fatalf("uninstall operation error = %v, want injected failure", err)
			}
			assertDirectRoleUninstallFiles(t, paths, before)
		})
	}
}

func TestDirectRoleUninstallRollbackFailureIsReported(t *testing.T) {
	svc, adapter := installedDirectRoleUninstallService(t)
	rollback := false
	svc.directRoleFailure = func(point directRoleUninstallFailurePoint) error {
		if point == directRoleUninstallAfterLauncher {
			rollback = true
			return errors.New("injected boundary failure")
		}
		return nil
	}
	svc.directRoleJournalWriter = func(path string, data []byte, mode os.FileMode) (filemerge.WriteResult, error) {
		if rollback {
			return filemerge.WriteResult{}, errors.New("injected restore failure")
		}
		return filemerge.WriteFileAtomic(path, data, mode)
	}
	op := directRoleUninstallOperation(t, svc, adapter)
	if _, _, err := op.apply(op.path); err == nil || !strings.Contains(err.Error(), "injected boundary failure") || !strings.Contains(err.Error(), "injected restore failure") {
		t.Fatalf("uninstall operation error = %v, want boundary and rollback failures", err)
	}
}

func installedDirectRoleUninstallService(t *testing.T) (*Service, agents.Adapter) {
	t.Helper()
	home := t.TempDir()
	svc, err := NewService(home, t.TempDir(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := svc.registry.Get(model.AgentOpenCode)
	if !ok {
		t.Fatal("missing OpenCode adapter")
	}
	if _, err := sdd.Inject(home, adapter, model.SDDModeSingle); err != nil {
		t.Fatal(err)
	}
	return svc, adapter
}

func directRoleUninstallOperation(t *testing.T, svc *Service, adapter agents.Adapter) operation {
	t.Helper()
	ops, _, err := svc.componentOperations(adapter, model.ComponentSDD)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.path == adapter.SettingsPath(svc.homeDir) {
			return op
		}
	}
	t.Fatal("missing direct-role uninstall operation")
	return operation{}
}

func directRoleUninstallPaths(home string) []string {
	settings := filepath.Join(home, ".config", "opencode", "opencode.json")
	plugins := filepath.Join(filepath.Dir(settings), "plugins")
	return []string{settings, filepath.Join(filepath.Dir(settings), ".gentle-ai-default-agent.json"), filepath.Join(plugins, "managed-direct-run.ts"), opencode.DirectRoleArtifactRecordPath(plugins)}
}

func snapshotDirectRoleUninstallFiles(t *testing.T, paths []string) []directRoleUninstallFileState {
	t.Helper()
	states := make([]directRoleUninstallFileState, len(paths))
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
		states[i] = directRoleUninstallFileState{exists: true, data: data, mode: info.Mode().Perm()}
	}
	return states
}

func assertDirectRoleUninstallFiles(t *testing.T, paths []string, want []directRoleUninstallFileState) {
	t.Helper()
	for i, path := range paths {
		got := snapshotDirectRoleUninstallFiles(t, []string{path})[0]
		if got.exists != want[i].exists || string(got.data) != string(want[i].data) || got.mode != want[i].mode {
			t.Fatalf("%s = %#v, want %#v", path, got, want[i])
		}
	}
}
