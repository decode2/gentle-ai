package reviewtransaction

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
	"runtime"
	"strings"
	"time"
)

const (
	FinalVerificationIncidentSchema                   = "gentle-ai.review-final-verification-incident/v1"
	FinalVerificationIncidentProceduralToolingFailure = "procedural_tooling_failure"
	CompactFinalVerificationRetryProofSchema          = "gentle-ai.review-final-verification-retry-proof/v1"
	FinalVerificationRetryAuthorizationSchema         = "gentle-ai.review-final-verification-retry-authorization/v1"
	CompactFinalEvidenceDir                           = "final-evidence"
	CompactFinalEvidenceFile                          = "verification.txt"
	compactFinalEvidenceLimit                         = 4 << 20
)

var finalVerificationRetryAfterFirstLiveValidation = func() {}
var finalVerificationRetryEvidenceAfterLstat = func() {}

// FinalVerificationIncident is the only admitted reason for reopening final
// verification. It describes a procedural/tooling failure after candidate
// review and correction were already complete; reviewer, validator, and code
// failures cannot be represented by this closed shape.
type FinalVerificationIncident struct {
	Schema                string `json:"schema"`
	Class                 string `json:"class"`
	LineageID             string `json:"lineage_id"`
	TerminalRevision      string `json:"terminal_revision"`
	ValidatingRevision    string `json:"validating_revision"`
	TargetIdentity        string `json:"target_identity"`
	FailedEvidenceHash    string `json:"failed_evidence_hash"`
	FinalizeRequestDigest string `json:"finalize_request_digest"`
}

// CompactFinalVerificationRetryProof makes the source failure self-contained
// in the successor recovery edge. Failed evidence bytes stay immutable in the
// predecessor's provider-owned artifact; this proof binds their hash, the
// exact journal attempt, and the canonical incident.
type CompactFinalVerificationRetryProof struct {
	Schema                string                    `json:"schema"`
	TerminalRevision      string                    `json:"terminal_revision"`
	ValidatingRevision    string                    `json:"validating_revision"`
	TargetIdentity        string                    `json:"target_identity"`
	FailedEvidenceHash    string                    `json:"failed_evidence_hash"`
	FinalizeRequestDigest string                    `json:"finalize_request_digest"`
	Incident              FinalVerificationIncident `json:"incident"`
	IncidentDigest        string                    `json:"incident_digest"`
	SourceFinalizeAttempt FinalizeAttempt           `json:"source_finalize_attempt"`
}

// FinalVerificationRetryRequest is the complete content-bound admission
// request. The provider derives the successor from the predecessor; callers
// cannot supply mutable review state or budgets.
type FinalVerificationRetryRequest struct {
	PredecessorLineageID        string
	ExpectedPredecessorRevision string
	SuccessorLineageID          string
	Incident                    FinalVerificationIncident
	Actor                       string
	Reason                      string
	RecoveredAt                 time.Time
	MaintainerAuthorization     string
}

// FinalVerificationRetryEligibility is safe status metadata for the one
// eligible failed FINALIZE attempt. It contains no local artifact path.
type FinalVerificationRetryEligibility struct {
	IncidentSchema        string `json:"incident_schema"`
	IncidentClass         string `json:"incident_class"`
	ValidatingRevision    string `json:"validating_revision"`
	TargetIdentity        string `json:"target_identity"`
	FailedEvidenceHash    string `json:"failed_evidence_hash"`
	FinalizeRequestDigest string `json:"finalize_request_digest"`
}

var ErrFinalVerificationRetryDenied = errors.New("final-verification retry denied")

type FinalVerificationRetryDeniedError struct {
	Code string
	Why  string
}

func (err *FinalVerificationRetryDeniedError) Error() string {
	if err.Why == "" {
		return fmt.Sprintf("%s: %s", ErrFinalVerificationRetryDenied, err.Code)
	}
	return fmt.Sprintf("%s: %s: %s", ErrFinalVerificationRetryDenied, err.Code, err.Why)
}

func (err *FinalVerificationRetryDeniedError) Unwrap() error { return ErrFinalVerificationRetryDenied }

func denyFinalVerificationRetry(code, why string) error {
	return &FinalVerificationRetryDeniedError{Code: code, Why: why}
}

func validateFinalVerificationIncident(incident FinalVerificationIncident) error {
	if incident.Schema != FinalVerificationIncidentSchema || incident.Class != FinalVerificationIncidentProceduralToolingFailure {
		return errors.New("unsupported final-verification incident identity or class")
	}
	if err := validateLineageID(incident.LineageID); err != nil {
		return errors.New("invalid final-verification incident lineage")
	}
	for _, value := range []string{incident.TerminalRevision, incident.ValidatingRevision, incident.TargetIdentity, incident.FailedEvidenceHash, incident.FinalizeRequestDigest} {
		if !validSHA256(value) {
			return errors.New("final-verification incident contains an invalid SHA-256 binding")
		}
	}
	return nil
}

func CanonicalFinalVerificationIncident(incident FinalVerificationIncident) ([]byte, error) {
	if err := validateFinalVerificationIncident(incident); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(incident)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func ParseFinalVerificationIncident(payload []byte) (FinalVerificationIncident, error) {
	if bytes.Contains(payload, []byte{'\r'}) {
		return FinalVerificationIncident{}, errors.New("final-verification incident must use LF-only canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var incident FinalVerificationIncident
	if err := decoder.Decode(&incident); err != nil {
		return FinalVerificationIncident{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return FinalVerificationIncident{}, errors.New("final-verification incident contains multiple JSON values")
	}
	canonical, err := CanonicalFinalVerificationIncident(incident)
	if err != nil {
		return FinalVerificationIncident{}, err
	}
	if !bytes.Equal(payload, canonical) {
		return FinalVerificationIncident{}, errors.New("final-verification incident is not canonical JSON")
	}
	return incident, nil
}

func finalVerificationPayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func FinalVerificationIncidentDigest(incident FinalVerificationIncident) string {
	payload, err := CanonicalFinalVerificationIncident(incident)
	if err != nil {
		return ""
	}
	return finalVerificationPayloadDigest(payload)
}

func validFinalVerificationAuthorizationField(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func validateFinalVerificationRetryRequestShape(request FinalVerificationRetryRequest) error {
	if validateLineageID(request.PredecessorLineageID) != nil || validateLineageID(request.SuccessorLineageID) != nil ||
		request.PredecessorLineageID == request.SuccessorLineageID || !validSHA256(request.ExpectedPredecessorRevision) {
		return errors.New("final-verification retry lineage or revision binding is invalid")
	}
	if !validFinalVerificationAuthorizationField(request.Actor) || !validFinalVerificationAuthorizationField(request.Reason) {
		return errors.New("final-verification retry actor and reason must be non-empty single-line canonical values")
	}
	if err := validateFinalVerificationIncident(request.Incident); err != nil {
		return err
	}
	if request.Incident.LineageID != request.PredecessorLineageID || request.Incident.TerminalRevision != request.ExpectedPredecessorRevision {
		return errors.New("final-verification incident does not bind the requested predecessor")
	}
	return nil
}

// FinalVerificationRetryAuthorization returns the sole accepted LF-only
// authorization. Every value comes from the content-bound request; callers do
// not choose an authorization format or omit a provider binding.
func FinalVerificationRetryAuthorization(request FinalVerificationRetryRequest) (string, error) {
	if err := validateFinalVerificationRetryRequestShape(request); err != nil {
		return "", err
	}
	incidentDigest := FinalVerificationIncidentDigest(request.Incident)
	return FinalVerificationRetryAuthorizationSchema +
		"\npredecessor_lineage=" + request.PredecessorLineageID +
		"\npredecessor_revision=" + request.ExpectedPredecessorRevision +
		"\nsuccessor_lineage=" + request.SuccessorLineageID +
		"\nvalidating_revision=" + request.Incident.ValidatingRevision +
		"\ntarget_identity=" + request.Incident.TargetIdentity +
		"\nfailed_evidence_hash=" + request.Incident.FailedEvidenceHash +
		"\nfinalize_request_digest=" + request.Incident.FinalizeRequestDigest +
		"\nincident_class=" + request.Incident.Class +
		"\nincident_digest=" + incidentDigest +
		"\nactor=" + request.Actor +
		"\nreason=" + request.Reason, nil
}

func validateExactFinalVerificationRetryAuthorization(request FinalVerificationRetryRequest) error {
	want, err := FinalVerificationRetryAuthorization(request)
	if err != nil {
		return denyFinalVerificationRetry("invalid_request", err.Error())
	}
	if request.MaintainerAuthorization != want || strings.Contains(request.MaintainerAuthorization, "\r") {
		return denyFinalVerificationRetry("authorization_mismatch", "maintainer authorization is not the exact LF-only provider binding")
	}
	return nil
}

func finalVerificationEvidencePathForStore(store CompactStore) string {
	return filepath.Join(store.Dir, CompactFinalEvidenceDir, CompactFinalEvidenceFile)
}

func compactPrivateArtifactMode(mode os.FileMode, directory bool) bool {
	return runtime.GOOS == "windows" || mode.Perm()&0o077 == 0 && (!directory || mode.Perm()&0o700 == 0o700)
}

func readCompactFailedFinalEvidence(store CompactStore) ([]byte, error) {
	dir := filepath.Join(store.Dir, CompactFinalEvidenceDir)
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || !compactPrivateArtifactMode(dirInfo.Mode(), true) {
		return nil, errors.New("failed final-verification evidence directory is unavailable or unsafe")
	}
	path := finalVerificationEvidencePathForStore(store)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !compactPrivateArtifactMode(info.Mode(), false) {
		return nil, errors.New("failed final-verification evidence is unavailable or unsafe")
	}
	finalVerificationRetryEvidenceAfterLstat()
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("failed final-verification evidence changed before read")
	}
	payload, err := io.ReadAll(io.LimitReader(file, compactFinalEvidenceLimit+1))
	if err != nil || len(payload) == 0 || len(payload) > compactFinalEvidenceLimit {
		return nil, errors.New("failed final-verification evidence is empty or exceeds the native limit")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) {
		return nil, errors.New("failed final-verification evidence changed during read")
	}
	return payload, nil
}

func buildLiveFinalVerificationSnapshot(ctx context.Context, repo string, expected Snapshot) (Snapshot, error) {
	target := Target{Kind: expected.Kind, Projection: expected.Projection,
		IntendedUntracked: append([]string(nil), expected.IntendedUntracked...), LedgerIDs: append([]string(nil), expected.LedgerIDs...)}
	if target.IntendedUntracked == nil {
		target.IntendedUntracked = []string{}
	}
	if target.LedgerIDs == nil {
		target.LedgerIDs = []string{}
	}
	switch expected.Kind {
	case TargetCurrentChanges:
	case TargetBaseDiff, TargetBaseWorkspaceOverlay, TargetFixDiff:
		target.BaseRef = expected.BaseTree
	default:
		return Snapshot{}, fmt.Errorf("unsupported live final-verification target kind %q", expected.Kind)
	}
	live, err := (SnapshotBuilder{Repo: repo}).Build(ctx, target)
	if err != nil {
		return Snapshot{}, err
	}
	if !snapshotsEqual(live, expected) {
		return Snapshot{}, errors.New("live current/final snapshot no longer matches failed final verification")
	}
	return live, nil
}

type finalVerificationRetrySource struct {
	eligibility FinalVerificationRetryEligibility
	attempt     FinalizeAttempt
	evidence    []byte
}

func deriveFinalVerificationRetrySourceLocked(ctx context.Context, store CompactStore, predecessor CompactRecord) (finalVerificationRetrySource, error) {
	if predecessor.HistoricalCompat || predecessor.State.Schema != CompactStateSchema || predecessor.State.State != StateEscalated || !validSHA256(predecessor.State.EvidenceHash) {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("ineligible_predecessor", "predecessor is not an exact compact-v2 failed final-verification terminal")
	}
	if !compactRecoveryReceiptBound(store, predecessor) {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("receipt_mismatch", "escalated receipt does not match terminal authority")
	}
	journal, err := store.loadFinalizeAttemptJournalLocked()
	if err != nil {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("journal_mismatch", "completed FINALIZE journal is unavailable or invalid")
	}
	qualifying := make([]FinalizeAttempt, 0, 1)
	for _, attempt := range journal.Attempts {
		if len(attempt.Transitions) == 0 {
			continue
		}
		last := attempt.Transitions[len(attempt.Transitions)-1]
		if last.Operation == "review/complete-verification" && last.Revision == predecessor.Revision {
			qualifying = append(qualifying, attempt)
		}
	}
	if len(qualifying) != 1 {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("journal_ambiguous", "terminal authority does not have exactly one final-verification FINALIZE attempt")
	}
	attempt := qualifying[0]
	last := attempt.Transitions[len(attempt.Transitions)-1]
	if !attempt.Completed || !attempt.ReceiptPublished || !validSHA256(last.ExpectedRevision) ||
		attempt.Request.CandidateDigest != FinalizeAttemptValueDigest("candidate", predecessor.State.CurrentSnapshot) ||
		attempt.Request.FailedDigest != FinalizeAttemptValueDigest("failed", true) {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("journal_mismatch", "FINALIZE attempt does not prove a completed failed verification of CurrentSnapshot")
	}
	evidence, err := readCompactFailedFinalEvidence(store)
	if err != nil {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("evidence_mismatch", err.Error())
	}
	evidenceHash := finalVerificationPayloadDigest(evidence)
	if evidenceHash != predecessor.State.EvidenceHash || attempt.Request.EvidenceDigest != FinalizeAttemptValueDigest("evidence", evidence) {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("evidence_mismatch", "captured evidence bytes do not bind the terminal state and FINALIZE journal")
	}
	if _, err := buildLiveFinalVerificationSnapshot(ctx, store.repo, predecessor.State.CurrentSnapshot); err != nil {
		return finalVerificationRetrySource{}, denyFinalVerificationRetry("live_target_drift", err.Error())
	}
	return finalVerificationRetrySource{attempt: attempt, evidence: evidence, eligibility: FinalVerificationRetryEligibility{
		IncidentSchema: FinalVerificationIncidentSchema, IncidentClass: FinalVerificationIncidentProceduralToolingFailure,
		ValidatingRevision: last.ExpectedRevision, TargetIdentity: predecessor.State.CurrentSnapshot.Identity,
		FailedEvidenceHash: evidenceHash, FinalizeRequestDigest: attempt.Request.RequestDigest,
	}}, nil
}

func validateFinalVerificationIncidentAgainstSource(request FinalVerificationRetryRequest, source finalVerificationRetrySource) error {
	incident := request.Incident
	want := source.eligibility
	if incident.LineageID != request.PredecessorLineageID || incident.TerminalRevision != request.ExpectedPredecessorRevision ||
		incident.ValidatingRevision != want.ValidatingRevision || incident.TargetIdentity != want.TargetIdentity ||
		incident.FailedEvidenceHash != want.FailedEvidenceHash || incident.FinalizeRequestDigest != want.FinalizeRequestDigest {
		return denyFinalVerificationRetry("incident_mismatch", "incident does not exactly bind the provider-derived failed FINALIZE source")
	}
	return nil
}

func cloneCompactStateValue(state CompactState) (CompactState, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return CompactState{}, err
	}
	var cloned CompactState
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return CompactState{}, err
	}
	return cloned, nil
}

func finalVerificationRetrySuccessor(predecessor CompactRecord, request FinalVerificationRetryRequest, source finalVerificationRetrySource, recoveredAt time.Time) (CompactState, error) {
	successor, err := cloneCompactStateValue(predecessor.State)
	if err != nil {
		return CompactState{}, err
	}
	proof := &CompactFinalVerificationRetryProof{
		Schema: CompactFinalVerificationRetryProofSchema, TerminalRevision: predecessor.Revision,
		ValidatingRevision: source.eligibility.ValidatingRevision, TargetIdentity: predecessor.State.CurrentSnapshot.Identity,
		FailedEvidenceHash: predecessor.State.EvidenceHash, FinalizeRequestDigest: source.attempt.Request.RequestDigest,
		Incident: request.Incident, IncidentDigest: FinalVerificationIncidentDigest(request.Incident), SourceFinalizeAttempt: source.attempt,
	}
	successor.LineageID = request.SuccessorLineageID
	successor.Generation = predecessor.State.Generation + 1
	successor.State = StateValidating
	successor.EvidenceHash = ""
	successor.Recovery = &CompactRecoveryProvenance{
		PredecessorLineageID: predecessor.State.LineageID, PredecessorRevision: predecessor.Revision,
		Disposition: RecoveryFinalVerificationRetry, Reason: request.Reason, Actor: request.Actor,
		RecoveredAt: recoveredAt.UTC(), MaintainerAuthorization: request.MaintainerAuthorization,
		FinalVerificationRetry: proof,
	}
	return successor, successor.Validate()
}

func validateCompactFinalVerificationRetryProofShape(successor CompactState, recovery CompactRecoveryProvenance) error {
	proof := recovery.FinalVerificationRetry
	if proof == nil || proof.Schema != CompactFinalVerificationRetryProofSchema || recovery.Evidence != nil ||
		proof.TerminalRevision != recovery.PredecessorRevision || proof.TargetIdentity != successor.CurrentSnapshot.Identity ||
		proof.FailedEvidenceHash == "" || proof.IncidentDigest != FinalVerificationIncidentDigest(proof.Incident) ||
		proof.FinalizeRequestDigest != proof.SourceFinalizeAttempt.Request.RequestDigest {
		return errors.New("final-verification retry source proof is incomplete or invalid")
	}
	if err := validateFinalVerificationIncident(proof.Incident); err != nil {
		return err
	}
	if proof.Incident.LineageID != recovery.PredecessorLineageID || proof.Incident.TerminalRevision != recovery.PredecessorRevision ||
		proof.Incident.ValidatingRevision != proof.ValidatingRevision || proof.Incident.TargetIdentity != proof.TargetIdentity ||
		proof.Incident.FailedEvidenceHash != proof.FailedEvidenceHash || proof.Incident.FinalizeRequestDigest != proof.FinalizeRequestDigest {
		return errors.New("final-verification retry incident does not bind its source proof")
	}
	attempt := proof.SourceFinalizeAttempt
	if !attempt.Completed || !attempt.ReceiptPublished || len(attempt.Transitions) == 0 {
		return errors.New("final-verification retry source FINALIZE attempt is incomplete")
	}
	last := attempt.Transitions[len(attempt.Transitions)-1]
	if last.Operation != "review/complete-verification" || last.ExpectedRevision != proof.ValidatingRevision || last.Revision != proof.TerminalRevision ||
		attempt.Request.CandidateDigest != FinalizeAttemptValueDigest("candidate", successor.CurrentSnapshot) ||
		attempt.Request.FailedDigest != FinalizeAttemptValueDigest("failed", true) {
		return errors.New("final-verification retry source FINALIZE attempt does not prove failed CurrentSnapshot verification")
	}
	authorization, err := FinalVerificationRetryAuthorization(FinalVerificationRetryRequest{
		PredecessorLineageID: recovery.PredecessorLineageID, ExpectedPredecessorRevision: recovery.PredecessorRevision,
		SuccessorLineageID: successor.LineageID, Incident: proof.Incident, Actor: recovery.Actor, Reason: recovery.Reason,
	})
	if err != nil || recovery.MaintainerAuthorization != authorization {
		return errors.New("final-verification retry recovery authorization is not exact")
	}
	return nil
}

func validateCompactFinalVerificationRetryEdge(predecessor CompactRecord, successor CompactState) error {
	recovery := successor.Recovery
	if recovery == nil || recovery.Disposition != RecoveryFinalVerificationRetry || recovery.FinalVerificationRetry == nil ||
		predecessor.State.State != StateEscalated || predecessor.State.EvidenceHash == "" {
		return errors.New("final-verification retry requires an escalated failed-verification predecessor")
	}
	if err := validateCompactFinalVerificationRetryProofShape(successor, *recovery); err != nil {
		return err
	}
	proof := recovery.FinalVerificationRetry
	if proof.FailedEvidenceHash != predecessor.State.EvidenceHash || proof.TargetIdentity != predecessor.State.CurrentSnapshot.Identity ||
		proof.TerminalRevision != predecessor.Revision {
		return errors.New("final-verification retry proof does not bind its predecessor")
	}
	want, err := cloneCompactStateValue(predecessor.State)
	if err != nil {
		return err
	}
	want.LineageID, want.Generation, want.State, want.EvidenceHash, want.Recovery = successor.LineageID, successor.Generation, StateValidating, "", successor.Recovery
	if !compactStateEqual(want, successor) {
		return errors.New("final-verification retry successor changed frozen authority or budget state")
	}
	return nil
}

func finalVerificationRetryAncestryEligible(predecessor CompactRecord, records map[string]CompactRecord) error {
	seen := map[string]bool{}
	cursor := predecessor
	for {
		if seen[cursor.State.LineageID] {
			return denyFinalVerificationRetry("ambiguous_ancestry", "recovery ancestry contains a cycle")
		}
		seen[cursor.State.LineageID] = true
		if cursor.State.Recovery == nil {
			return nil
		}
		if cursor.State.Recovery.Disposition == RecoveryFinalVerificationRetry {
			return denyFinalVerificationRetry("retry_already_consumed", "final_verification_retry already exists in predecessor ancestry")
		}
		parent, ok := records[cursor.State.Recovery.PredecessorLineageID]
		if !ok || parent.Revision != cursor.State.Recovery.PredecessorRevision {
			return denyFinalVerificationRetry("ambiguous_ancestry", "recovery ancestry is incomplete or revision-mismatched")
		}
		cursor = parent
	}
}

// RetryCompactFinalVerification creates the sole provider-owned
// final_verification_retry edge. The repository-wide compact lock serializes
// predecessor CAS, graph/ancestry validation, live CurrentSnapshot proof, and
// successor publication. Every denial returns before writeAtomic.
func RetryCompactFinalVerification(ctx context.Context, repo string, request FinalVerificationRetryRequest) (CompactRecord, error) {
	if err := ctx.Err(); err != nil {
		return CompactRecord{}, err
	}
	if err := validateExactFinalVerificationRetryAuthorization(request); err != nil {
		return CompactRecord{}, err
	}
	predecessorStore, err := CompactAuthoritativeStore(ctx, repo, request.PredecessorLineageID)
	if err != nil {
		return CompactRecord{}, err
	}
	successorStore, err := CompactAuthoritativeStore(ctx, repo, request.SuccessorLineageID)
	if err != nil {
		return CompactRecord{}, err
	}
	var maintenance *MaintenanceLock
	if predecessorStore.maintenanceLockPath != "" {
		maintenance, err = acquireMaintenanceLock(ctx, predecessorStore.maintenanceLockPath, maintenanceShared)
		if err != nil {
			return CompactRecord{}, err
		}
		defer maintenance.Release()
	}
	lock, err := acquireCompactStartLock(ctx, predecessorStore.lockPath)
	if err != nil {
		return CompactRecord{}, err
	}
	defer lock.release()

	predecessor, err := predecessorStore.loadCompactRecordLocked()
	if err != nil {
		return CompactRecord{}, denyFinalVerificationRetry("predecessor_unavailable", "compact-v2 predecessor cannot be loaded")
	}
	if predecessor.Revision != request.ExpectedPredecessorRevision {
		return CompactRecord{}, denyFinalVerificationRetry("stale_revision", "expected predecessor revision is not current")
	}
	stores, err := DiscoverCompactStores(ctx, predecessorStore.repo)
	if err != nil {
		return CompactRecord{}, err
	}
	records := make(map[string]CompactRecord, len(stores))
	storeByLineage := make(map[string]CompactStore, len(stores))
	for _, store := range stores {
		record, loadErr := store.loadCompactRecordLocked()
		if loadErr != nil {
			return CompactRecord{}, denyFinalVerificationRetry("ambiguous_authority", "compact authority graph cannot be loaded exactly")
		}
		records[record.State.LineageID], storeByLineage[record.State.LineageID] = record, store
	}
	if _, err := compactAuthorityLeaves(records, storeByLineage); err != nil {
		return CompactRecord{}, denyFinalVerificationRetry("ambiguous_authority", err.Error())
	}
	if err := finalVerificationRetryAncestryEligible(predecessor, records); err != nil {
		return CompactRecord{}, err
	}
	source, err := deriveFinalVerificationRetrySourceLocked(ctx, predecessorStore, predecessor)
	if err != nil {
		return CompactRecord{}, err
	}
	if err := validateFinalVerificationIncidentAgainstSource(request, source); err != nil {
		return CompactRecord{}, err
	}
	children := make([]CompactRecord, 0, 1)
	for _, record := range records {
		if record.State.Recovery != nil && record.State.Recovery.PredecessorLineageID == predecessor.State.LineageID {
			children = append(children, record)
		}
	}
	existing, successorExists := records[request.SuccessorLineageID]
	if len(children) > 0 && (!successorExists || len(children) != 1 || children[0].State.LineageID != request.SuccessorLineageID) {
		return CompactRecord{}, denyFinalVerificationRetry("predecessor_has_successor", "predecessor already has a different successor")
	}
	if successorExists && (existing.State.Recovery == nil || existing.State.Recovery.PredecessorLineageID != predecessor.State.LineageID) {
		return CompactRecord{}, denyFinalVerificationRetry("successor_collision", "successor lineage already contains different authority")
	}
	recoveredAt := request.RecoveredAt.UTC()
	if request.RecoveredAt.IsZero() {
		if successorExists && existing.State.Recovery != nil {
			recoveredAt = existing.State.Recovery.RecoveredAt
		} else {
			recoveredAt = time.Now().UTC()
		}
	}
	successor, err := finalVerificationRetrySuccessor(predecessor, request, source, recoveredAt)
	if err != nil {
		return CompactRecord{}, denyFinalVerificationRetry("invalid_successor", err.Error())
	}
	if err := validateCompactRecoveryEdge(predecessor, successor); err != nil {
		return CompactRecord{}, denyFinalVerificationRetry("invalid_successor", err.Error())
	}
	if successorExists {
		if compactStateEqual(existing.State, successor) {
			return existing, nil
		}
		return CompactRecord{}, denyFinalVerificationRetry("different_replay", "existing successor does not match the exact retry request")
	}
	if len(children) != 0 {
		return CompactRecord{}, denyFinalVerificationRetry("predecessor_has_successor", "predecessor is not an authority leaf")
	}
	finalVerificationRetryAfterFirstLiveValidation()
	if _, err := buildLiveFinalVerificationSnapshot(ctx, predecessorStore.repo, predecessor.State.CurrentSnapshot); err != nil {
		return CompactRecord{}, denyFinalVerificationRetry("live_target_drift", err.Error())
	}
	current, err := predecessorStore.loadCompactRecordLocked()
	if err != nil || current.Revision != request.ExpectedPredecessorRevision || !compactStateEqual(current.State, predecessor.State) {
		return CompactRecord{}, denyFinalVerificationRetry("stale_revision", "predecessor changed before successor publication")
	}
	record, payload, err := makeCompactRecord(successor)
	if err != nil {
		return CompactRecord{}, err
	}
	if err := writeAtomic(successorStore.StatePath(), payload, 0o644); err != nil {
		return CompactRecord{}, err
	}
	return record, nil
}

// InspectCompactFinalVerificationRetrySource returns provider-derived status
// metadata only when the exact selected leaf is eligible. Ineligible states
// return ok=false without weakening their terminal stop behavior.
func InspectCompactFinalVerificationRetrySource(ctx context.Context, repo, lineageID, expectedRevision string) (FinalVerificationRetryEligibility, bool, error) {
	store, err := CompactAuthoritativeStore(ctx, repo, lineageID)
	if err != nil {
		return FinalVerificationRetryEligibility{}, false, err
	}
	maintenance, err := store.acquireReadMaintenance(ctx)
	if err != nil {
		return FinalVerificationRetryEligibility{}, false, err
	}
	if maintenance != nil {
		defer maintenance.Release()
	}
	record, err := store.loadCompactRecordLocked()
	if err != nil || record.Revision != expectedRevision {
		return FinalVerificationRetryEligibility{}, false, err
	}
	source, err := deriveFinalVerificationRetrySourceLocked(ctx, store, record)
	if err != nil {
		if errors.Is(err, ErrFinalVerificationRetryDenied) {
			return FinalVerificationRetryEligibility{}, false, nil
		}
		return FinalVerificationRetryEligibility{}, false, err
	}
	stores, err := DiscoverCompactStores(ctx, store.repo)
	if err != nil {
		return FinalVerificationRetryEligibility{}, false, err
	}
	records := make(map[string]CompactRecord, len(stores))
	for _, candidateStore := range stores {
		candidate, loadErr := candidateStore.LoadContext(ctx)
		if loadErr != nil {
			return FinalVerificationRetryEligibility{}, false, loadErr
		}
		records[candidate.State.LineageID] = candidate
		if candidate.State.Recovery != nil && candidate.State.Recovery.PredecessorLineageID == lineageID {
			return FinalVerificationRetryEligibility{}, false, nil
		}
	}
	if err := finalVerificationRetryAncestryEligible(record, records); err != nil {
		return FinalVerificationRetryEligibility{}, false, nil
	}
	return source.eligibility, true, nil
}
