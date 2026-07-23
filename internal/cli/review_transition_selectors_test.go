package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrePRValidateTransitionPreservesBaseRefSelector(t *testing.T) {
	repo := initReviewCLIRepo(t)
	baseCommit := strings.TrimSpace(runReviewCLIGit(t, repo, "rev-parse", "HEAD"))
	branch := strings.TrimSpace(runReviewCLIGit(t, repo, "symbolic-ref", "--short", "HEAD"))
	configureCLIReviewPublicationRemote(t, repo, branch)

	candidate := filepath.Join(repo, "candidate.txt")
	if err := os.WriteFile(candidate, []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "candidate.txt")
	runReviewCLIGit(t, repo, "commit", "-m", "add candidate")

	var startBuf bytes.Buffer
	err := RunReviewFacadeStart([]string{
		"--cwd", repo,
		"--lineage", "validate-selector-lineage",
		"--base-ref", baseCommit,
		"--committed-only",
	}, &startBuf)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	var startResult ReviewFacadeStartResult
	decodeStrictReviewJSON(t, startBuf.Bytes(), &startResult)

	// Capture result and finalize
	evidence := filepath.Join(t.TempDir(), "result.json")
	payload := `{"findings":[],"evidence":["checked exact target"]}`
	if err := os.WriteFile(evidence, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	if len(startResult.SelectedLenses) == 0 {
		t.Fatalf("startResult has no selected lenses: %#v, output: %s", startResult, startBuf.String())
	}

	if err := RunReviewCaptureResult([]string{
		"--cwd", repo, "--lineage", "validate-selector-lineage", "--target", startResult.TargetIdentity,
		"--lens", startResult.SelectedLenses[0], "--order", "0", "--input", evidence,
	}, &bytes.Buffer{}); err != nil {
		t.Fatalf("capture result failed for target %q lens %q: %v", startResult.TargetIdentity, startResult.SelectedLenses[0], err)
	}

	var finalizeBuf bytes.Buffer
	if err := RunReviewFacadeFinalize([]string{
		"--contract", ReviewIntegrationContractV1,
		"--cwd", repo,
		"--lineage", "validate-selector-lineage",
		"--captured-results",
	}, &finalizeBuf); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	// Capture evidence for validating state
	var statusBuf bytes.Buffer
	if err := RunReview([]string{
		"status",
		"--contract", ReviewIntegrationContractV1,
		"--cwd", repo,
		"--lineage", "validate-selector-lineage",
		"--base-ref", baseCommit,
	}, &statusBuf); err != nil {
		t.Fatalf("status failed: %v, buf: %s", err, statusBuf.String())
	}
	var validatingStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusBuf.Bytes(), &validatingStatus)

	evidenceFile := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(evidenceFile, []byte("verification passed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunReview([]string{
		"capture-evidence",
		"--cwd", repo,
		"--lineage", "validate-selector-lineage",
		"--target", startResult.TargetIdentity,
		"--expected-revision", validatingStatus.Authority.Revision,
		"--input", evidenceFile,
	}, &bytes.Buffer{}); err != nil {
		t.Fatalf("capture evidence failed: %v", err)
	}

	finalizeBuf.Reset()
	if err := RunReviewFacadeFinalize([]string{
		"--contract", ReviewIntegrationContractV1,
		"--cwd", repo,
		"--lineage", "validate-selector-lineage",
		"--captured-evidence",
	}, &finalizeBuf); err != nil {
		t.Fatalf("second finalize failed: %v", err)
	}

	// Now check status with pre-pr gate and base-ref
	statusBuf.Reset()
	err = RunReview([]string{
		"status",
		"--contract", ReviewIntegrationContractV1,
		"--next-transition",
		"--cwd", repo,
		"--lineage", "validate-selector-lineage",
		"--base-ref", baseCommit,
		"--gate", "pre-pr",
	}, &statusBuf)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusBuf.Bytes(), &status)

	if status.NextTransition == nil || status.NextTransition.Kind != reviewNextTransitionExecute {
		t.Fatalf("expected execute transition, got %#v", status.NextTransition)
	}

	exec := status.NextTransition.Execute
	if exec.Operation != "review.validate" {
		t.Fatalf("expected operation review.validate, got %s", exec.Operation)
	}

	baseRefArg := ""
	for _, arg := range exec.Arguments {
		if arg.Name == "base-ref" {
			baseRefArg = arg.Value
		}
	}
	if baseRefArg != "release" && baseRefArg != baseCommit {
		t.Fatalf("expected base-ref argument in validate transition, got arguments: %#v", exec.Arguments)
	}
}

func TestBaseDiffRecoverTransitionIncludesTargetSelectorsAndRejectsUnchanged(t *testing.T) {
	repo := initReviewCLIRepo(t)
	baseCommit := strings.TrimSpace(runReviewCLIGit(t, repo, "rev-parse", "HEAD"))

	candidate := filepath.Join(repo, "docs/recovery.md")
	if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("# Recovery candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "docs/recovery.md")
	runReviewCLIGit(t, repo, "commit", "-m", "add recovery candidate")

	// Start authority
	var startBuf bytes.Buffer
	err := RunReviewFacadeStart([]string{
		"--cwd", repo,
		"--lineage", "recover-selector-lineage",
		"--base-ref", baseCommit,
		"--committed-only",
	}, &startBuf)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Status to get revision
	var statusBuf bytes.Buffer
	if err := RunReview([]string{
		"status",
		"--contract", ReviewIntegrationContractV1,
		"--cwd", repo,
		"--lineage", "recover-selector-lineage",
		"--base-ref", baseCommit,
	}, &statusBuf); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	var reviewingStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusBuf.Bytes(), &reviewingStatus)

	// Invalidate
	if err := RunReviewInvalidate([]string{
		"--cwd", repo,
		"--lineage", "recover-selector-lineage",
		"--expected-revision", reviewingStatus.Authority.Revision,
		"--reason", "test invalidation",
	}, &bytes.Buffer{}); err != nil {
		t.Fatalf("invalidate failed: %v", err)
	}

	// Status for invalidated authority
	statusBuf.Reset()
	if err := RunReview([]string{
		"status",
		"--contract", ReviewIntegrationContractV1,
		"--cwd", repo,
		"--lineage", "recover-selector-lineage",
		"--base-ref", baseCommit,
	}, &statusBuf); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	var invalidatedStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusBuf.Bytes(), &invalidatedStatus)

	if invalidatedStatus.Authority == nil {
		t.Fatalf("invalidatedStatus.Authority is nil, status output: %s", statusBuf.String())
	}

	// 1. Unchanged target scope -> expect stop transition
	authHeaderUnchanged := "gentle-ai.review-recovery-authorization/v1\npredecessor_lineage=recover-selector-lineage\npredecessor_revision=" + invalidatedStatus.Authority.Revision + "\ntarget_identity=" + invalidatedStatus.TargetIdentity + "\nactor=maintainer\nreason=scope change test"
	statusBuf.Reset()
	err = RunReview([]string{
		"status",
		"--contract", ReviewIntegrationContractV1,
		"--next-transition",
		"--cwd", repo,
		"--lineage", "recover-selector-lineage",
		"--base-ref", baseCommit,
		"--recovery-successor-lineage", "recover-selector-successor",
		"--recovery-reason", "scope change test",
		"--recovery-actor", "maintainer",
		"--recovery-authorization", authHeaderUnchanged,
	}, &statusBuf)
	if err != nil {
		t.Fatalf("status with authorization failed: %v", err)
	}
	var unchangedNext ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusBuf.Bytes(), &unchangedNext)
	if unchangedNext.NextTransition == nil || unchangedNext.NextTransition.Kind != reviewNextTransitionStop {
		t.Fatalf("expected stop transition for unchanged recovery target, got %#v", unchangedNext.NextTransition)
	}

	// 2. Add new commit to change base-diff target scope so recovery is valid
	newCandidate := filepath.Join(repo, "docs/recovery-new.md")
	if err := os.WriteFile(newCandidate, []byte("# New recovery candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "docs/recovery-new.md")
	runReviewCLIGit(t, repo, "commit", "-m", "add new recovery candidate")

	// Get new status on updated target identity with explicit lineage
	statusBuf.Reset()
	if err := RunReview([]string{
		"status",
		"--contract", ReviewIntegrationContractV1,
		"--cwd", repo,
		"--lineage", "recover-selector-lineage",
		"--base-ref", baseCommit,
	}, &statusBuf); err != nil {
		t.Fatalf("status on changed target failed: %v", err)
	}
	var changedStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusBuf.Bytes(), &changedStatus)

	if invalidatedStatus.Authority == nil {
		t.Fatalf("invalidatedStatus.Authority is nil, status output: %s", statusBuf.String())
	}
	authHeader := "gentle-ai.review-recovery-authorization/v1\npredecessor_lineage=recover-selector-lineage\npredecessor_revision=" + invalidatedStatus.Authority.Revision + "\ntarget_identity=" + changedStatus.TargetIdentity + "\nactor=maintainer\nreason=scope change test"

	statusBuf.Reset()
	err = RunReview([]string{
		"status",
		"--contract", ReviewIntegrationContractV1,
		"--next-transition",
		"--cwd", repo,
		"--lineage", "recover-selector-lineage",
		"--base-ref", baseCommit,
		"--recovery-successor-lineage", "recover-selector-successor",
		"--recovery-reason", "scope change test",
		"--recovery-actor", "maintainer",
		"--recovery-authorization", authHeader,
	}, &statusBuf)
	if err != nil {
		t.Fatalf("status with authorization failed: %v", err)
	}

	var changedNext ReviewTargetStatusResult
	decodeStrictReviewJSON(t, statusBuf.Bytes(), &changedNext)

	if changedNext.NextTransition == nil || changedNext.NextTransition.Kind != reviewNextTransitionExecute {
		t.Fatalf("expected execute transition for changed recovery target, got %#v", changedNext.NextTransition)
	}

	exec := changedNext.NextTransition.Execute
	if exec.Operation != "review.recover" {
		t.Fatalf("expected operation review.recover, got %s", exec.Operation)
	}

	argMap := make(map[string]string)
	for _, arg := range exec.Arguments {
		argMap[arg.Name] = arg.Value
	}

	if argMap["base-ref"] != baseCommit {
		t.Errorf("expected base-ref=%s in recover transition, got %#v", baseCommit, argMap)
	}
	if argMap["committed-only"] != "true" {
		t.Errorf("expected committed-only=true in recover transition, got %#v", argMap)
	}

	// Now execute recover using the arguments from transition
	recoverArgs := []string{}
	for _, arg := range exec.Arguments {
		recoverArgs = append(recoverArgs, "--"+arg.Name+"="+arg.Value)
	}
	recoverArgs = append([]string{"--cwd", repo}, recoverArgs...)

	var recoverOut bytes.Buffer
	if err := RunReviewRecover(recoverArgs, &recoverOut); err != nil {
		t.Fatalf("RunReviewRecover failed with emitted arguments: %v\nArgs: %#v", err, recoverArgs)
	}

	var recoverResult ReviewRecoverResult
	if err := json.Unmarshal(recoverOut.Bytes(), &recoverResult); err != nil {
		t.Fatalf("failed to decode recover result: %v", err)
	}

	if recoverResult.LineageID != "recover-selector-successor" {
		t.Fatalf("expected successor lineage recover-selector-successor, got %s", recoverResult.LineageID)
	}
}
