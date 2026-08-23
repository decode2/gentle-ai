//go:build android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package filecoord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func writePOSIXSnapshotFile(t *testing.T, path, data string, mode os.FileMode) {
	mustFS(t, os.WriteFile(path, []byte(data), mode))
}

func observePOSIXSnapshot(t *testing.T, path string) *Snapshot {
	snapshot, err := Observe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func wantSnapshotConflict(t *testing.T, err error, reason ConflictReason) {
	var conflict *ConflictError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &conflict) || conflict.Reason != reason {
		t.Fatalf("error = %v, reason = %v, want conflict %q", err, conflict, reason)
	}
}

func TestPOSIXObserveRegularCopiesBytesModeAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	writePOSIXSnapshotFile(t, path, "immutable\n", 0o640)
	snapshot := observePOSIXSnapshot(t, path)
	if snapshot.Path() != path || string(snapshot.Bytes()) != "immutable\n" || snapshot.Mode().Perm() != 0o640 || snapshot.Mode().Type() != 0 || snapshot.Attributes()&0o777 != 0o640 || len(snapshot.identity) == 0 {
		t.Fatalf("snapshot = path:%q bytes:%q mode:%v attrs:%#o identity:%x", snapshot.Path(), snapshot.Bytes(), snapshot.Mode(), snapshot.Attributes(), snapshot.identity)
	}
	bytes := snapshot.Bytes()
	bytes[0] = 'X'
	if string(snapshot.Bytes()) != "immutable\n" {
		t.Fatal("Snapshot.Bytes returned mutable authoritative data")
	}
	if err := compareSnapshots(*snapshot, *observePOSIXSnapshot(t, path)); err != nil {
		t.Fatalf("repeat observation differs: %v", err)
	}
}
func TestPOSIXRevalidateDetectsContentModeAndIdentityChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason ConflictReason
	}{
		{"content", ConflictContent}, {"mode", ConflictMode}, {"identity", ConflictIdentity},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "target")
			writePOSIXSnapshotFile(t, path, "same\n", 0o640)
			snapshot := observePOSIXSnapshot(t, path)
			switch test.name {
			case "content":
				writePOSIXSnapshotFile(t, path, "changed\n", 0o640)
			case "mode":
				mustFS(t, os.Chmod(path, 0o600))
			case "identity":
				replacement := path + ".replacement"
				writePOSIXSnapshotFile(t, replacement, "same\n", 0o640)
				mustFS(t, os.Rename(replacement, path))
			}
			wantSnapshotConflict(t, Revalidate(context.Background(), snapshot), test.reason)
		})
	}
}

func TestPOSIXObserveRefusesUnsafeTopologyAndPreservesSymlinkTarget(t *testing.T) {
	for _, name := range []string{"final symlink", "intermediate symlink", "directory", "hardlink"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			marker := filepath.Join(base, "marker")
			writePOSIXSnapshotFile(t, marker, "untouched", 0o640)
			var path string
			switch name {
			case "final symlink":
				path = filepath.Join(base, "final")
				mustSymlink(t, marker, path)
			case "intermediate symlink":
				real := mustMkdir(t, filepath.Join(base, "real"), 0o755)
				writePOSIXSnapshotFile(t, filepath.Join(real, "target"), "real", 0o640)
				path = filepath.Join(base, "link", "target")
				mustSymlink(t, real, filepath.Dir(path))
			case "directory":
				path = mustMkdir(t, filepath.Join(base, "directory"), 0o755)
			case "hardlink":
				path = filepath.Join(base, "hardlink")
				writePOSIXSnapshotFile(t, path, "linked", 0o640)
				mustHardlink(t, path, path+".alias")
			}
			snapshot, err := Observe(context.Background(), path)
			if snapshot != nil || !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("Observe() = snapshot:%v err:%v", snapshot, err)
			}
			data, _ := os.ReadFile(marker)
			if string(data) != "untouched" {
				t.Fatalf("symlink target changed: %q", data)
			}
		})
	}
}
func TestPOSIXObserveRefusesFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	mustFS(t, unix.Mkfifo(fifo, 0o600))
	done := make(chan error, 1)
	go func() { _, err := Observe(context.Background(), fifo); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("Observe(FIFO) = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Observe(FIFO) blocked")
	}
	info, err := os.Lstat(fifo)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO changed: info=%v err=%v", info, err)
	}
}
func TestPOSIXRevalidateMapsMissingAndTopology(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason ConflictReason
	}{
		{"missing", ConflictMissing}, {"final symlink", ConflictTopology}, {"hardlink", ConflictTopology},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "target")
			writePOSIXSnapshotFile(t, path, "same\n", 0o640)
			snapshot := observePOSIXSnapshot(t, path)
			switch test.name {
			case "missing":
				mustFS(t, os.Remove(path))
			case "final symlink":
				mustFS(t, os.Remove(path))
				mustSymlink(t, path+".target", path)
			case "hardlink":
				mustHardlink(t, path, path+".alias")
			}
			err := Revalidate(context.Background(), snapshot)
			wantSnapshotConflict(t, err, test.reason)
			if test.reason == ConflictMissing && !errors.Is(err, unix.ENOENT) {
				t.Fatalf("missing cause not preserved: %v", err)
			}
		})
	}
}
func TestPOSIXObserveHonorsContextAndRejectsMutationDuringRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	writePOSIXSnapshotFile(t, path, "stable\n", 0o640)
	oldRead := snapshotRead
	defer func() { snapshotRead = oldRead }()
	ctx, cancel := context.WithCancel(context.Background())
	snapshotRead = func(file *os.File, buffer []byte) (int, error) { cancel(); return file.Read(buffer) }
	_, err := Observe(ctx, path)
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled observation = %v", err)
	}

	snapshotRead = oldRead
	snapshotRead = func(file *os.File, buffer []byte) (int, error) {
		if writeErr := os.WriteFile(path, []byte("mutated\n"), 0o640); writeErr != nil {
			return 0, writeErr
		}
		return file.Read(buffer)
	}
	_, err = Observe(context.Background(), path)
	wantSnapshotConflict(t, err, ConflictContent)
}
