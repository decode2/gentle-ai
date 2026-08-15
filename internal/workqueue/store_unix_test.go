//go:build unix

package workqueue

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func storeFixture(t *testing.T) (*Store, GraphSnapshot, string) {
	t.Helper()
	common := t.TempDir()
	store, err := OpenStore(common)
	if err != nil {
		t.Fatal(err)
	}
	return store, snapshot(t, common, testInput(common)), common
}

func writeStoreState(t *testing.T, store *Store, graph GraphSnapshot) {
	t.Helper()
	data, err := Encode(graph, queueState())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.state, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func requireStoreError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestStoreBootstrapsPrivateAuthorityWithoutState(t *testing.T) {
	store, graph, common := storeFixture(t)
	for _, path := range store.authority {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatalf("authority %s = %v, %v", path, info, err)
		}
	}
	if store.state != filepath.Join(common, "gentle-ai", "workqueue", "v1", "state.json") {
		t.Fatalf("state path = %q", store.state)
	}
	if _, err := os.Lstat(store.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenStore created state: %v", err)
	}
	_, err := store.Load(graph)
	requireStoreError(t, err, ErrStoreNotInitialized)
}

func TestStoreLoadsCanonicalBoundState(t *testing.T) {
	store, graph, _ := storeFixture(t)
	writeStoreState(t, store, graph)
	state, err := store.Load(graph)
	if err != nil || state.GraphRevision != graph.GraphRevision() || len(state.Items) != 2 {
		t.Fatalf("Load() = %#v, %v", state, err)
	}
}

func TestStoreRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, store *Store)
	}{
		{"group writable authority", func(t *testing.T, s *Store) {
			t.Helper()
			if err := os.Chmod(s.authority[0], 0770); err != nil {
				t.Fatal(err)
			}
		}},
		{"world writable authority", func(t *testing.T, s *Store) {
			t.Helper()
			if err := os.Chmod(s.authority[0], 0777); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink authority", func(t *testing.T, s *Store) {
			t.Helper()
			if err := os.Remove(s.authority[2]); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), s.authority[2]); err != nil {
				t.Fatal(err)
			}
		}},
		{"state symlink", func(t *testing.T, s *Store) {
			t.Helper()
			target := filepath.Join(t.TempDir(), "state")
			if err := os.WriteFile(target, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, s.state); err != nil {
				t.Fatal(err)
			}
		}},
		{"non-regular state", func(t *testing.T, s *Store) {
			t.Helper()
			if err := os.Mkdir(s.state, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong state mode", func(t *testing.T, s *Store) {
			t.Helper()
			if err := os.WriteFile(s.state, nil, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(s.state, 0644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store, graph, _ := storeFixture(t)
			tt.mutate(t, store)
			_, err := store.Load(graph)
			requireStoreError(t, err, ErrUnsafeStorePath)
		})
	}
}

func TestStoreRevalidatesAuthorityAndOwnership(t *testing.T) {
	store, graph, _ := storeFixture(t)
	writeStoreState(t, store, graph)
	if err := os.Chmod(store.authority[1], 0770); err != nil {
		t.Fatal(err)
	}
	_, err := store.Load(graph)
	requireStoreError(t, err, ErrUnsafeStorePath)
	info, statErr := os.Lstat(store.authority[0])
	if statErr != nil || ownedBy(info, os.Geteuid()+1) {
		t.Fatalf("authority ownership helper = %v, %v", info, statErr)
	}
	if err := os.Chmod(store.authority[1], 0700); err != nil {
		t.Fatal(err)
	}
	info, statErr = os.Lstat(store.state)
	if statErr != nil || ownedBy(info, os.Geteuid()+1) {
		t.Fatalf("state ownership helper = %v, %v", info, statErr)
	}
}

func TestStoreRejectsReplacedIdentities(t *testing.T) {
	for _, tt := range []struct {
		name string
		path func(*Store) string
	}{
		{"common directory", func(s *Store) string { return s.common }},
		{"authority directory", func(s *Store) string { return s.authority[0] }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, graph, _ := storeFixture(t)
			writeStoreState(t, store, graph)
			path := tt.path(store)
			if err := os.Rename(path, filepath.Join(t.TempDir(), "replaced")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			_, err := store.Load(graph)
			requireStoreError(t, err, ErrUnsafeStorePath)
		})
	}
}

func TestStorePreservesEnvelopeCauses(t *testing.T) {
	store, graph, _ := storeFixture(t)
	for _, tt := range []struct {
		name string
		data []byte
		want error
	}{
		{"malformed", []byte("{"), ErrInvalidEnvelope},
		{"graph mismatch", []byte(`{"schema":"gentle-ai.workqueue/v1","graph_revision":"` + digest("9") + `","items":[],"revision":"` + digest("1") + `"}`), ErrGraphMismatch},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(store.state, tt.data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := store.Load(graph)
			requireStoreError(t, err, tt.want)
		})
	}
}
