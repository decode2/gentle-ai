package directrun

import (
	"context"
	"errors"
)

const maxRecordBytes = 1 << 20 // Records include bounded handoff and worker-output evidence.
var (
	ErrNotFound           = errors.New("direct-run record not found")
	ErrAlreadyExists      = errors.New("direct-run record already exists")
	ErrCASConflict        = errors.New("direct-run record conflict")
	ErrRecordTooLarge     = errors.New("direct-run record too large")
	ErrIdentityChanged    = errors.New("direct-run identity changed")
	ErrBackendUnavailable = errors.New("direct-run backend unavailable")
)

type IdentityLease interface {
	Validate(context.Context) error
	StorageKey() string
}
type RecordKey struct{ Repository, Record Digest }
type RecordBackend interface {
	Read(context.Context, RecordKey) ([]byte, error)
	Create(context.Context, RecordKey, []byte) error
	CompareAndSwap(context.Context, RecordKey, Digest, []byte) error
}
type Store struct {
	backend RecordBackend
	lease   IdentityLease
	key     Digest
}

func NewStore(backend RecordBackend, lease IdentityLease) (*Store, error) {
	if backend == nil || lease == nil || lease.StorageKey() == "" {
		return nil, ErrIdentityChanged
	}
	return &Store{backend: backend, lease: lease, key: digest("gentle-ai.direct-run-store/v1", []byte(lease.StorageKey()))}, nil
}
func (s *Store) Issue(ctx context.Context, handoff Handoff) (Record, error) {
	record, err := IssueRecord(handoff)
	if err != nil {
		return Record{}, err
	}
	if err := s.valid(ctx); err != nil {
		return Record{}, err
	}
	bytes, err := recordBytes(record)
	if err != nil {
		return Record{}, err
	}
	if err := s.valid(ctx); err != nil {
		return Record{}, err
	}
	if err := backendError(s.backend.Create(ctx, s.recordKey(handoff.Identity), bytes)); err == nil {
		return record, nil
	} else if !errors.Is(err, ErrAlreadyExists) {
		return Record{}, err
	}
	existing, err := s.Read(ctx, handoff.Identity)
	if err != nil {
		return Record{}, err
	}
	existingHandoff, _ := existing.Handoff.CanonicalJSON()
	incomingHandoff, _ := handoff.CanonicalJSON()
	if string(existingHandoff) != string(incomingHandoff) {
		return Record{}, ErrAlreadyExists
	}
	return existing, nil
}
func (s *Store) Read(ctx context.Context, identity string) (Record, error) {
	if err := s.valid(ctx); err != nil {
		return Record{}, err
	}
	bytes, err := s.backend.Read(ctx, s.recordKey(identity))
	return s.decode(identity, bytes, err)
}
func (s *Store) Register(ctx context.Context, identity string, expected Digest, parentSessionID, parentCallID, agent, repositoryIdentity string, expiresAt, now int64) (Record, error) {
	return s.mutate(ctx, identity, expected, func(r Record) (Record, error) {
		return r.Register(expected, parentSessionID, parentCallID, agent, repositoryIdentity, expiresAt, now)
	})
}
func (s *Store) Bind(ctx context.Context, identity string, expected Digest, parentSessionID, parentCallID, agent, sessionID, repositoryIdentity string, now int64) (Record, error) {
	return s.mutate(ctx, identity, expected, func(r Record) (Record, error) {
		return r.Bind(expected, parentSessionID, parentCallID, agent, sessionID, repositoryIdentity, now)
	})
}
func (s *Store) Consume(ctx context.Context, identity string, expected Digest, sessionID, repositoryIdentity string) (Record, error) {
	return s.mutate(ctx, identity, expected, func(r Record) (Record, error) { return r.Consume(expected, sessionID, repositoryIdentity) })
}
func (s *Store) Finish(ctx context.Context, identity string, expected Digest, outcome RecordOutcome, output WorkerOutput) (Record, error) {
	return s.mutate(ctx, identity, expected, func(r Record) (Record, error) { return r.Finish(expected, outcome, output) })
}
func (s *Store) Abort(ctx context.Context, identity string, expected Digest, reason AbortReason) (Record, error) {
	return s.mutate(ctx, identity, expected, func(r Record) (Record, error) { return r.Abort(expected, reason) })
}
func (s *Store) mutate(ctx context.Context, identity string, expected Digest, transition func(Record) (Record, error)) (Record, error) {
	record, err := s.Read(ctx, identity)
	if err != nil {
		return Record{}, err
	}
	next, err := transition(record)
	if err != nil {
		return Record{}, err
	}
	bytes, err := recordBytes(next)
	if err != nil {
		return Record{}, err
	}
	if err := s.valid(ctx); err != nil {
		return Record{}, err
	}
	if err := backendError(s.backend.CompareAndSwap(ctx, s.recordKey(identity), expected, bytes)); err != nil {
		return Record{}, err
	}
	return next, nil
}
func (s *Store) decode(identity string, bytes []byte, backendErr error) (Record, error) {
	if err := backendError(backendErr); err != nil {
		return Record{}, err
	}
	if len(bytes) > maxRecordBytes {
		return Record{}, ErrRecordTooLarge
	}
	record, err := DecodeRecord(bytes)
	if err != nil {
		return Record{}, ErrCorruptRecord
	}
	if record.Handoff.Identity != identity {
		return Record{}, ErrNotFound
	}
	return record, nil
}
func (s *Store) valid(ctx context.Context) error {
	if s.lease.Validate(ctx) != nil || s.lease.StorageKey() == "" || digest("gentle-ai.direct-run-store/v1", []byte(s.lease.StorageKey())) != s.key {
		return ErrIdentityChanged
	}
	return nil
}
func (s *Store) recordKey(identity string) RecordKey {
	return RecordKey{s.key, digest("gentle-ai.direct-run-record-key/v1", []byte(identity))}
}
func recordBytes(record Record) ([]byte, error) {
	bytes, err := record.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	if len(bytes) > maxRecordBytes {
		return nil, ErrRecordTooLarge
	}
	return bytes, nil
}
func backendError(err error) error {
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrAlreadyExists) || errors.Is(err, ErrCASConflict) {
		return err
	}
	return ErrBackendUnavailable
}
