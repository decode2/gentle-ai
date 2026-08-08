package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

var sddEvidenceRetryObjective = []string{
	"--work-unit", "bench evidence-only verification retry",
	"--evidence-goal", "retry transient verification against unchanged candidate",
	"--max-attempts", "1", "--max-changed-lines", "0",
}

func sddEvidenceRetryFailsVerification(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	r.run(sddAttemptArgs(r, "begin", status.Revision, "bench-evidence-retry-begin", sddEvidenceRetryObjective...), false)
	if status, err = readRuntimeStatus(r); err != nil {
		return err
	}
	r.run(sddAttemptArgs(r, "finish", status.Revision, "bench-evidence-retry-fail",
		append([]string{"--outcome", "failed", "--evidence-revision", sddFailedEvidence}, sddTerminalEvidence...)...), false)
	failed, err := proveRuntime(r.sandbox)
	if err != nil {
		return err
	}
	if len(failed.Attempts) != 1 || failed.Attempts[0].Outcome != "failed" ||
		failed.Attempts[0].EvidenceRevision != sddFailedEvidence || failed.NextAction != "reset" {
		return fmt.Errorf("failed verification did not require an audited reset: %#v", failed)
	}
	return nil
}

func sddEvidenceRetryReset(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	r.run(sddAttemptArgs(r, "reset", status.Revision, "bench-evidence-retry-reset",
		"--reason", "maintainer authorized one unchanged-candidate retry after a transient unrelated failure",
		"--actor", "maintainer"), false)
	reset, err := proveRuntime(r.sandbox)
	if err != nil {
		return err
	}
	if reset.NextAction != "begin" || reset.EvidenceRevision != "" {
		return fmt.Errorf("audited reset did not admit the retry: %#v", reset)
	}
	return nil
}

func sddEvidenceRetrySettlesUnchanged(r *journeyRun) error {
	observation := r.run(append([]string{
		"sdd-attempt", "acquire", "--cwd", r.sandbox.Repo, "--change", sddChange,
		"--request-id", "bench-evidence-retry-acquire",
	}, sddEvidenceRetryObjective...), false)
	var acquired sddCompactAttemptResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(observation.Stdout)), &acquired); err != nil || observation.ExitCode != 0 || acquired.State != "proceed" || acquired.Token == "" {
		return fmt.Errorf("acquire unchanged-candidate retry = %#v exit=%d parse=%v", acquired, observation.ExitCode, err)
	}
	observation = r.run(append([]string{
		"sdd-attempt", "settle", "--cwd", r.sandbox.Repo, "--change", sddChange,
		"--token", acquired.Token, "--request-id", "bench-evidence-retry-settle",
		"--outcome", "passed", "--evidence-revision", sddCorrectedEvidence,
		"--remediates-evidence-revision", sddFailedEvidence,
	}, sddTerminalEvidence...), false)
	var settled sddCompactAttemptResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(observation.Stdout)), &settled); err != nil || observation.ExitCode != 0 || settled.State != "complete" {
		return fmt.Errorf("settle unchanged-candidate retry = %#v exit=%d parse=%v stderr=%s", settled, observation.ExitCode, err, firstLine(observation.Stderr))
	}
	status, err := proveRuntime(r.sandbox)
	if err != nil {
		return err
	}
	if !status.Complete || status.Binding != nil || len(status.Attempts) != 2 {
		return fmt.Errorf("evidence-only retry lost its failed-evidence link or invented review authority: %#v", status)
	}
	last := status.Attempts[1]
	if last.Outcome != "passed" || last.RemediatesEvidenceRevision != sddFailedEvidence ||
		last.BeginCandidateTree == "" || last.BeginCandidateTree != last.FinishCandidateTree {
		return fmt.Errorf("evidence-only retry did not settle against the unchanged candidate: %#v", last)
	}
	return nil
}

func sddEvidenceRetryJourneys() []Journey {
	return []Journey{{
		ID:     "j65-sdd-evidence-only-retry-after-audited-reset",
		Title:  "An audited reset admits one successful verification retry against the unchanged candidate",
		Source: "https://github.com/Gentleman-Programming/gentle-ai/issues/2621",
		// Issue #2621: the audited reset authorizes one evidence-only retry
		// when the failure was transient and unrelated to the candidate. The pass
		// must preserve that failed-evidence link without inventing review authority.
		Steps: []Step{
			{Name: "fixture: completed change with failed verification evidence", Fixture: sddPlanningArtifacts(sddFailedVerifyReport)},
			{Name: "mode disable", Requires: modeCapability, Args: productArgs("review", "mode", "disable", "--json")},
			{Name: "verification fails and exhausts its objective", Requires: sddAttemptBeginCapability, Composite: sddEvidenceRetryFailsVerification},
			{Name: "maintainer-audited reset authorizes one retry", Requires: sddAttemptResetCapability, Composite: sddEvidenceRetryReset},
			{Name: "unchanged-candidate retry settles and links failed evidence", Requires: sddAttemptSettleCapability, Composite: sddEvidenceRetrySettlesUnchanged},
		},
	}}
}
