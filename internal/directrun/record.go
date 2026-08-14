package directrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const RecordSchema = "gentle-ai.direct-run-record/v1"

type RecordState string
type RecordOutcome string
type AbortReason string

const (
	RecordIssued     RecordState = "issued"
	RecordRegistered RecordState = "registered"
	RecordBound      RecordState = "bound"
	RecordConsumed   RecordState = "consumed"
	RecordFinished   RecordState = "finished"
	RecordAborted    RecordState = "aborted"

	OutcomeSucceeded RecordOutcome = "succeeded"
	OutcomeFailed    RecordOutcome = "failed"

	AbortCancelled AbortReason = "cancelled"
	AbortRevoked   AbortReason = "revoked"
	AbortExpired   AbortReason = "expired"
)

var (
	ErrInvalidTransition = errors.New("invalid direct-run record transition")
	ErrRevisionMismatch  = errors.New("direct-run record revision mismatch")
	ErrReplay            = errors.New("direct-run record is terminal")
	ErrCorruptRecord     = errors.New("direct-run record is corrupt")
)

// Record is immutable: transitions return a successor for the persistence layer to CAS.
type Record struct {
	Schema             string        `json:"schema"`
	Handoff            Handoff       `json:"handoff"`
	State              RecordState   `json:"state"`
	SessionID          string        `json:"session_id,omitempty"`
	ParentSessionID    string        `json:"parent_session_id,omitempty"`
	ParentCallID       string        `json:"parent_call_id,omitempty"`
	Agent              string        `json:"agent,omitempty"`
	RepositoryIdentity string        `json:"repository_identity,omitempty"`
	ExpiresAt          int64         `json:"expires_at,omitempty"`
	Outcome            RecordOutcome `json:"outcome,omitempty"`
	AbortReason        AbortReason   `json:"abort_reason,omitempty"`
	Output             *WorkerOutput `json:"output,omitempty"`
	Revision           Digest        `json:"revision"`
}

func IssueRecord(h Handoff) (Record, error) {
	if err := h.Validate(); err != nil {
		return Record{}, err
	}
	copy, err := copyHandoff(h)
	if err != nil {
		return Record{}, err
	}
	return sealRecord(Record{Schema: RecordSchema, Handoff: copy, State: RecordIssued})
}

func (r Record) Register(expected Digest, parentSessionID, parentCallID, agent, repositoryIdentity string, expiresAt, now int64) (Record, error) {
	if err := r.expected(expected); err != nil {
		return Record{}, err
	}
	if r.State != RecordIssued {
		return Record{}, transitionError(r.State)
	}
	if !recordIdentifier(parentSessionID) || !recordIdentifier(parentCallID) || !recordAgent(agent) || !recordIdentifier(repositoryIdentity) || expiresAt <= now || expiresAt-now > 300 {
		return Record{}, ErrInvalidTransition
	}
	r.State, r.ParentSessionID, r.ParentCallID, r.Agent, r.RepositoryIdentity, r.ExpiresAt = RecordRegistered, parentSessionID, parentCallID, agent, repositoryIdentity, expiresAt
	return sealRecord(r)
}

func (r Record) Bind(expected Digest, parentSessionID, parentCallID, agent, sessionID, repositoryIdentity string, now int64) (Record, error) {
	if err := r.expected(expected); err != nil {
		return Record{}, err
	}
	if r.State != RecordRegistered || r.ExpiresAt <= now || !recordIdentifier(sessionID) || parentSessionID != r.ParentSessionID || parentCallID != r.ParentCallID || agent != r.Agent || repositoryIdentity != r.RepositoryIdentity {
		return Record{}, transitionError(r.State)
	}
	r.State, r.SessionID = RecordBound, sessionID
	return sealRecord(r)
}

func (r Record) Consume(expected Digest, sessionID, repositoryIdentity string) (Record, error) {
	if err := r.expected(expected); err != nil {
		return Record{}, err
	}
	if r.State != RecordBound || sessionID != r.SessionID || repositoryIdentity != r.RepositoryIdentity {
		return Record{}, transitionError(r.State)
	}
	r.State = RecordConsumed
	return sealRecord(r)
}

func (r Record) Finish(expected Digest, outcome RecordOutcome, output WorkerOutput) (Record, error) {
	if err := r.expected(expected); err != nil {
		return Record{}, err
	}
	if r.State != RecordConsumed {
		return Record{}, transitionError(r.State)
	}
	if outcome != OutcomeSucceeded && outcome != OutcomeFailed {
		return Record{}, ErrInvalidTransition
	}
	if err := output.ValidateAgainst(r.Handoff); err != nil {
		return Record{}, ErrInvalidTransition
	}
	copy, err := copyOutput(output)
	if err != nil {
		return Record{}, ErrInvalidTransition
	}
	r.State, r.Outcome, r.Output = RecordFinished, outcome, &copy
	return sealRecord(r)
}

// Abort is allowed before completion, including cancellation after consumption; aborted records retain no output.
func (r Record) Abort(expected Digest, reason AbortReason) (Record, error) {
	if err := r.expected(expected); err != nil {
		return Record{}, err
	}
	if r.State != RecordIssued && r.State != RecordRegistered && r.State != RecordBound && r.State != RecordConsumed {
		return Record{}, transitionError(r.State)
	}
	if reason != AbortCancelled && reason != AbortRevoked && reason != AbortExpired {
		return Record{}, ErrInvalidTransition
	}
	r.State, r.AbortReason, r.Outcome, r.Output = RecordAborted, reason, "", nil
	return sealRecord(r)
}

func (r Record) CanonicalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

func DecodeRecord(payload []byte) (Record, error) {
	var r Record
	if len(payload) == 0 || duplicate(payload) {
		return r, ErrCorruptRecord
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return r, ErrCorruptRecord
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return r, ErrCorruptRecord
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || !recordFields(fields) || r.Validate() != nil {
		return r, ErrCorruptRecord
	}
	canonical, err := r.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, payload) {
		return r, ErrCorruptRecord
	}
	return r, nil
}

func recordFields(fields map[string]json.RawMessage) bool {
	for _, field := range []string{"schema", "handoff", "state", "revision"} {
		value, ok := fields[field]
		if !ok || bytes.Equal(value, []byte("null")) {
			return false
		}
	}
	return true
}

func (r Record) Validate() error {
	if err := r.validateContent(); err != nil {
		return err
	}
	want, err := recordRevision(r)
	if err != nil || r.Revision != want {
		return ErrCorruptRecord
	}
	return nil
}

func (r Record) validateContent() error {
	if r.Schema != RecordSchema || r.Handoff.Validate() != nil {
		return ErrCorruptRecord
	}
	registered := recordIdentifier(r.ParentSessionID) && recordIdentifier(r.ParentCallID) && recordAgent(r.Agent) && recordIdentifier(r.RepositoryIdentity) && r.ExpiresAt > 0
	bound := registered && recordIdentifier(r.SessionID)
	switch r.State {
	case RecordIssued:
		if r.SessionID != "" || r.ParentSessionID != "" || r.ParentCallID != "" || r.Agent != "" || r.RepositoryIdentity != "" || r.ExpiresAt != 0 || r.Outcome != "" || r.AbortReason != "" || r.Output != nil {
			return ErrCorruptRecord
		}
	case RecordRegistered:
		if !registered || r.SessionID != "" || r.Outcome != "" || r.AbortReason != "" || r.Output != nil {
			return ErrCorruptRecord
		}
	case RecordBound, RecordConsumed:
		if !bound || r.Outcome != "" || r.AbortReason != "" || r.Output != nil {
			return ErrCorruptRecord
		}
	case RecordFinished:
		if !bound || (r.Outcome != OutcomeSucceeded && r.Outcome != OutcomeFailed) || r.AbortReason != "" || r.Output == nil || r.Output.ValidateAgainst(r.Handoff) != nil {
			return ErrCorruptRecord
		}
	case RecordAborted:
		if r.Outcome != "" || r.Output != nil || (r.AbortReason != AbortCancelled && r.AbortReason != AbortRevoked && r.AbortReason != AbortExpired) || (r.ParentSessionID != "" && !registered) {
			return ErrCorruptRecord
		}
	default:
		return ErrCorruptRecord
	}
	return nil
}

func (r Record) expected(expected Digest) error {
	if err := r.Validate(); err != nil {
		return ErrCorruptRecord
	}
	if expected != r.Revision {
		return ErrRevisionMismatch
	}
	return nil
}
func transitionError(state RecordState) error {
	if state == RecordFinished || state == RecordAborted {
		return ErrReplay
	}
	return ErrInvalidTransition
}
func sealRecord(r Record) (Record, error) {
	if err := r.validateContent(); err != nil {
		return Record{}, err
	}
	revision, err := recordRevision(r)
	if err != nil {
		return Record{}, err
	}
	r.Revision = revision
	return r, nil
}
func recordRevision(r Record) (Digest, error) {
	r.Revision = ""
	payload, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return Digest(fmt.Sprintf("sha256:%x", sum)), nil
}
func recordIdentifier(value string) bool {
	return len(value) > 0 && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n/\\")
}
func recordAgent(value string) bool {
	if value == WorkerRole || value == "gentle-reviewer" {
		return true
	}
	for _, prefix := range []string{WorkerRole + "-", "gentle-reviewer-"} {
		if strings.HasPrefix(value, prefix) && recordIdentifier(strings.TrimPrefix(value, prefix)) {
			return true
		}
	}
	return false
}
func copyHandoff(h Handoff) (Handoff, error) {
	payload, err := h.CanonicalJSON()
	if err != nil {
		return Handoff{}, err
	}
	return DecodeHandoff(payload)
}
func copyOutput(o WorkerOutput) (WorkerOutput, error) {
	payload, err := o.CanonicalJSON()
	if err != nil {
		return WorkerOutput{}, err
	}
	return DecodeWorkerOutput(payload)
}
