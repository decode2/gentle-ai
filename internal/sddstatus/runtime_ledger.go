package sddstatus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	RuntimeStatusSchema        = "gentle-ai.sdd-runtime-status/v1"
	runtimeRecordSchema        = "gentle-ai.sdd-runtime-record/v1"
	runtimeObjectiveSchema     = "gentle-ai.sdd-runtime-objective/v1"
	DefaultRuntimeAttemptLimit = 2
	DefaultRuntimeChangedLines = 200
	maximumRuntimeAttemptLimit = 100
	maximumRuntimeChangedLines = 1_000_000
	maximumRuntimeRecordBytes  = 1 << 20
	maximumRuntimeChainRecords = 10_000
	RuntimeActionBegin         = "begin"
	RuntimeActionFinish        = "finish"
	RuntimeActionReset         = "reset"
	RuntimeActionComplete      = "complete"
	runtimeOperationBegin      = "attempt/begin"
	runtimeOperationFinish     = "attempt/finish"
)

var (
	ErrRuntimeRevisionConflict = errors.New("SDD runtime ledger revision conflict")
	ErrRuntimeConcurrentUpdate = errors.New("SDD runtime ledger is concurrently updated")
	ErrRuntimeRequestConflict  = errors.New("SDD runtime request identifier was reused with different inputs")
	ErrRuntimeBudgetExhausted  = errors.New("SDD runtime objective budget is exhausted")
	ErrRuntimeAttemptActive    = errors.New("SDD runtime objective already has an active attempt")
	ErrRuntimeNoActiveAttempt  = errors.New("SDD runtime objective has no active attempt")
	ErrRuntimeObjectiveChange  = errors.New("SDD runtime objective changed without an explicit reset")
	ErrRuntimeObjectiveDone    = errors.New("SDD runtime objective is complete")

	runtimeRequestIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	runtimeRevisionPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	runtimeGitTreePattern   = regexp.MustCompile(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`)

	runtimePublishRecord = reviewtransaction.PublishFileNoReplace
	runtimeReplaceHead   = reviewtransaction.ReplaceFileAtomic
	runtimeSyncDirectory = reviewtransaction.SyncReviewDirectory
)

// RuntimeRevisionConflictError is a deterministic pre-publication CAS denial.
type RuntimeRevisionConflictError struct {
	Expected string
	Current  string
}

func (err *RuntimeRevisionConflictError) Error() string {
	return fmt.Sprintf("%v: expected %q, current %q", ErrRuntimeRevisionConflict, err.Expected, err.Current)
}

func (err *RuntimeRevisionConflictError) Unwrap() error { return ErrRuntimeRevisionConflict }

// RuntimePublicationError reports that HEAD was atomically replaced but its
// directory durability could not be confirmed. The exact request is safe to
// replay; replay reopens the immutable chain and repeats directory fsync.
type RuntimePublicationError struct {
	Revision  string
	Committed bool
	Cause     error
}

func (err *RuntimePublicationError) Error() string {
	return fmt.Sprintf("SDD runtime ledger publication for %s requires exact replay: %v", err.Revision, err.Cause)
}

func (err *RuntimePublicationError) Unwrap() error { return err.Cause }

type AttemptOutcome string

const (
	AttemptRunning     AttemptOutcome = "running"
	AttemptFailed      AttemptOutcome = "failed"
	AttemptInterrupted AttemptOutcome = "interrupted"
	AttemptPassed      AttemptOutcome = "passed"
)

type HarnessDisposition string

const (
	HarnessReused      HarnessDisposition = "reused"
	HarnessInvalidated HarnessDisposition = "invalidated"
)

type RuntimeObjective struct {
	ID                       string `json:"id"`
	WorkUnit                 string `json:"work_unit"`
	EvidenceGoal             string `json:"evidence_goal"`
	InitialCandidateIdentity string `json:"initial_candidate_identity"`
	InitialCandidateTree     string `json:"initial_candidate_tree"`
	MaxAttempts              int    `json:"max_attempts"`
	MaxChangedLines          int    `json:"max_changed_lines"`
}

type RuntimeAttempt struct {
	Ordinal                 int                `json:"ordinal"`
	WorkUnit                string             `json:"work_unit"`
	BeginCandidateIdentity  string             `json:"begin_candidate_identity"`
	BeginCandidateTree      string             `json:"begin_candidate_tree"`
	FinishCandidateIdentity string             `json:"finish_candidate_identity,omitempty"`
	FinishCandidateTree     string             `json:"finish_candidate_tree,omitempty"`
	Outcome                 AttemptOutcome     `json:"outcome"`
	ChangedLines            int                `json:"changed_lines"`
	EvidenceRevision        string             `json:"evidence_revision,omitempty"`
	Diagnosis               string             `json:"diagnosis,omitempty"`
	HarnessDisposition      HarnessDisposition `json:"harness_disposition,omitempty"`
	CleanupEvidence         string             `json:"cleanup_evidence,omitempty"`
	ProcessEvidence         string             `json:"process_evidence,omitempty"`
}

type RuntimeStatus struct {
	Schema                 string            `json:"schema"`
	Change                 string            `json:"change"`
	Revision               string            `json:"revision"`
	Objective              *RuntimeObjective `json:"objective,omitempty"`
	ActiveAttempt          *RuntimeAttempt   `json:"active_attempt,omitempty"`
	Attempts               []RuntimeAttempt  `json:"attempts"`
	NextOrdinal            int               `json:"next_ordinal"`
	CumulativeAttempts     int               `json:"cumulative_attempts"`
	CumulativeChangedLines int               `json:"cumulative_changed_lines"`
	EvidenceRevision       string            `json:"evidence_revision"`
	DecisionRequired       bool              `json:"decision_required"`
	Complete               bool              `json:"complete"`
	NextAction             string            `json:"next_action"`
	BindingRevision        string            `json:"binding_revision"`
	Binding                *ReviewBinding    `json:"binding,omitempty"`
}

type BeginAttemptRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	WorkUnit         string `json:"work_unit"`
	EvidenceGoal     string `json:"evidence_goal"`
	MaxAttempts      int    `json:"max_attempts"`
	MaxChangedLines  int    `json:"max_changed_lines"`
}

type FinishAttemptRequest struct {
	ExpectedRevision   string             `json:"expected_revision"`
	RequestID          string             `json:"request_id"`
	Outcome            AttemptOutcome     `json:"outcome"`
	EvidenceRevision   string             `json:"evidence_revision"`
	Diagnosis          string             `json:"diagnosis"`
	HarnessDisposition HarnessDisposition `json:"harness_disposition"`
	CleanupEvidence    string             `json:"cleanup_evidence"`
	ProcessEvidence    string             `json:"process_evidence"`
}

// RuntimeStore is one provider-owned immutable chain for one SDD change. Its
// directory is rooted in the repository Git common-dir, so linked worktrees
// and later processes observe the same attempt ordinals and line charges.
type RuntimeStore struct {
	Dir       string
	Repo      string
	Change    string
	commonDir string
}

type runtimeRecord struct {
	Schema           string               `json:"schema"`
	Change           string               `json:"change"`
	PreviousRevision string               `json:"previous_revision"`
	Operation        string               `json:"operation"`
	RequestID        string               `json:"request_id"`
	RequestDigest    string               `json:"request_digest"`
	Begin            *runtimeBeginEvent   `json:"begin,omitempty"`
	Finish           *runtimeFinishEvent  `json:"finish,omitempty"`
	Binding          *ReviewBinding       `json:"binding,omitempty"`
	runtimeReserved  *runtimeReservedData `json:"reserved,omitempty"`
}

// runtimeReservedData intentionally keeps the v1 record decoder extensible
// only through a provider release. Unknown caller-authored fields still fail.
type runtimeReservedData struct{}

type runtimeBeginEvent struct {
	ObjectiveID            string `json:"objective_id"`
	WorkUnit               string `json:"work_unit"`
	EvidenceGoal           string `json:"evidence_goal"`
	MaxAttempts            int    `json:"max_attempts"`
	MaxChangedLines        int    `json:"max_changed_lines"`
	Ordinal                int    `json:"ordinal"`
	BeginCandidateIdentity string `json:"begin_candidate_identity"`
	BeginCandidateTree     string `json:"begin_candidate_tree"`
}

type runtimeFinishEvent struct {
	Ordinal                 int                `json:"ordinal"`
	FinishCandidateIdentity string             `json:"finish_candidate_identity"`
	FinishCandidateTree     string             `json:"finish_candidate_tree"`
	Outcome                 AttemptOutcome     `json:"outcome"`
	ChangedLines            int                `json:"changed_lines"`
	EvidenceRevision        string             `json:"evidence_revision"`
	Diagnosis               string             `json:"diagnosis"`
	HarnessDisposition      HarnessDisposition `json:"harness_disposition"`
	CleanupEvidence         string             `json:"cleanup_evidence"`
	ProcessEvidence         string             `json:"process_evidence"`
}

type runtimeRequestReceipt struct {
	Digest   string
	Revision string
}

type runtimeReplay struct {
	Status   RuntimeStatus
	Requests map[string]runtimeRequestReceipt
}

func OpenRuntimeStore(ctx context.Context, repo, change string) (RuntimeStore, error) {
	if !validReviewBindingChange(change) {
		return RuntimeStore{}, errors.New("invalid SDD change name")
	}
	root, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).ResolveRepositoryRoot(ctx)
	if err != nil {
		return RuntimeStore{}, err
	}
	probe, err := reviewtransaction.CompactAuthoritativeStore(ctx, root, "sdd-runtime-probe")
	if err != nil {
		return RuntimeStore{}, err
	}
	commonDir := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(probe.Dir))))
	dir := filepath.Join(commonDir, "gentle-ai", "sdd-runtime", "v1", change)
	return RuntimeStore{Dir: dir, Repo: root, Change: change, commonDir: commonDir}, nil
}

func (store RuntimeStore) Status() (RuntimeStatus, error) {
	replay, err := store.load()
	return replay.Status, err
}

func (store RuntimeStore) Begin(ctx context.Context, request BeginAttemptRequest) (RuntimeStatus, error) {
	request, err := normalizeBeginAttemptRequest(request)
	if err != nil {
		return RuntimeStatus{}, err
	}
	digest := runtimeValueHash("gentle-ai.sdd-runtime-begin-request/v1", request)
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay runtimeReplay) (runtimeRecord, error) {
		status := replay.Status
		if status.ActiveAttempt != nil {
			return runtimeRecord{}, ErrRuntimeAttemptActive
		}
		if status.Complete {
			return runtimeRecord{}, ErrRuntimeObjectiveDone
		}
		if status.DecisionRequired {
			return runtimeRecord{}, ErrRuntimeBudgetExhausted
		}

		snapshot, err := captureRuntimeCandidate(ctx, store.Repo)
		if err != nil {
			return runtimeRecord{}, fmt.Errorf("capture SDD runtime candidate before launch: %w", err)
		}
		objectiveID := runtimeObjectiveID(store.Change, request.EvidenceGoal)
		if status.Objective != nil {
			objectiveID = status.Objective.ID
			if request.EvidenceGoal != status.Objective.EvidenceGoal || request.MaxAttempts != status.Objective.MaxAttempts ||
				request.MaxChangedLines != status.Objective.MaxChangedLines {
				return runtimeRecord{}, ErrRuntimeObjectiveChange
			}
		}
		if status.CumulativeAttempts >= request.MaxAttempts || status.CumulativeChangedLines >= request.MaxChangedLines {
			return runtimeRecord{}, ErrRuntimeBudgetExhausted
		}
		event := &runtimeBeginEvent{
			ObjectiveID: objectiveID, WorkUnit: request.WorkUnit, EvidenceGoal: request.EvidenceGoal,
			MaxAttempts: request.MaxAttempts, MaxChangedLines: request.MaxChangedLines,
			Ordinal: status.NextOrdinal, BeginCandidateIdentity: snapshot.Identity, BeginCandidateTree: snapshot.CandidateTree,
		}
		return runtimeRecord{Operation: runtimeOperationBegin, Begin: event}, nil
	})
}

func (store RuntimeStore) Finish(ctx context.Context, request FinishAttemptRequest) (RuntimeStatus, error) {
	request, err := normalizeFinishAttemptRequest(request)
	if err != nil {
		return RuntimeStatus{}, err
	}
	digest := runtimeValueHash("gentle-ai.sdd-runtime-finish-request/v1", request)
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay runtimeReplay) (runtimeRecord, error) {
		active := replay.Status.ActiveAttempt
		if active == nil {
			return runtimeRecord{}, ErrRuntimeNoActiveAttempt
		}
		intended, err := (reviewtransaction.SnapshotBuilder{Repo: store.Repo}).DiscoverIntendedUntracked(ctx)
		if err != nil {
			return runtimeRecord{}, fmt.Errorf("discover SDD runtime intended-untracked paths: %w", err)
		}
		snapshot, err := (reviewtransaction.SnapshotBuilder{Repo: store.Repo}).Build(ctx, reviewtransaction.Target{
			Kind: reviewtransaction.TargetBaseWorkspaceOverlay, BaseRef: active.BeginCandidateTree,
			Projection: reviewtransaction.ProjectionWorkspace, IntendedUntracked: intended,
		})
		if err != nil {
			return runtimeRecord{}, fmt.Errorf("capture SDD runtime candidate after attempt: %w", err)
		}
		changedLines, err := (reviewtransaction.SnapshotBuilder{Repo: store.Repo}).ChangedLines(ctx, snapshot)
		if err != nil {
			return runtimeRecord{}, fmt.Errorf("measure native SDD runtime line charge: %w", err)
		}
		event := &runtimeFinishEvent{
			Ordinal: active.Ordinal, FinishCandidateIdentity: snapshot.Identity, FinishCandidateTree: snapshot.CandidateTree,
			Outcome: request.Outcome, ChangedLines: changedLines, EvidenceRevision: request.EvidenceRevision,
			Diagnosis: request.Diagnosis, HarnessDisposition: request.HarnessDisposition,
			CleanupEvidence: request.CleanupEvidence, ProcessEvidence: request.ProcessEvidence,
		}
		return runtimeRecord{Operation: runtimeOperationFinish, Finish: event}, nil
	})
}

func (store RuntimeStore) mutate(
	ctx context.Context,
	expected, requestID, requestDigest string,
	build func(runtimeReplay) (runtimeRecord, error),
) (RuntimeStatus, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeStatus{}, err
	}
	if err := store.ensureDirectories(); err != nil {
		return RuntimeStatus{}, err
	}
	lock, err := reviewtransaction.AcquireAuthorityFileLock(filepath.Join(store.Dir, "LOCK"))
	if err != nil {
		if errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
			return RuntimeStatus{}, fmt.Errorf("%w: %v", ErrRuntimeConcurrentUpdate, err)
		}
		return RuntimeStatus{}, err
	}
	defer lock.Release()

	replay, err := store.load()
	if err != nil {
		return RuntimeStatus{}, err
	}
	if receipt, ok := replay.Requests[requestID]; ok {
		if receipt.Digest != requestDigest {
			return RuntimeStatus{}, ErrRuntimeRequestConflict
		}
		if err := store.syncReplay(); err != nil {
			return RuntimeStatus{}, &RuntimePublicationError{Revision: receipt.Revision, Committed: true, Cause: err}
		}
		return replay.Status, nil
	}
	if replay.Status.Revision != expected {
		return RuntimeStatus{}, &RuntimeRevisionConflictError{Expected: expected, Current: replay.Status.Revision}
	}
	record, err := build(replay)
	if err != nil {
		return RuntimeStatus{}, err
	}
	record.Schema = runtimeRecordSchema
	record.Change = store.Change
	record.PreviousRevision = expected
	record.RequestID = requestID
	record.RequestDigest = requestDigest
	if err := validateRuntimeRecordShape(record); err != nil {
		return RuntimeStatus{}, err
	}

	revision, payload, err := runtimeRecordRevision(record)
	if err != nil {
		return RuntimeStatus{}, err
	}
	if err := store.publishRecord(revision, payload); err != nil {
		return RuntimeStatus{}, err
	}
	if err := store.publishHead(revision); err != nil {
		return RuntimeStatus{}, err
	}
	if err := runtimeSyncDirectory(store.Dir); err != nil {
		return RuntimeStatus{}, &RuntimePublicationError{Revision: revision, Committed: true, Cause: fmt.Errorf("sync SDD runtime HEAD directory: %w", err)}
	}

	committed, err := store.load()
	if err != nil {
		return RuntimeStatus{}, &RuntimePublicationError{Revision: revision, Committed: true, Cause: fmt.Errorf("replay committed SDD runtime HEAD: %w", err)}
	}
	if committed.Status.Revision != revision {
		return RuntimeStatus{}, &RuntimePublicationError{Revision: revision, Committed: true, Cause: errors.New("committed SDD runtime HEAD did not replay to candidate revision")}
	}
	return committed.Status, nil
}

func (store RuntimeStore) load() (runtimeReplay, error) {
	replay := runtimeReplay{
		Status: RuntimeStatus{
			Schema: RuntimeStatusSchema, Change: store.Change, Attempts: []RuntimeAttempt{},
			NextOrdinal: 1, NextAction: RuntimeActionBegin,
		},
		Requests: map[string]runtimeRequestReceipt{},
	}
	head, exists, err := readRuntimeHead(filepath.Join(store.Dir, "HEAD"))
	if err != nil || !exists {
		return replay, err
	}

	type revisionRecord struct {
		revision string
		record   runtimeRecord
	}
	reverse := make([]revisionRecord, 0, 16)
	seen := map[string]struct{}{}
	for revision := head; revision != ""; {
		if len(reverse) >= maximumRuntimeChainRecords {
			return runtimeReplay{}, errors.New("SDD runtime chain exceeds the bounded record count")
		}
		if _, duplicate := seen[revision]; duplicate {
			return runtimeReplay{}, errors.New("SDD runtime record predecessor cycle detected")
		}
		seen[revision] = struct{}{}
		record, err := store.loadRecord(revision)
		if err != nil {
			return runtimeReplay{}, err
		}
		reverse = append(reverse, revisionRecord{revision: revision, record: record})
		revision = record.PreviousRevision
	}
	for index := len(reverse) - 1; index >= 0; index-- {
		entry := reverse[index]
		if err := applyRuntimeRecord(&replay, entry.revision, entry.record); err != nil {
			return runtimeReplay{}, fmt.Errorf("replay SDD runtime revision %s: %w", entry.revision, err)
		}
	}
	if replay.Status.Revision != head {
		return runtimeReplay{}, errors.New("SDD runtime HEAD does not equal replayed revision")
	}
	return replay, nil
}

func applyRuntimeRecord(replay *runtimeReplay, revision string, record runtimeRecord) error {
	if record.PreviousRevision != replay.Status.Revision {
		return errors.New("record predecessor does not equal replay state")
	}
	if _, duplicate := replay.Requests[record.RequestID]; duplicate {
		return errors.New("duplicate runtime request identifier")
	}
	if err := validateRuntimeRecordShape(record); err != nil {
		return err
	}
	switch record.Operation {
	case runtimeOperationBegin:
		event := record.Begin
		if replay.Status.ActiveAttempt != nil || replay.Status.Complete || replay.Status.DecisionRequired {
			return errors.New("begin record is not a valid successor")
		}
		if replay.Status.Objective == nil {
			if event.Ordinal != 1 || event.ObjectiveID != runtimeObjectiveID(record.Change, event.EvidenceGoal) {
				return errors.New("initial objective identity or ordinal is invalid")
			}
			replay.Status.Objective = &RuntimeObjective{
				ID: event.ObjectiveID, WorkUnit: event.WorkUnit, EvidenceGoal: event.EvidenceGoal,
				InitialCandidateIdentity: event.BeginCandidateIdentity, InitialCandidateTree: event.BeginCandidateTree,
				MaxAttempts: event.MaxAttempts, MaxChangedLines: event.MaxChangedLines,
			}
		} else {
			objective := replay.Status.Objective
			if event.ObjectiveID != objective.ID || event.EvidenceGoal != objective.EvidenceGoal ||
				event.MaxAttempts != objective.MaxAttempts || event.MaxChangedLines != objective.MaxChangedLines ||
				event.Ordinal != replay.Status.NextOrdinal {
				return errors.New("begin record changes the active objective or ordinal")
			}
			objective.WorkUnit = event.WorkUnit
		}
		if replay.Status.CumulativeAttempts >= event.MaxAttempts || replay.Status.CumulativeChangedLines >= event.MaxChangedLines {
			return errors.New("begin record exceeds the persisted objective budget")
		}
		attempt := RuntimeAttempt{
			Ordinal: event.Ordinal, WorkUnit: event.WorkUnit, BeginCandidateIdentity: event.BeginCandidateIdentity,
			BeginCandidateTree: event.BeginCandidateTree, Outcome: AttemptRunning,
		}
		replay.Status.Attempts = append(replay.Status.Attempts, attempt)
		active := attempt
		replay.Status.ActiveAttempt = &active
		replay.Status.CumulativeAttempts++
		replay.Status.NextOrdinal = event.Ordinal + 1
		replay.Status.NextAction = RuntimeActionFinish

	case runtimeOperationFinish:
		event := record.Finish
		active := replay.Status.ActiveAttempt
		if active == nil || active.Ordinal != event.Ordinal || len(replay.Status.Attempts) == 0 ||
			replay.Status.Attempts[len(replay.Status.Attempts)-1].Outcome != AttemptRunning {
			return errors.New("finish record does not match the active attempt")
		}
		attempt := &replay.Status.Attempts[len(replay.Status.Attempts)-1]
		attempt.FinishCandidateIdentity = event.FinishCandidateIdentity
		attempt.FinishCandidateTree = event.FinishCandidateTree
		attempt.Outcome = event.Outcome
		attempt.ChangedLines = event.ChangedLines
		attempt.EvidenceRevision = event.EvidenceRevision
		attempt.Diagnosis = event.Diagnosis
		attempt.HarnessDisposition = event.HarnessDisposition
		attempt.CleanupEvidence = event.CleanupEvidence
		attempt.ProcessEvidence = event.ProcessEvidence
		replay.Status.ActiveAttempt = nil
		replay.Status.CumulativeChangedLines += event.ChangedLines
		replay.Status.EvidenceRevision = event.EvidenceRevision
		if event.Outcome == AttemptPassed {
			replay.Status.Complete = true
			replay.Status.NextAction = RuntimeActionComplete
		} else if replay.Status.CumulativeAttempts >= replay.Status.Objective.MaxAttempts ||
			replay.Status.CumulativeChangedLines >= replay.Status.Objective.MaxChangedLines {
			replay.Status.DecisionRequired = true
			replay.Status.NextAction = RuntimeActionReset
		} else {
			replay.Status.NextAction = RuntimeActionBegin
		}
	default:
		return errors.New("unsupported SDD runtime record operation")
	}
	replay.Status.Revision = revision
	replay.Requests[record.RequestID] = runtimeRequestReceipt{Digest: record.RequestDigest, Revision: revision}
	return nil
}

func validateRuntimeRecordShape(record runtimeRecord) error {
	if record.Schema != runtimeRecordSchema || !validReviewBindingChange(record.Change) ||
		(record.PreviousRevision != "" && !runtimeRevisionPattern.MatchString(record.PreviousRevision)) ||
		!runtimeRequestIDPattern.MatchString(record.RequestID) || !runtimeRevisionPattern.MatchString(record.RequestDigest) {
		return errors.New("invalid SDD runtime record identity")
	}
	switch record.Operation {
	case runtimeOperationBegin:
		if record.Begin == nil || record.Finish != nil || record.Binding != nil || record.runtimeReserved != nil {
			return errors.New("invalid SDD runtime begin record shape")
		}
		event := record.Begin
		if !runtimeRevisionPattern.MatchString(event.ObjectiveID) || validateRuntimeText(event.WorkUnit, 160) != nil ||
			validateRuntimeText(event.EvidenceGoal, 240) != nil || event.MaxAttempts < 1 || event.MaxAttempts > maximumRuntimeAttemptLimit ||
			event.MaxChangedLines < 1 || event.MaxChangedLines > maximumRuntimeChangedLines || event.Ordinal < 1 ||
			!runtimeRevisionPattern.MatchString(event.BeginCandidateIdentity) || !runtimeGitTreePattern.MatchString(event.BeginCandidateTree) {
			return errors.New("invalid SDD runtime begin event")
		}
		request := BeginAttemptRequest{
			ExpectedRevision: record.PreviousRevision, RequestID: record.RequestID, WorkUnit: event.WorkUnit,
			EvidenceGoal: event.EvidenceGoal, MaxAttempts: event.MaxAttempts, MaxChangedLines: event.MaxChangedLines,
		}
		if runtimeValueHash("gentle-ai.sdd-runtime-begin-request/v1", request) != record.RequestDigest {
			return errors.New("SDD runtime begin request digest does not match record")
		}
	case runtimeOperationFinish:
		if record.Finish == nil || record.Begin != nil || record.Binding != nil || record.runtimeReserved != nil {
			return errors.New("invalid SDD runtime finish record shape")
		}
		event := record.Finish
		if event.Ordinal < 1 || !validTerminalAttemptOutcome(event.Outcome) || event.ChangedLines < 0 ||
			event.ChangedLines > maximumRuntimeChangedLines || !runtimeRevisionPattern.MatchString(event.EvidenceRevision) ||
			!runtimeRevisionPattern.MatchString(event.FinishCandidateIdentity) || !runtimeGitTreePattern.MatchString(event.FinishCandidateTree) ||
			validateRuntimeText(event.Diagnosis, 500) != nil || !validHarnessDisposition(event.HarnessDisposition) ||
			validateRuntimeText(event.CleanupEvidence, 500) != nil || validateRuntimeText(event.ProcessEvidence, 500) != nil {
			return errors.New("invalid SDD runtime finish event")
		}
		request := FinishAttemptRequest{
			ExpectedRevision: record.PreviousRevision, RequestID: record.RequestID, Outcome: event.Outcome,
			EvidenceRevision: event.EvidenceRevision, Diagnosis: event.Diagnosis, HarnessDisposition: event.HarnessDisposition,
			CleanupEvidence: event.CleanupEvidence, ProcessEvidence: event.ProcessEvidence,
		}
		if runtimeValueHash("gentle-ai.sdd-runtime-finish-request/v1", request) != record.RequestDigest {
			return errors.New("SDD runtime finish request digest does not match record")
		}
	default:
		return errors.New("invalid SDD runtime record operation")
	}
	return nil
}

func normalizeBeginAttemptRequest(request BeginAttemptRequest) (BeginAttemptRequest, error) {
	if request.ExpectedRevision != "" && !runtimeRevisionPattern.MatchString(request.ExpectedRevision) {
		return BeginAttemptRequest{}, errors.New("expected runtime revision must be empty or sha256")
	}
	if !runtimeRequestIDPattern.MatchString(request.RequestID) {
		return BeginAttemptRequest{}, errors.New("request_id must be a canonical lowercase identifier")
	}
	if err := validateRuntimeText(request.WorkUnit, 160); err != nil {
		return BeginAttemptRequest{}, fmt.Errorf("invalid work_unit: %w", err)
	}
	if err := validateRuntimeText(request.EvidenceGoal, 240); err != nil {
		return BeginAttemptRequest{}, fmt.Errorf("invalid evidence_goal: %w", err)
	}
	if request.MaxAttempts == 0 {
		request.MaxAttempts = DefaultRuntimeAttemptLimit
	}
	if request.MaxChangedLines == 0 {
		request.MaxChangedLines = DefaultRuntimeChangedLines
	}
	if request.MaxAttempts < 1 || request.MaxAttempts > maximumRuntimeAttemptLimit {
		return BeginAttemptRequest{}, fmt.Errorf("max_attempts must be within 1..%d", maximumRuntimeAttemptLimit)
	}
	if request.MaxChangedLines < 1 || request.MaxChangedLines > maximumRuntimeChangedLines {
		return BeginAttemptRequest{}, fmt.Errorf("max_changed_lines must be within 1..%d", maximumRuntimeChangedLines)
	}
	return request, nil
}

func normalizeFinishAttemptRequest(request FinishAttemptRequest) (FinishAttemptRequest, error) {
	if request.ExpectedRevision == "" || !runtimeRevisionPattern.MatchString(request.ExpectedRevision) {
		return FinishAttemptRequest{}, errors.New("finish requires an exact expected runtime revision")
	}
	if !runtimeRequestIDPattern.MatchString(request.RequestID) {
		return FinishAttemptRequest{}, errors.New("request_id must be a canonical lowercase identifier")
	}
	if !validTerminalAttemptOutcome(request.Outcome) {
		return FinishAttemptRequest{}, errors.New("outcome must be failed, interrupted, or passed")
	}
	if !runtimeRevisionPattern.MatchString(request.EvidenceRevision) {
		return FinishAttemptRequest{}, errors.New("evidence_revision must be sha256")
	}
	if err := validateRuntimeText(request.Diagnosis, 500); err != nil {
		return FinishAttemptRequest{}, fmt.Errorf("invalid diagnosis: %w", err)
	}
	if !validHarnessDisposition(request.HarnessDisposition) {
		return FinishAttemptRequest{}, errors.New("harness_disposition must be reused or invalidated")
	}
	if err := validateRuntimeText(request.CleanupEvidence, 500); err != nil {
		return FinishAttemptRequest{}, fmt.Errorf("invalid cleanup_evidence: %w", err)
	}
	if err := validateRuntimeText(request.ProcessEvidence, 500); err != nil {
		return FinishAttemptRequest{}, fmt.Errorf("invalid process_evidence: %w", err)
	}
	return request, nil
}

func validateRuntimeText(value string, maximum int) error {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("value must be non-empty, trimmed, single-line, and bounded")
	}
	return nil
}

func validTerminalAttemptOutcome(outcome AttemptOutcome) bool {
	return outcome == AttemptFailed || outcome == AttemptInterrupted || outcome == AttemptPassed
}

func validHarnessDisposition(disposition HarnessDisposition) bool {
	return disposition == HarnessReused || disposition == HarnessInvalidated
}

func captureRuntimeCandidate(ctx context.Context, repo string) (reviewtransaction.Snapshot, error) {
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	intended, err := builder.DiscoverIntendedUntracked(ctx)
	if err != nil {
		return reviewtransaction.Snapshot{}, err
	}
	return builder.Build(ctx, reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges, Projection: reviewtransaction.ProjectionWorkspace,
		IntendedUntracked: intended,
	})
}

func runtimeObjectiveID(change, evidenceGoal string) string {
	return runtimeValueHash(runtimeObjectiveSchema, struct {
		Change       string `json:"change"`
		EvidenceGoal string `json:"evidence_goal"`
	}{Change: change, EvidenceGoal: evidenceGoal})
}

func runtimeValueHash(domain string, value any) string {
	payload, _ := json.Marshal(value)
	sum := sha256.Sum256(append(append([]byte(domain), '\n'), payload...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func runtimeRecordRevision(record runtimeRecord) (string, []byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return "", nil, err
	}
	payload = append(payload, '\n')
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), payload, nil
}

func (store RuntimeStore) ensureDirectories() error {
	if filepath.Clean(store.commonDir) == "." || !filepath.IsAbs(store.commonDir) {
		return errors.New("SDD runtime common directory is invalid")
	}
	relative, err := filepath.Rel(store.commonDir, filepath.Join(store.Dir, "records"))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("SDD runtime authority escapes the Git common directory")
	}
	current := store.commonDir
	segments := strings.Split(relative, string(filepath.Separator))
	created := make([]string, 0, len(segments))
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("SDD runtime authority contains an invalid path segment")
		}
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			mode := os.FileMode(0o700)
			// The shared gentle-ai container predates this private store and may
			// also hold review authority. New SDD runtime descendants remain 0700.
			if index == 0 && segment == "gentle-ai" {
				mode = 0o755
			}
			if err := os.Mkdir(current, mode); err != nil {
				if !os.IsExist(err) {
					return err
				}
			} else {
				created = append(created, current)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("SDD runtime authority path is not a private directory")
		}
	}
	if filepath.Clean(current) != filepath.Clean(filepath.Join(store.Dir, "records")) {
		return errors.New("SDD runtime authority path resolution is inconsistent")
	}
	for _, path := range created {
		if err := runtimeSyncDirectory(filepath.Dir(path)); err != nil {
			return fmt.Errorf("sync parent of SDD runtime authority directory: %w", err)
		}
	}
	if err := os.Chmod(store.Dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(store.Dir, "records"), 0o700); err != nil {
		return err
	}
	return nil
}

func (store RuntimeStore) publishRecord(revision string, payload []byte) error {
	recordsDir := filepath.Join(store.Dir, "records")
	path := filepath.Join(recordsDir, strings.TrimPrefix(revision, "sha256:")+".json")
	temp, err := os.CreateTemp(recordsDir, ".record-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err == nil {
		_, err = temp.Write(payload)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := runtimePublishRecord(tempPath, path); err != nil {
		if !os.IsExist(err) {
			return err
		}
		existing, readErr := readBoundedRuntimeFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, payload) {
			return errors.New("existing immutable SDD runtime record differs from its revision")
		}
	}
	if err := runtimeSyncDirectory(recordsDir); err != nil {
		return fmt.Errorf("sync immutable SDD runtime record directory: %w", err)
	}
	return nil
}

func (store RuntimeStore) publishHead(revision string) error {
	temp, err := os.CreateTemp(store.Dir, ".head-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err == nil {
		_, err = temp.WriteString(revision + "\n")
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := runtimeReplaceHead(tempPath, filepath.Join(store.Dir, "HEAD")); err != nil {
		return fmt.Errorf("publish SDD runtime HEAD: %w", err)
	}
	return nil
}

func (store RuntimeStore) syncReplay() error {
	if err := runtimeSyncDirectory(filepath.Join(store.Dir, "records")); err != nil {
		return fmt.Errorf("sync immutable SDD runtime record directory: %w", err)
	}
	if err := runtimeSyncDirectory(store.Dir); err != nil {
		return fmt.Errorf("sync SDD runtime HEAD directory: %w", err)
	}
	return nil
}

func readRuntimeHead(path string) (string, bool, error) {
	payload, err := readBoundedRuntimeFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if len(payload) != len("sha256:")+64+1 || payload[len(payload)-1] != '\n' {
		return "", true, errors.New("invalid SDD runtime HEAD encoding")
	}
	revision := strings.TrimSuffix(string(payload), "\n")
	if !runtimeRevisionPattern.MatchString(revision) {
		return "", true, errors.New("invalid SDD runtime HEAD revision")
	}
	return revision, true, nil
}

func (store RuntimeStore) loadRecord(revision string) (runtimeRecord, error) {
	if !runtimeRevisionPattern.MatchString(revision) {
		return runtimeRecord{}, errors.New("invalid SDD runtime record revision")
	}
	path := filepath.Join(store.Dir, "records", strings.TrimPrefix(revision, "sha256:")+".json")
	payload, err := readBoundedRuntimeFile(path)
	if err != nil {
		return runtimeRecord{}, fmt.Errorf("load SDD runtime revision %s: %w", revision, err)
	}
	sum := sha256.Sum256(payload)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != revision {
		return runtimeRecord{}, fmt.Errorf("SDD runtime record revision mismatch: expected %s, got %s", revision, actual)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var record runtimeRecord
	if err := decoder.Decode(&record); err != nil {
		return runtimeRecord{}, fmt.Errorf("decode SDD runtime revision %s: %w", revision, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return runtimeRecord{}, errors.New("SDD runtime record contains multiple JSON values")
	}
	_, canonical, err := runtimeRecordRevision(record)
	if err != nil || !bytes.Equal(payload, canonical) {
		return runtimeRecord{}, errors.New("SDD runtime record is not canonical")
	}
	if record.Change != store.Change {
		return runtimeRecord{}, errors.New("SDD runtime record change does not match store")
	}
	return record, nil
}

func readBoundedRuntimeFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maximumRuntimeRecordBytes {
		return nil, errors.New("SDD runtime authority artifact is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maximumRuntimeRecordBytes+1))
}
