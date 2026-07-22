package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestReviewRetryFinalVerificationOperationAndStatusCompleteNormally(t *testing.T) {
	fixture := failedFinalVerificationCLIFixture(t)
	statusArgs := []string{"status", "--contract", ReviewIntegrationContractV1, "--action-eligibility", "--next-transition", "--cwd", fixture.repo, "--lineage", fixture.predecessor.State.LineageID}
	var statusOutput bytes.Buffer
	if err := RunReview(statusArgs, &statusOutput); err != nil {
		t.Fatal(err)
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusOutput.Bytes(), &status)
	if status.Action != reviewtransaction.TargetStatusActionRetryFinalVerification || status.ActionDisposition != reviewtransaction.RecoveryFinalVerificationRetry ||
		status.FinalVerificationRetry == nil || status.FinalVerificationRetry.ValidatingRevision != fixture.incident.ValidatingRevision ||
		status.NextTransition == nil || status.NextTransition.Kind != reviewNextTransitionCollect ||
		status.NextTransition.ReasonCode != "final_verification_retry_authorization_required" ||
		status.Eligibility == nil || status.Eligibility.AllowedActions[0].Action != ReviewIntegrationOperationRetryFinalVerification {
		t.Fatalf("eligible retry status = %#v\n%s", status, statusOutput.String())
	}
	if strings.Contains(statusOutput.String(), fixture.repo) || strings.Contains(statusOutput.String(), fixture.incidentPath) {
		t.Fatalf("eligible status leaked a local path:\n%s", statusOutput.String())
	}

	request := reviewtransaction.FinalVerificationRetryRequest{
		PredecessorLineageID: fixture.predecessor.State.LineageID, ExpectedPredecessorRevision: fixture.predecessor.Revision,
		SuccessorLineageID: "retry-final-cli-successor", Incident: fixture.incident,
		Actor: "maintainer", Reason: "retry final verification after provider tooling failure",
	}
	authorization, err := reviewtransaction.FinalVerificationRetryAuthorization(request)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"retry-final-verification", "--contract", ReviewIntegrationContractV1, "--cwd", fixture.repo,
		"--predecessor-lineage", request.PredecessorLineageID, "--expected-predecessor-revision", request.ExpectedPredecessorRevision,
		"--successor-lineage", request.SuccessorLineageID, "--incident", fixture.incidentPath,
		"--actor", request.Actor, "--reason", request.Reason, "--maintainer-authorization", authorization}
	var output bytes.Buffer
	if err := RunReview(args, &output); err != nil {
		t.Fatal(err)
	}
	var result ReviewFinalVerificationRetryResult
	decodeStrictReviewJSON(t, decodeReviewOperationEnvelope(t, output.Bytes()).Result, &result)
	if result.Operation != ReviewIntegrationOperationRetryFinalVerification || result.LineageID != request.SuccessorLineageID ||
		result.State != reviewtransaction.StateValidating || result.PredecessorLineageID != request.PredecessorLineageID ||
		result.TargetIdentity != fixture.incident.TargetIdentity || result.IncidentDigest != reviewtransaction.FinalVerificationIncidentDigest(fixture.incident) {
		t.Fatalf("retry result = %#v\n%s", result, output.String())
	}
	if strings.Contains(output.String(), fixture.repo) || strings.Contains(output.String(), fixture.incidentPath) {
		t.Fatalf("retry result leaked a local path:\n%s", output.String())
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), fixture.repo, request.SuccessorLineageID)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if successor.State.Recovery == nil || successor.State.Recovery.Disposition != reviewtransaction.RecoveryFinalVerificationRetry ||
		successor.State.EvidenceHash != "" || successor.State.Generation != fixture.predecessor.State.Generation+1 {
		t.Fatalf("retry successor = %#v", successor.State)
	}

	replayOutput := bytes.Buffer{}
	if err := RunReview(args, &replayOutput); err != nil {
		t.Fatal(err)
	}
	var replay ReviewFinalVerificationRetryResult
	decodeStrictReviewJSON(t, decodeReviewOperationEnvelope(t, replayOutput.Bytes()).Result, &replay)
	if replay.StoreRevision != result.StoreRevision {
		t.Fatalf("exact retry replay revision = %q, want %q", replay.StoreRevision, result.StoreRevision)
	}

	passed := filepath.Join(t.TempDir(), "retry-passed.txt")
	if err := os.WriteFile(passed, []byte("retry verification passed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunReview([]string{"capture-evidence", "--cwd", fixture.repo, "--lineage", successor.State.LineageID,
		"--target", successor.State.CurrentSnapshot.Identity, "--expected-revision", successor.Revision, "--input", passed}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var finalized bytes.Buffer
	if err := RunReview([]string{"finalize", "--contract", ReviewIntegrationContractV1, "--cwd", fixture.repo,
		"--lineage", successor.State.LineageID, "--captured-evidence"}, &finalized); err != nil {
		t.Fatal(err)
	}
	var terminal ReviewIntegrationFinalizeResult
	decodeStrictReviewJSON(t, decodeReviewOperationEnvelope(t, finalized.Bytes()).Result, &terminal)
	if terminal.State != reviewtransaction.StateApproved {
		t.Fatalf("retry final state = %#v", terminal)
	}
}

func TestReviewRetryFinalVerificationNegotiatedDenialIsNoMutation(t *testing.T) {
	fixture := failedFinalVerificationCLIFixture(t)
	request := reviewtransaction.FinalVerificationRetryRequest{
		PredecessorLineageID: fixture.predecessor.State.LineageID, ExpectedPredecessorRevision: fixture.predecessor.Revision,
		SuccessorLineageID: "retry-final-cli-denied-successor", Incident: fixture.incident,
		Actor: "maintainer", Reason: "retry after tooling failure",
	}
	authorization, err := reviewtransaction.FinalVerificationRetryAuthorization(request)
	if err != nil {
		t.Fatal(err)
	}
	before := cliReviewAuthoritySnapshot(t, fixture.repo)
	var output bytes.Buffer
	err = RunReview([]string{"retry-final-verification", "--contract", ReviewIntegrationContractV1, "--cwd", fixture.repo,
		"--predecessor-lineage", request.PredecessorLineageID, "--expected-predecessor-revision", request.ExpectedPredecessorRevision,
		"--successor-lineage", request.SuccessorLineageID, "--incident", fixture.incidentPath,
		"--actor", request.Actor, "--reason", request.Reason, "--maintainer-authorization", authorization + "\n"}, &output)
	if err == nil {
		t.Fatal("inexact authorization succeeded")
	}
	var failure ReviewIntegrationFailure
	decodeStrictReviewJSON(t, output.Bytes(), &failure)
	if failure.Operation != ReviewIntegrationOperationRetryFinalVerification || failure.Code != "final_verification_retry_denied" ||
		failure.MutationOutcome != ReviewMutationNotStarted || failure.RetrySafe || failure.NextAction != "stop" {
		t.Fatalf("retry denial failure = %#v", failure)
	}
	if after := cliReviewAuthoritySnapshot(t, fixture.repo); !reflect.DeepEqual(after, before) {
		t.Fatalf("retry denial mutated authority: %#v != %#v", after, before)
	}
}

func TestFinalVerificationRetryContractFixturesAreStrictAndPathFree(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v1", "fixtures")
	incidentPayload, err := os.ReadFile(filepath.Join(root, "final-verification-incident.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	incident, err := reviewtransaction.ParseFinalVerificationIncident(incidentPayload)
	if err != nil || incident.Class != reviewtransaction.FinalVerificationIncidentProceduralToolingFailure {
		t.Fatalf("incident fixture = %#v, %v", incident, err)
	}
	validateFinalVerificationContractSchema(t, "final-verification-incident.schema.json", incidentPayload)
	statusPayload, err := os.ReadFile(filepath.Join(root, "status-v2-final-verification-retry.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusPayload, &status)
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	if status.Action != reviewtransaction.TargetStatusActionRetryFinalVerification ||
		status.FinalVerificationRetry == nil || status.NextTransition == nil ||
		status.NextTransition.ReasonCode != "final_verification_retry_authorization_required" {
		t.Fatalf("retry status fixture = %#v", status)
	}
	if strings.Contains(string(statusPayload), "/tmp/") || strings.Contains(string(statusPayload), `\\`) {
		t.Fatal("retry status fixture contains a local path")
	}
}

func validateFinalVerificationContractSchema(t *testing.T, name string, payload []byte) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "contracts", "review-integration", "v1", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		schemaPayload, readErr := os.ReadFile(filepath.Join(root, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var document any
		if err := json.Unmarshal(schemaPayload, &document); err != nil {
			t.Fatalf("decode %s: %v", entry.Name(), err)
		}
		location := "https://gentle-ai.dev/contracts/review-integration/v1/schemas/" + entry.Name()
		if err := compiler.AddResource(location, document); err != nil {
			t.Fatalf("add %s: %v", entry.Name(), err)
		}
	}
	schema, err := compiler.Compile("https://gentle-ai.dev/contracts/review-integration/v1/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("%s rejected fixture: %v", name, err)
	}
}

func TestReviewHelpPublishesDedicatedFinalVerificationRetryBoundary(t *testing.T) {
	var output bytes.Buffer
	if err := RunReview([]string{"help"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "retry-final-verification") ||
		!strings.Contains(output.String(), "Generic review recover remains unchanged") {
		t.Fatalf("review help omits the dedicated boundary:\n%s", output.String())
	}
}

type failedFinalVerificationCLI struct {
	repo         string
	predecessor  reviewtransaction.CompactRecord
	incident     reviewtransaction.FinalVerificationIncident
	incidentPath string
}

func failedFinalVerificationCLIFixture(t *testing.T) failedFinalVerificationCLI {
	t.Helper()
	repo, started, _, _, _ := capturedArtifact(t)
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, started.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", started.LineageID, "--captured-results"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	validating, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(t.TempDir(), "failed-evidence.txt")
	evidence := []byte("provider final verification tooling failed\n")
	if err := os.WriteFile(evidencePath, evidence, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunReviewCaptureEvidence([]string{"--cwd", repo, "--lineage", started.LineageID,
		"--target", validating.State.CurrentSnapshot.Identity, "--expected-revision", validating.Revision, "--input", evidencePath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", started.LineageID, "--captured-evidence", "--failed"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	predecessor, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if predecessor.State.State != reviewtransaction.StateEscalated {
		t.Fatalf("failed fixture state = %q", predecessor.State.State)
	}
	requestDigest := completedFinalizeRequestDigest(t, store.FinalizeAttemptJournalPath(), predecessor.Revision)
	incident := reviewtransaction.FinalVerificationIncident{
		Schema: reviewtransaction.FinalVerificationIncidentSchema, Class: reviewtransaction.FinalVerificationIncidentProceduralToolingFailure,
		LineageID: predecessor.State.LineageID, TerminalRevision: predecessor.Revision, ValidatingRevision: validating.Revision,
		TargetIdentity: predecessor.State.CurrentSnapshot.Identity, FailedEvidenceHash: predecessor.State.EvidenceHash, FinalizeRequestDigest: requestDigest,
	}
	payload, err := reviewtransaction.CanonicalFinalVerificationIncident(incident)
	if err != nil {
		t.Fatal(err)
	}
	incidentPath := filepath.Join(t.TempDir(), "incident.json")
	if err := os.WriteFile(incidentPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return failedFinalVerificationCLI{repo: repo, predecessor: predecessor, incident: incident, incidentPath: incidentPath}
}

func completedFinalizeRequestDigest(t *testing.T, path, terminalRevision string) string {
	t.Helper()
	var journal struct {
		Attempts []struct {
			Request struct {
				RequestDigest string `json:"request_digest"`
			} `json:"request"`
			Transitions []struct {
				Operation string `json:"operation"`
				Revision  string `json:"revision"`
			} `json:"transitions"`
			ReceiptPublished bool `json:"receipt_published"`
			Completed        bool `json:"completed"`
		} `json:"attempts"`
	}
	payload, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(payload, &journal) != nil {
		t.Fatalf("read finalize journal: %v", err)
	}
	for _, attempt := range journal.Attempts {
		if !attempt.Completed || !attempt.ReceiptPublished || len(attempt.Transitions) == 0 {
			continue
		}
		last := attempt.Transitions[len(attempt.Transitions)-1]
		if last.Operation == "review/complete-verification" && last.Revision == terminalRevision {
			return attempt.Request.RequestDigest
		}
	}
	t.Fatal("completed final-verification attempt not found")
	return ""
}

func cliReviewAuthoritySnapshot(t *testing.T, repo string) map[string]string {
	t.Helper()
	gitDir := strings.TrimSpace(runReviewCLIGitOutput(t, repo, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repo, gitDir)
	}
	root := filepath.Join(gitDir, "gentle-ai", "review-transactions")
	result := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == "LOCK" || strings.HasPrefix(entry.Name(), ".atomic-") {
			return err
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, _ := filepath.Rel(root, path)
		result[filepath.ToSlash(relative)] = facadePayloadHash(payload)
		return nil
	})
	return result
}

func runReviewCLIGitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := append([]string{"-C", repo}, args...)
	output, err := exec.Command("git", command...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", command, err, output)
	}
	return string(output)
}
