package filecoord

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
)

var ErrConflict = errors.New("file coordination snapshot conflict")

type ConflictReason string

const (
	ConflictMissing  ConflictReason = "missing"
	ConflictTopology ConflictReason = "topology"
	ConflictIdentity ConflictReason = "identity"
	ConflictContent  ConflictReason = "content"
	ConflictMode     ConflictReason = "mode"
)

// ConflictError reports that a point-in-time snapshot no longer matches.
type ConflictError struct {
	Reason ConflictReason
	Cause  error
}

func (*ConflictError) Error() string { return ErrConflict.Error() }
func (e *ConflictError) Unwrap() []error {
	if e == nil || e.Cause == nil {
		return []error{ErrConflict}
	}
	return []error{ErrConflict, e.Cause}
}

// Snapshot is a point-in-time observation, not a pathname CAS, publication
// authority, or protection against changes after successful revalidation.
type Snapshot struct {
	path       string
	data       []byte
	mode       fs.FileMode
	attributes uint32
	identity   []byte
	valid      bool
}

// Observe validates a target lexically, then asks the platform backend for a
// point-in-time observation. It is not pathname CAS or publication authority.
func Observe(ctx context.Context, target string) (*Snapshot, error) {
	path, err := cleanTarget(target)
	if err != nil {
		return nil, err
	}
	return observeSnapshotBackend(ctx, path)
}

// Revalidate asks the platform backend to compare the same path and
// point-in-time identity. Successful revalidation does not protect against
// later changes and is not publication authority or pathname CAS.
func Revalidate(ctx context.Context, snapshot *Snapshot) error {
	if !validSnapshot(snapshot) {
		return &InvalidTargetError{}
	}
	return revalidateSnapshotBackend(ctx, snapshot)
}

func validSnapshot(snapshot *Snapshot) bool {
	if snapshot == nil || !snapshot.valid {
		return false
	}
	path, err := cleanTarget(snapshot.path)
	return err == nil && path == snapshot.path
}

// newSnapshot is the internal seam for platform backends and tests. Identity
// is deliberately not exposed through the public Snapshot API.
func newSnapshot(path string, data []byte, mode fs.FileMode, attributes uint32, identity []byte) (*Snapshot, error) {
	path, err := cleanTarget(path)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		path:       path,
		data:       append([]byte(nil), data...),
		mode:       mode,
		attributes: attributes,
		identity:   append([]byte(nil), identity...),
		valid:      true,
	}, nil
}

func (s *Snapshot) Path() string       { return s.path }
func (s *Snapshot) Bytes() []byte      { return append([]byte(nil), s.data...) }
func (s *Snapshot) Mode() fs.FileMode  { return s.mode }
func (s *Snapshot) Attributes() uint32 { return s.attributes }

// compareSnapshots is the exact comparison seam used by platform backends.
func compareSnapshots(want, got Snapshot) error {
	if want.path != got.path || want.mode.Type() != got.mode.Type() {
		return &ConflictError{Reason: ConflictTopology}
	}
	if !bytes.Equal(want.data, got.data) {
		return &ConflictError{Reason: ConflictContent}
	}
	if want.mode != got.mode || want.attributes != got.attributes {
		return &ConflictError{Reason: ConflictMode}
	}
	if !bytes.Equal(want.identity, got.identity) {
		return &ConflictError{Reason: ConflictIdentity}
	}
	return nil
}
