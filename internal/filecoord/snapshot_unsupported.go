package filecoord

import "context"

// Layer 5 intentionally makes no platform capability or filesystem change.
func observeSnapshotUnsupported(context.Context, string) (*Snapshot, error) {
	return nil, &UnsupportedError{}
}

func revalidateSnapshotUnsupported(context.Context, *Snapshot) error {
	return &UnsupportedError{}
}

var (
	observeSnapshotBackend    = observeSnapshotUnsupported
	revalidateSnapshotBackend = revalidateSnapshotUnsupported
)
