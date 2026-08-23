package filecoord

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestObserveValidatesCanonicalTargetBeforeUnsupportedBackend(t *testing.T) {
	called, delegatedPath := false, ""
	old := observeSnapshotBackend
	observeSnapshotBackend = func(_ context.Context, path string) (*Snapshot, error) {
		called, delegatedPath = true, path
		return nil, &UnsupportedError{}
	}
	defer func() { observeSnapshotBackend = old }()
	for _, target := range []string{"", "bad\x00target"} {
		_, err := Observe(context.Background(), target)
		if !errors.Is(err, ErrInvalidTarget) || called {
			t.Fatalf("Observe(%q) = %v, backend called=%v", target, err, called)
		}
	}
	base := t.TempDir()
	messy := filepath.Join(base, "nested", "..", "target")
	_, _ = Observe(context.Background(), messy)
	want, err := filepath.Abs(filepath.Clean(messy))
	if err != nil {
		t.Fatal(err)
	}
	if !called || delegatedPath != filepath.Clean(want) {
		t.Fatalf("valid target delegated path = %q, want %q", delegatedPath, filepath.Clean(want))
	}
}

func TestObserveUnsupportedBackendHasNoFilesystemSideEffects(t *testing.T) {
	target := filepath.Join(t.TempDir(), "not-created")
	snapshot, err := Observe(context.Background(), target)
	if snapshot != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Observe() = snapshot:%v err:%v", snapshot, err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("Observe changed target: %v", statErr)
	}
	expected, err := newSnapshot(target, []byte("same"), 0o640, 7, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if revalidateErr := Revalidate(context.Background(), expected); !errors.Is(revalidateErr, ErrUnsupported) {
		t.Fatalf("Revalidate() = %v, want unsupported", revalidateErr)
	}
}

func TestSnapshotAccessorsAndConstructorCopyAuthoritativeData(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "nested", "..", "target")
	data, identity := []byte("content"), []byte("identity")
	snapshot, err := newSnapshot(path, data, 0o640, 7, identity)
	if err != nil {
		t.Fatal(err)
	}
	data[0], identity[0] = 'X', 'X'
	bytes := snapshot.Bytes()
	bytes[0] = 'Y'
	same, err := newSnapshot(filepath.Join(base, "target"), []byte("content"), 0o640, 7, []byte("identity"))
	if err != nil || compareSnapshots(*snapshot, *same) != nil || string(snapshot.Bytes()) != "content" || snapshot.Path() != filepath.Join(base, "target") || snapshot.Mode() != 0o640 || snapshot.Attributes() != 7 {
		t.Fatalf("snapshot accessors lost authority: path=%q mode=%v attrs=%d bytes=%q", snapshot.Path(), snapshot.Mode(), snapshot.Attributes(), snapshot.Bytes())
	}
}

func TestRevalidateRejectsNilAndInvalidSnapshots(t *testing.T) {
	for _, snapshot := range []*Snapshot{nil, {}, {path: filepath.Join(t.TempDir(), "target")}} {
		if err := Revalidate(context.Background(), snapshot); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("Revalidate(%#v) = %v, want invalid target", snapshot, err)
		}
	}
}

func TestCompareSnapshotsClassifiesExactDifferences(t *testing.T) {
	base := t.TempDir()
	makeSnapshot := func(path string, data []byte, mode fs.FileMode, attributes uint32, identity []byte) Snapshot {
		snapshot, err := newSnapshot(path, data, mode, attributes, identity)
		if err != nil {
			t.Fatal(err)
		}
		return *snapshot
	}
	want := makeSnapshot(filepath.Join(base, "target"), []byte("same"), 0o640, 7, []byte("one"))
	cases := []struct {
		name   string
		got    Snapshot
		reason ConflictReason
	}{
		{"path", makeSnapshot(filepath.Join(base, "other"), []byte("same"), 0o640, 7, []byte("one")), ConflictTopology},
		{"content", makeSnapshot(want.path, []byte("different"), 0o640, 7, []byte("one")), ConflictContent},
		{"mode", makeSnapshot(want.path, []byte("same"), 0o600, 7, []byte("one")), ConflictMode},
		{"attributes", makeSnapshot(want.path, []byte("same"), 0o640, 8, []byte("one")), ConflictMode},
		{"identity", makeSnapshot(want.path, []byte("same"), 0o640, 7, []byte("two")), ConflictIdentity},
		{"topology", makeSnapshot(want.path, []byte("same"), fs.ModeDir|0o755, 7, []byte("one")), ConflictTopology},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := compareSnapshots(want, test.got)
			var conflict *ConflictError
			if !errors.Is(err, ErrConflict) || !errors.As(err, &conflict) || conflict.Reason != test.reason {
				t.Fatalf("compareSnapshots() = %v, reason=%v", err, conflict)
			}
		})
	}
}

func TestConflictErrorUnwrapsWithoutNilChildren(t *testing.T) {
	for _, cause := range []error{nil, errors.New("backend detail")} {
		err := &ConflictError{Reason: ConflictContent, Cause: cause}
		if !errors.Is(err, ErrConflict) || cause != nil && !errors.Is(err, cause) {
			t.Fatalf("ConflictError does not preserve taxonomy and cause: %v", err)
		}
		for index, child := range err.Unwrap() {
			if child == nil {
				t.Fatalf("ConflictError.Unwrap()[%d] is nil", index)
			}
		}
	}
}

func TestRevalidateDelegatesValidSnapshot(t *testing.T) {
	snapshot, err := newSnapshot(filepath.Join(t.TempDir(), "target"), []byte("same"), 0o640, 7, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	old := revalidateSnapshotBackend
	revalidateSnapshotBackend = func(_ context.Context, got *Snapshot) error {
		called = got == snapshot
		return &ConflictError{Reason: ConflictIdentity}
	}
	defer func() { revalidateSnapshotBackend = old }()
	if err := Revalidate(context.Background(), snapshot); !errors.Is(err, ErrConflict) || !called {
		t.Fatalf("Revalidate() = %v, backend called=%v", err, called)
	}
}
