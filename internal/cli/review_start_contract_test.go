package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestNegotiatedReviewStartMatchesVersionedFixture(t *testing.T) {
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "scripts/deploy.sh", "echo deploy\n", 0o644)

	var output bytes.Buffer
	if err := RunReview([]string{
		"start", "--contract", ReviewIntegrationContractV1, "--cwd", repo, "--lineage", "review-start-fixture",
	}, &output); err != nil {
		t.Fatal(err)
	}
	result := decodeNegotiatedReviewStart(t, output.Bytes())
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	wantReasons := []reviewtransaction.RiskReason{{
		Code: reviewtransaction.RiskReasonShellSource, Signal: reviewtransaction.SignalShellProcess, Path: "scripts/deploy.sh",
	}}
	wantLenses := []string{
		reviewtransaction.LensRisk, reviewtransaction.LensResilience,
		reviewtransaction.LensReadability, reviewtransaction.LensReliability,
	}
	if result.Schema != ReviewIntegrationStartSchema || result.Contract != ReviewIntegrationContractV1 ||
		result.Operation != "review.start" || result.Action != "created" || !result.LensesRequired ||
		result.LineageID != "review-start-fixture" || result.State != reviewtransaction.StateReviewing ||
		result.RiskLevel != reviewtransaction.RiskHigh || !reflect.DeepEqual(result.SelectedLenses, wantLenses) ||
		result.Projection != reviewtransaction.ProjectionWorkspace || result.ChangedFiles != 1 ||
		result.ChangedLines != 1 || result.CorrectionBudget != 1 || !reflect.DeepEqual(result.RiskReasons, wantReasons) {
		t.Fatalf("negotiated START = %#v\n%s", result, output.String())
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "contracts", "review-integration", "v1", "fixtures", "start.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), fixture) {
		t.Fatalf("START fixture mismatch:\ngot=%s\nwant=%s", output.String(), fixture)
	}
}

func TestNegotiatedReviewStartRiskReasonsUseOnlyImmutableSnapshotEvidence(t *testing.T) {
	t.Run("mode-only executable transition", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Git worktree executable-bit transitions are POSIX-only")
		}
		repo := initReviewCLIRepo(t)
		if runtime.GOOS != "windows" {
			if err := os.Chmod(filepath.Join(repo, "tracked.txt"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		result := runNegotiatedReviewStart(t, repo, "review-start-mode")
		want := []reviewtransaction.RiskReason{{
			Code: reviewtransaction.RiskReasonExecutableMode, Signal: reviewtransaction.SignalPermissions,
			Path: "tracked.txt", OldMode: "100644", NewMode: "100755",
		}}
		if result.RiskLevel != reviewtransaction.RiskHigh || result.ChangedLines != 0 ||
			!reflect.DeepEqual(result.RiskReasons, want) {
			t.Fatalf("mode-only negotiated START = %#v", result)
		}
	})

	t.Run("canonical stable sorting", func(t *testing.T) {
		repo := initReviewCLIRepo(t)
		if err := os.Chmod(filepath.Join(repo, "tracked.txt"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeReviewStartCandidate(t, repo, "scripts/deploy.sh", "echo deploy\n", 0o644)
		writeReviewStartCandidate(t, repo, "security/check.go", "package security\n", 0o644)
		result := runNegotiatedReviewStart(t, repo, "review-start-sorted")
		want := []reviewtransaction.RiskReason{
			{Code: reviewtransaction.RiskReasonHotPath, Signal: reviewtransaction.SignalSecurity, Path: "security/check.go"},
			{Code: reviewtransaction.RiskReasonShellSource, Signal: reviewtransaction.SignalShellProcess, Path: "scripts/deploy.sh"},
		}
		if runtime.GOOS != "windows" {
			want = append([]reviewtransaction.RiskReason{{Code: reviewtransaction.RiskReasonExecutableMode, Signal: reviewtransaction.SignalPermissions, Path: "tracked.txt", OldMode: "100644", NewMode: "100755"}}, want...)
		}
		if !reflect.DeepEqual(result.RiskReasons, want) {
			t.Fatalf("canonical risk reasons = %#v, want %#v", result.RiskReasons, want)
		}
	})

	t.Run("semantic filename near misses", func(t *testing.T) {
		repo := initReviewCLIRepo(t)
		writeReviewStartCandidate(t, repo, "internal/model-provider-profile-data-exposure-data-loss.go", "package internal\n", 0o644)
		writeReviewStartCandidate(t, repo, "scripts/deploy.sh.txt", "not shell source\n", 0o644)
		writeReviewStartCandidate(t, repo, "tokens/service-tokenizer.go", "package tokens\n", 0o644)
		result := runNegotiatedReviewStart(t, repo, "review-start-near-miss")
		if result.RiskLevel != reviewtransaction.RiskMedium ||
			!reflect.DeepEqual(result.SelectedLenses, []string{reviewtransaction.LensReliability}) {
			t.Fatalf("near-miss tier/lenses = %q/%v", result.RiskLevel, result.SelectedLenses)
		}
		for _, reason := range result.RiskReasons {
			if reason.Signal == reviewtransaction.SignalDataExposure || reason.Signal == reviewtransaction.SignalDataLoss ||
				reason.Code == reviewtransaction.RiskReasonShellSource || reason.Code == reviewtransaction.RiskReasonServiceToken {
				t.Fatalf("near-miss filename created semantic reason %#v", reason)
			}
		}
		payload, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(payload, &document); err != nil {
			t.Fatal(err)
		}
		forbidden := map[string]struct{}{"model": {}, "provider": {}, "profile": {}}
		if field := findCapabilityForbiddenField(document, forbidden); field != "" {
			t.Fatalf("negotiated START exposed classifier input field %q", field)
		}
	})
}

func TestNegotiatedReviewStartRoutesLargePureDocumentationToReadability(t *testing.T) {
	full4R := []string{
		reviewtransaction.LensRisk, reviewtransaction.LensResilience,
		reviewtransaction.LensReadability, reviewtransaction.LensReliability,
	}
	tests := []struct {
		name       string
		path       string
		lines      int
		prefix     string
		focus      string
		wantRisk   reviewtransaction.RiskLevel
		wantLenses []string
	}{
		{name: "400 pure doc lines remain low", path: "docs/guide.md", lines: 400, wantRisk: reviewtransaction.RiskLow, wantLenses: []string{}},
		{name: "401 pure doc lines select readability", path: "docs/guide.md", lines: 401, focus: "risk", wantRisk: reviewtransaction.RiskMedium, wantLenses: []string{reviewtransaction.LensReadability}},
		{name: "401 static MDX lines select readability", path: "book/chapter.mdx", lines: 401, wantRisk: reviewtransaction.RiskMedium, wantLenses: []string{reviewtransaction.LensReadability}},
		{name: "active MDX keeps high routing", path: "book/chapter.mdx", lines: 400, prefix: "import Widget from './widget'\n", wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "SVG keeps high routing", path: "docs/diagram.svg", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "semantic doc path keeps high routing", path: "docs/security/guide.md", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "prompt markdown keeps normal large routing", path: "prompts/system.md", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "compound prompt filename keeps normal large routing", path: "docs/system-prompt.md", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "agent rules keep normal large routing", path: "AGENTS.md", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "workflow markdown keeps normal large routing", path: ".github/workflows/release.md", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "runtime docs keep normal large routing", path: "runtime/README.md", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "configuration keeps normal large routing", path: "config/settings.yaml", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
		{name: "code keeps normal large routing", path: "internal/app.go", lines: 401, wantRisk: reviewtransaction.RiskHigh, wantLenses: full4R},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := initReviewCLIRepo(t)
			writeReviewStartCandidate(t, repo, tt.path, tt.prefix+strings.Repeat("line\n", tt.lines), 0o644)
			args := []string{
				"start", "--contract", ReviewIntegrationContractV1, "--cwd", repo,
				"--lineage", fmt.Sprintf("large-doc-routing-%d", index),
			}
			if tt.focus != "" {
				args = append(args, "--focus", tt.focus)
			}
			var output bytes.Buffer
			if err := RunReview(args, &output); err != nil {
				t.Fatal(err)
			}
			result := decodeNegotiatedReviewStart(t, output.Bytes())
			wantLines := tt.lines + strings.Count(tt.prefix, "\n")
			if result.RiskLevel != tt.wantRisk || result.ChangedLines != wantLines || !reflect.DeepEqual(result.SelectedLenses, tt.wantLenses) {
				t.Fatalf("routing = risk %q, lines %d, lenses %v; want %q, %d, %v", result.RiskLevel, result.ChangedLines, result.SelectedLenses, tt.wantRisk, wantLines, tt.wantLenses)
			}
		})
	}

	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/guide.md", strings.Repeat("line\n", 401), 0o644)
	err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", "large-doc-invalid-focus", "--focus", "unknown"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unsupported review focus") {
		t.Fatalf("large pure documentation invalid focus error = %v", err)
	}
}

func TestNegotiatedReviewStartPreservesPrePolicyLargeDocumentationAuthority(t *testing.T) {
	full4R := []string{
		reviewtransaction.LensRisk, reviewtransaction.LensResilience,
		reviewtransaction.LensReadability, reviewtransaction.LensReliability,
	}
	legacy := ReviewFacadeStartResult{RiskLevel: reviewtransaction.RiskHigh, SelectedLenses: full4R, ChangedLines: 401}
	assessment := reviewtransaction.RiskAssessment{
		Level: reviewtransaction.RiskMedium, ChangedLines: 401, DominantLens: reviewtransaction.LensReadability,
		Reasons: []reviewtransaction.RiskReason{{Code: reviewtransaction.RiskReasonLargeChange}, {Code: reviewtransaction.RiskReasonNonExecutableOnly}},
	}
	aligned, err := reviewStartAssessmentForFrozenAuthority(legacy, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if aligned.Level != reviewtransaction.RiskHigh || aligned.DominantLens != "" ||
		!reflect.DeepEqual(aligned.Reasons, []reviewtransaction.RiskReason{{Code: reviewtransaction.RiskReasonLargeChange}}) {
		t.Fatalf("pre-policy aligned assessment = %#v", aligned)
	}
}

func TestNegotiatedReviewStartPreservesLegacyPayloadAndAuthorityIdentity(t *testing.T) {
	legacyRepo := initReviewCLIRepo(t)
	negotiatedRepo := initReviewCLIRepo(t)
	for _, repo := range []string{legacyRepo, negotiatedRepo} {
		if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lineage := "review-start-authority-parity"
	var legacyOutput bytes.Buffer
	if err := RunReview([]string{"start", "--cwd", legacyRepo, "--lineage", lineage}, &legacyOutput); err != nil {
		t.Fatal(err)
	}
	var legacyFields map[string]json.RawMessage
	if err := json.Unmarshal(legacyOutput.Bytes(), &legacyFields); err != nil {
		t.Fatal(err)
	}
	gotFields := make([]string, 0, len(legacyFields))
	for field := range legacyFields {
		gotFields = append(gotFields, field)
	}
	sortStrings(gotFields)
	wantFields := []string{
		"action", "changed_files", "changed_lines", "correction_budget", "lenses_required", "lineage_id",
		"operation", "projection", "risk_level", "selected_lenses", "state",
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("unnegotiated START fields = %v, want %v\n%s", gotFields, wantFields, legacyOutput.String())
	}
	var legacy ReviewFacadeStartResult
	decoder := json.NewDecoder(bytes.NewReader(legacyOutput.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Operation != "review/start" {
		t.Fatalf("legacy operation = %q", legacy.Operation)
	}

	var negotiatedOutput bytes.Buffer
	if err := RunReview([]string{
		"start", "--contract", ReviewIntegrationContractV1, "--cwd", negotiatedRepo, "--lineage", lineage,
	}, &negotiatedOutput); err != nil {
		t.Fatal(err)
	}
	negotiated := decodeNegotiatedReviewStart(t, negotiatedOutput.Bytes())
	if negotiated.Operation != "review.start" || negotiated.Contract != ReviewIntegrationContractV1 {
		t.Fatalf("negotiated identity = %#v", negotiated)
	}

	legacyStore, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), legacyRepo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	negotiatedStore, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), negotiatedRepo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	legacyAuthority, err := os.ReadFile(legacyStore.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	negotiatedAuthority, err := os.ReadFile(negotiatedStore.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyAuthority, negotiatedAuthority) {
		t.Fatalf("contract negotiation changed compact authority bytes:\nlegacy=%s\nnegotiated=%s", legacyAuthority, negotiatedAuthority)
	}
	for _, path := range []string{legacyStore.ReceiptPath(), negotiatedStore.ReceiptPath()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("START unexpectedly materialized receipt %q: %v", path, err)
		}
	}
}

func TestNegotiatedReviewStartRejectsInvalidContractsBeforeAuthorityMutation(t *testing.T) {
	for _, contract := range []string{"", "gentle-ai.review-integration/v2", " " + ReviewIntegrationContractV1} {
		t.Run(strings.ReplaceAll(contract, "/", "_"), func(t *testing.T) {
			repo := initReviewCLIRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			contractArg := "--contract=" + contract
			err := RunReview([]string{"start", contractArg, "--cwd", repo, "--lineage", "review-invalid-contract"}, &output)
			if err == nil {
				t.Fatalf("contract %q result = %q, %v", contract, output.String(), err)
			}
			failure := decodeReviewIntegrationFailure(t, output.Bytes())
			if failure.MutationOutcome != ReviewMutationNotStarted {
				t.Fatalf("contract %q failure = %#v", contract, failure)
			}
			stores, discoverErr := reviewtransaction.DiscoverCompactStores(context.Background(), repo)
			if discoverErr != nil {
				t.Fatal(discoverErr)
			}
			if len(stores) != 0 {
				t.Fatalf("invalid contract created authority stores: %#v", stores)
			}
		})
	}
}

func TestNegotiatedReviewStartSchemaAndFixtureAreStrict(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "review-integration", "v1")
	schemaPayload, err := os.ReadFile(filepath.Join(root, "schemas", "start.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaPayload, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" ||
		schema["$id"] != ReviewIntegrationStartSchemaID || schema["additionalProperties"] != false {
		t.Fatalf("START schema header = %#v", schema)
	}
	fixture, err := os.ReadFile(filepath.Join(root, "fixtures", "start.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeNegotiatedReviewStart(t, fixture)
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(fixture, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	malformed, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(malformed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ReviewIntegrationStartResult{}); err == nil {
		t.Fatal("strict negotiated START decoder accepted unknown top-level field")
	}
}

func runNegotiatedReviewStart(t *testing.T, repo, lineage string) ReviewIntegrationStartResult {
	t.Helper()
	var output bytes.Buffer
	if err := RunReview([]string{
		"start", "--contract", ReviewIntegrationContractV1, "--cwd", repo, "--lineage", lineage,
	}, &output); err != nil {
		t.Fatal(err)
	}
	result := decodeNegotiatedReviewStart(t, output.Bytes())
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	return result
}

func decodeNegotiatedReviewStart(t *testing.T, payload []byte) ReviewIntegrationStartResult {
	t.Helper()
	var result ReviewIntegrationStartResult
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func writeReviewStartCandidate(t *testing.T, repo, path, contents string, mode os.FileMode) {
	t.Helper()
	fullPath := filepath.Join(repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}
