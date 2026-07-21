package sddstatus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestBindApprovedReviewRejectsInvalidChangeBeforePublishing(t *testing.T) {
	if _, err := BindApprovedReview(context.Background(), t.TempDir(), "../escape", "approved", ""); err == nil {
		t.Fatal("traversal change name was accepted")
	}
}

func TestBindApprovedReviewCASAndLiveEvidence(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	binding, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", "")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Schema != reviewBindingSchema || binding.AuthorityRevision == "" || binding.ReceiptHash == "" || binding.GateContext.Gate != "post-apply" {
		t.Fatalf("binding = %#v", binding)
	}
	if _, err := BindApprovedReview(context.Background(), filepath.Join(root, "openspec", "changes", "thin"), "thin", "approved-thin", ""); err != nil {
		t.Fatalf("exact retry with original empty expected revision: %v", err)
	}
	if _, err := BindApprovedReview(context.Background(), root, "thin", "other", binding.Revision); err == nil {
		t.Fatal("conflicting lineage accepted")
	}
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", "sha256:deadbeef"); err != nil {
		t.Fatalf("identical candidate retry must precede expected revision conflict: %v", err)
	}
	store, _ := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
	path := bindingPath(store, "thin")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("common-dir binding: %v", err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", binding.Revision); err == nil {
		t.Fatal("corrupt binding accepted")
	}
	if err := os.WriteFile(filepath.Join(changeRoot, "tasks.md"), []byte("- [x] 1.1 Done\n# drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err == nil {
		t.Fatal("working-tree drift bound authority")
	}
}

func TestBindApprovedReviewUsesNestedOpenSpecPlanningWorkspace(t *testing.T) {
	root := t.TempDir()
	planningRoot := filepath.Join(root, "packages", "app")
	changeRoot := seedReadyChange(t, planningRoot, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")

	binding, err := BindApprovedReview(context.Background(), planningRoot, "thin", "approved-thin", "")
	if err != nil {
		t.Fatal(err)
	}
	deeperPath := filepath.Join(planningRoot, "src", "feature")
	if err := os.MkdirAll(deeperPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BindApprovedReview(context.Background(), deeperPath, "thin", "approved-thin", binding.Revision); err != nil {
		t.Fatalf("bind from deeper package path: %v", err)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bindingPath(store, "thin")); err != nil {
		t.Fatalf("binding was not stored in the repository common dir: %v", err)
	}
	status, err := Resolve(ResolveOptions{CWD: planningRoot, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.PlanningHome.Path != filepath.Join(planningRoot, "openspec") || status.ReviewGate == nil || status.ReviewGate.Result != reviewtransaction.GateAllow {
		t.Fatalf("nested planning status did not consume canonical binding: %#v", status)
	}
}

func TestBindApprovedReviewRejectsAmbiguousPlanningChanges(t *testing.T) {
	for _, tt := range []struct {
		name string
		seed func(t *testing.T, root, planningRoot string)
	}{
		{name: "ancestor collision", seed: func(t *testing.T, root, planningRoot string) {
			seedReadyChange(t, root, "thin", "- [x] 1.1 Root\n")
			seedReadyChange(t, planningRoot, "thin", "- [x] 1.1 Package\n")
		}},
		{name: "sibling collision", seed: func(t *testing.T, root, planningRoot string) {
			seedReadyChange(t, planningRoot, "thin", "- [x] 1.1 App\n")
			seedReadyChange(t, filepath.Join(root, "packages", "api"), "thin", "- [x] 1.1 API\n")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			planningRoot := filepath.Join(root, "packages", "app")
			tt.seed(t, root, planningRoot)
			runSDDStatusGit(t, root, "init", "-q")

			if _, err := BindApprovedReview(context.Background(), planningRoot, "thin", "approved-thin", ""); err == nil || !strings.Contains(err.Error(), "ambiguous") {
				t.Fatalf("ambiguous planning changes error = %v", err)
			}
		})
	}
}

func TestBindApprovedReviewRejectsOpenSpecSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	planningRoot := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(planningRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	seedReadyChange(t, outside, "thin", "- [x] 1.1 Outside\n")
	if err := os.Symlink(filepath.Join(outside, "openspec"), filepath.Join(planningRoot, "openspec")); err != nil {
		t.Fatal(err)
	}
	runSDDStatusGit(t, root, "init", "-q")

	if _, err := BindApprovedReview(context.Background(), planningRoot, "thin", "approved-thin", ""); err == nil || !strings.Contains(err.Error(), "outside repository") {
		t.Fatalf("OpenSpec symlink escape error = %v", err)
	}
}

func TestBindApprovedReviewDoesNotFallBackPastNestedPlanningWorkspace(t *testing.T) {
	root := t.TempDir()
	planningRoot := filepath.Join(root, "packages", "app")
	seedReadyChange(t, root, "thin", "- [x] 1.1 Root\n")
	seedReadyChange(t, planningRoot, "other", "- [x] 1.1 Package\n")
	runSDDStatusGit(t, root, "init", "-q")

	if _, err := BindApprovedReview(context.Background(), planningRoot, "thin", "approved-thin", ""); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("nested planning workspace fallback error = %v", err)
	}
}

func TestBindApprovedReviewChecksNestedPlanningLedger(t *testing.T) {
	root := t.TempDir()
	planningRoot := filepath.Join(root, "packages", "app")
	changeRoot := seedReadyChange(t, planningRoot, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	write(t, filepath.Join(changeRoot, "reviews", "ledger.json"), `{"schema":"gentle-ai.review-ledger/v1","findings":[{"id":"wrong"}]}`)

	if _, err := BindApprovedReview(context.Background(), planningRoot, "thin", "approved-thin", ""); err == nil || !strings.Contains(err.Error(), "ledger does not equal") {
		t.Fatalf("nested planning ledger error = %v", err)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bindingPath(store, "thin")); !os.IsNotExist(err) {
		t.Fatalf("failed nested bind mutated canonical binding path: %v", err)
	}
}

func TestResolveConsumesOnlyAnExplicitValidBinding(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")

	withoutBinding, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if withoutBinding.NextRecommended != "verify" || withoutBinding.Dependencies.Verify != DependencyReady {
		t.Fatalf("unbound authority status = %#v", withoutBinding)
	}

	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	bound, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if bound.NextRecommended != "verify" || bound.Dependencies.Verify != DependencyReady || bound.Dependencies.Archive != DependencyBlocked || bound.ReviewGate == nil || bound.ReviewGate.Result != reviewtransaction.GateAllow {
		t.Fatalf("bound authority status = %#v", bound)
	}
}

func TestBindApprovedReviewRequiresTheSelectedCanonicalChange(t *testing.T) {
	for _, change := range []string{"../escape", "thin-", "thin--binding", strings.Repeat("a", 129)} {
		if _, err := BindApprovedReview(context.Background(), t.TempDir(), change, "approved", ""); err == nil {
			t.Fatalf("invalid change %q was accepted", change)
		}
	}
}

func TestValidateBoundReviewFailsClosedWhenFinalGateChanges(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	original := bindingFinalAuthorizationHook
	bindingFinalAuthorizationHook = func() { write(t, filepath.Join(changeRoot, "tasks.md"), "- [x] 1.1 Done\n# final gate drift\n") }
	t.Cleanup(func() { bindingFinalAuthorizationHook = original })
	if _, _, err := validateBoundReview(context.Background(), root, "thin"); err == nil {
		t.Fatal("final live gate mutation was accepted")
	}
}

func TestValidateBoundReviewFailsClosedForFinalAuthorityArtifacts(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, store reviewtransaction.CompactStore)
	}{
		{name: "receipt bytes", mutate: func(t *testing.T, store reviewtransaction.CompactStore) {
			if err := os.WriteFile(store.ReceiptPath(), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "authority state", mutate: func(t *testing.T, store reviewtransaction.CompactStore) {
			if err := os.WriteFile(store.StatePath(), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "binding bytes", mutate: func(t *testing.T, store reviewtransaction.CompactStore) {
			if err := os.WriteFile(bindingPath(store, "thin"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
			writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
			if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
				t.Fatal(err)
			}
			store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
			if err != nil {
				t.Fatal(err)
			}
			original := bindingFinalAuthorizationHook
			bindingFinalAuthorizationHook = func() { tt.mutate(t, store) }
			t.Cleanup(func() { bindingFinalAuthorizationHook = original })
			if _, _, err := validateBoundReview(context.Background(), root, "thin"); err == nil {
				t.Fatal("final artifact mutation was accepted")
			}
		})
	}
}

func TestBindingLockRejectsConcurrentWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binding.lock")
	first, err := acquireBindingLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	if second, err := acquireBindingLock(path); err == nil || second != nil {
		t.Fatalf("concurrent binding lock = %#v, %v", second, err)
	}
}

func TestBindApprovedReviewPreservesAuthorityAcrossBindingPublicationFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		inject func() func()
		want   string
	}{
		{name: "rename", want: "rename binding", inject: func() func() {
			original := bindingRename
			bindingRename = func(string, string) error { return errors.New("rename binding") }
			return func() { bindingRename = original }
		}},
		{name: "directory sync", want: "sync binding", inject: func() func() {
			original := syncBindingDirectory
			syncBindingDirectory = func(string) error { return errors.New("sync binding") }
			return func() { syncBindingDirectory = original }
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
			writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
			store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			restore := tt.inject()
			_, err = BindApprovedReview(context.Background(), root, "thin", "approved-thin", "")
			restore()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("binding publication error = %v, want %q", err, tt.want)
			}
			after, loadErr := store.Load()
			if loadErr != nil || after.Revision != before.Revision || !reflect.DeepEqual(after.State, before.State) {
				t.Fatalf("binding publication changed authority: before=%#v after=%#v error=%v", before, after, loadErr)
			}
			path := bindingPath(store, "thin")
			if tt.name == "rename" {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("failed rename published binding: %v", statErr)
				}
			} else if _, statErr := os.Stat(path); statErr != nil {
				t.Fatalf("post-rename sync failure lost published binding: %v", statErr)
			} else {
				original := syncBindingDirectory
				syncBindingDirectory = func(string) error { return errors.New("sync binding again") }
				_, retryErr := BindApprovedReview(context.Background(), root, "thin", "approved-thin", "")
				var publicationErr *ReviewBindingPublicationError
				if !errors.As(retryErr, &publicationErr) {
					t.Fatalf("repeated sync failure = %T %v, want ReviewBindingPublicationError", retryErr, retryErr)
				}
				syncs := 0
				syncBindingDirectory = func(string) error { syncs++; return nil }
				_, retryErr = BindApprovedReview(context.Background(), root, "thin", "approved-thin", "")
				syncBindingDirectory = original
				if retryErr != nil || syncs != 1 {
					t.Fatalf("binding retry did not repeat directory sync: syncs=%d err=%v", syncs, retryErr)
				}
			}
		})
	}
}

func TestBindingFailsClosedForLedgerDriftAndChangedLiveEvidence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, root, changeRoot string)
	}{
		{name: "mismatched external ledger", mutate: func(t *testing.T, _ string, changeRoot string) {
			if err := os.MkdirAll(filepath.Join(changeRoot, "reviews"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(changeRoot, "reviews", "ledger.json"), []byte(`{"schema":"gentle-ai.review-ledger/v1","findings":[{"id":"wrong"}]}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "staged candidate drift", mutate: func(t *testing.T, root, changeRoot string) {
			if err := os.WriteFile(filepath.Join(changeRoot, "tasks.md"), []byte("- [x] 1.1 Done\n# staged drift\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runSDDStatusGit(t, root, "add", "openspec/changes/thin/tasks.md")
		}},
		{name: "committed candidate drift", mutate: func(t *testing.T, root, changeRoot string) {
			if err := os.WriteFile(filepath.Join(changeRoot, "tasks.md"), []byte("- [x] 1.1 Done\n# committed drift\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runSDDStatusGit(t, root, "add", "openspec/changes/thin/tasks.md")
			runSDDStatusGit(t, root, "commit", "-qm", "drift")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
			writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
			tt.mutate(t, root, changeRoot)
			if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err == nil {
				t.Fatal("changed live evidence created a binding")
			}
			store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(bindingPath(store, "thin")); !os.IsNotExist(err) {
				t.Fatalf("failed bind mutated canonical path: %v", err)
			}
		})
	}
}

func TestResolveRejectsCorruptOrChangedBoundEvidence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, root string, store reviewtransaction.CompactStore, binding ReviewBinding)
	}{
		{name: "corrupt binding", mutate: func(t *testing.T, _ string, store reviewtransaction.CompactStore, _ ReviewBinding) {
			if err := os.WriteFile(bindingPath(store, "thin"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "changed receipt", mutate: func(t *testing.T, _ string, store reviewtransaction.CompactStore, _ ReviewBinding) {
			if err := os.WriteFile(store.ReceiptPath(), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
			writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
			binding, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", "")
			if err != nil {
				t.Fatal(err)
			}
			store, _ := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
			tt.mutate(t, root, store, binding)
			status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
			if err != nil {
				t.Fatal(err)
			}
			if status.NextRecommended != "resolve-review" || status.Dependencies.Verify != DependencyBlocked {
				t.Fatalf("%s status = %#v", tt.name, status)
			}
		})
	}
}

func TestBoundReviewUsesNormalVerifyThenArchiveRouting(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Binding\n#### Scenario: Exact authority\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("a"), "pass"))

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Verify != DependencyAllDone || status.Dependencies.Archive != DependencyReady || status.NextRecommended != "archive" || status.ReviewGate == nil || status.ReviewGate.Result != reviewtransaction.GateAllow {
		t.Fatalf("bound completed verification status = %#v", status)
	}
	store, _ := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
	if err := os.WriteFile(bindingPath(store, "thin"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Archive != DependencyBlocked || status.NextRecommended != "resolve-review" {
		t.Fatalf("corrupt completed binding status = %#v", status)
	}
}

func TestBoundReviewRoutesStaleVerifyEvidenceToVerify(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Binding\n#### Scenario: Exact authority\n#### Scenario: Added after verification\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("a"), "pass"))

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewGate == nil || status.ReviewGate.Result != reviewtransaction.GateAllow {
		t.Fatalf("ReviewGate = %#v, want allow", status.ReviewGate)
	}
	if len(status.BlockedReasons) != 0 {
		t.Fatalf("BlockedReasons = %v, want empty", status.BlockedReasons)
	}
	if status.Dependencies.Verify != DependencyReady || status.Dependencies.Archive != DependencyBlocked || status.NextRecommended != "verify" {
		t.Fatalf("verify=%q archive=%q next=%q, want ready/blocked/verify", status.Dependencies.Verify, status.Dependencies.Archive, status.NextRecommended)
	}
	if status.RemediationState != (RemediationState{}) {
		t.Fatalf("RemediationState = %#v, want empty for stale evidence", status.RemediationState)
	}
}

func TestBoundReviewGrantsCompactRemediationBudgetForFailedVerdictWithIncompleteScenarios(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Binding\n#### Scenario: Exact authority\n#### Scenario: Added after verification\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("a"), "fail"))

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Verify != DependencyBlocked || status.NextRecommended != "remediate" {
		t.Fatalf("verify=%q next=%q, want blocked/remediate for failed verdict", status.Dependencies.Verify, status.NextRecommended)
	}
	if !status.RemediationState.Required || status.RemediationState.CorrectionBudget <= 0 || status.RemediationState.LineageID != "approved-thin" || status.RemediationState.FailedEvidenceRevision != shaID("a") {
		t.Fatalf("RemediationState = %#v, want transaction-bound nonzero compact budget", status.RemediationState)
	}
}

func TestSelectedBindingSupersedesOnlyItsLegacyReviewAuthority(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Binding\n#### Scenario: Exact authority\n")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("a"), "pass"))
	writeApprovedReviewArtifacts(t, changeRoot)
	if err := os.Remove(filepath.Join(changeRoot, "verify-report.md")); err != nil {
		t.Fatal(err)
	}
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	legacy, err := reviewtransaction.AuthoritativeStore(context.Background(), root, "thin-lineage")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.LoadChain(); err != nil {
		t.Fatalf("binding removed or changed legacy authority: %v", err)
	}
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.NextRecommended != "verify" || status.Dependencies.Verify != DependencyReady || status.ReviewGate == nil || status.ReviewGate.Result != reviewtransaction.GateAllow {
		t.Fatalf("selected binding did not supersede only the selected legacy authority: %#v", status)
	}
}

func TestValidBindingDoesNotAdvanceIncompleteApply(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [ ] 1.1 Pending\n")
	writeApprovedCompactAuthorityForChangeWithTasks(t, root, changeRoot, "approved-thin", "- [ ] 1.1 Pending\n# approved compact scope\n")
	if _, err := BindApprovedReview(context.Background(), root, "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.ApplyState != ApplyReady || status.Dependencies.Apply != DependencyReady || status.Dependencies.Verify != DependencyBlocked || status.Dependencies.Archive != DependencyBlocked || status.NextRecommended != "apply" {
		t.Fatalf("incomplete bound status = %#v", status)
	}
}

func TestBindApprovedReviewSanitizesHostileGitEnvironmentFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "approved-thin")
	subdirectory := filepath.Join(root, "nested")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	hostile := t.TempDir()
	runSDDStatusGit(t, hostile, "init", "-q")
	for name, value := range map[string]string{
		"GIT_DIR":        filepath.Join(hostile, ".git"),
		"GIT_WORK_TREE":  hostile,
		"GIT_COMMON_DIR": filepath.Join(hostile, ".git"),
		"GIT_INDEX_FILE": filepath.Join(hostile, ".git", "index"),
	} {
		t.Setenv(name, value)
	}
	t.Chdir(root)
	if _, err := BindApprovedReview(context.Background(), "nested", "thin", "approved-thin", ""); err != nil {
		t.Fatal(err)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "approved-thin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bindingPath(store, "thin")); err != nil {
		t.Fatalf("binding was not stored in the selected repository common dir: %v", err)
	}
}

func TestBindApprovedReviewPiBridge(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Binding\n#### Scenario: Exact authority\n")
	writeApprovedPiCompactAuthorityForChange(t, root, changeRoot, "pi-approved-thin")

	binding, err := BindApprovedReview(context.Background(), root, "thin", "pi-approved-thin", "")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Schema != reviewBindingSchema || binding.Lineage != "pi-approved-thin" || binding.ReceiptHash == "" {
		t.Fatalf("invalid binding: %#v", binding)
	}

	// Idempotency: replaying doesn't fail
	if _, err := BindApprovedReview(context.Background(), root, "thin", "pi-approved-thin", binding.Revision); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}

	// Conflict validation: changing the base/candidate tree in Pi receipt makes it fail
	piStoreRoot := filepath.Join(root, ".git", "gentle-ai", "reviews", "compact-v2", "pi-approved-thin")
	piReceiptBytes, err := os.ReadFile(filepath.Join(piStoreRoot, "review-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var piReceipt map[string]interface{}
	if err := json.Unmarshal(piReceiptBytes, &piReceipt); err == nil {
		if body, ok := piReceipt["body"].(map[string]interface{}); ok {
			body["final_candidate_tree"] = "0000000000000000000000000000000000000000"
		}
		corruptedBytes, _ := json.MarshalIndent(piReceipt, "", "  ")
		_ = os.WriteFile(filepath.Join(piStoreRoot, "review-receipt.json"), corruptedBytes, 0o644)
	}

	// Since we corrupted the Pi files, if we run it again with a new expected revision or if we attempt to re-bridge a conflict, it should fail
	// Let's clear the native Go store to force re-bridging
	_ = os.RemoveAll(filepath.Join(root, ".git", "gentle-ai", "review-transactions", "v2", "pi-approved-thin"))
	if _, err := BindApprovedReview(context.Background(), root, "thin", "pi-approved-thin", ""); err == nil {
		t.Fatal("expected conflict or validation failure on corrupted Pi receipt, but succeeded")
	}
}

func runGitCommandOutput(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func writeApprovedPiCompactAuthorityForChange(t *testing.T, repo, changeRoot, lineage string) {
	t.Helper()
	runSDDStatusGit(t, repo, "init", "-q")
	runSDDStatusGit(t, repo, "config", "user.email", "status@example.com")
	runSDDStatusGit(t, repo, "config", "user.name", "Status Test")
	runSDDStatusGit(t, repo, "add", ".")
	runSDDStatusGit(t, repo, "commit", "-qm", "base")
	write(t, filepath.Join(changeRoot, "tasks.md"), "- [x] 1.1 Done\n# approved compact scope\n")

	snapshot, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).Build(context.Background(), reviewtransaction.Target{Kind: reviewtransaction.TargetCurrentChanges, IntendedUntracked: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	risk, lines, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).ClassifySnapshotRisk(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	budget := lines/2 + lines%2
	if budget > 200 {
		budget = 200
	}

	lenses := []string{}
	if risk == reviewtransaction.RiskMedium {
		lenses = []string{string(reviewtransaction.LensReliability)}
	} else if risk == reviewtransaction.RiskHigh {
		lenses = []string{string(reviewtransaction.LensRisk), string(reviewtransaction.LensResilience), string(reviewtransaction.LensReadability), string(reviewtransaction.LensReliability)}
	}

	results := []interface{}{}
	for _, lens := range lenses {
		results = append(results, map[string]interface{}{
			"lens":     lens,
			"findings": []interface{}{},
			"evidence": []string{"review complete"},
		})
	}

	commonDirOutput, err := runGitCommandOutput(context.Background(), repo, "rev-parse", "--git-common-dir")
	if err != nil {
		t.Fatal(err)
	}
	commonDir := strings.TrimSpace(commonDirOutput)
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	commonDir, _ = filepath.Abs(commonDir)

	piStoreRoot := filepath.Join(commonDir, "gentle-ai", "reviews", "compact-v2", lineage)
	if err := os.MkdirAll(piStoreRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	piState := map[string]interface{}{
		"schema":     "gentle-ai.review-state/v2",
		"lineage_id": lineage,
		"generation": 1,
		"mode":       "ordinary",
		"state":      "approved",
		"initial_snapshot": map[string]interface{}{
			"schema":                 "gentle-ai.review-snapshot/v1",
			"mode":                   "ordinary",
			"repository_root":        repo,
			"base_tree":              snapshot.BaseTree,
			"complete_snapshot_tree": snapshot.CandidateTree,
			"review_projection": map[string]interface{}{
				"kind": "complete",
			},
			"initial_review_tree": snapshot.CandidateTree,
			"genesis_paths":       snapshot.Paths,
			"intended_untracked":  []string{},
			"diff_evidence": map[string]interface{}{
				"event":        "commit",
				"changedLines": lines,
				"triviality":   "non-trivial",
			},
			"route":                  "verify",
			"lenses":                 lenses,
			"risk_tier":              string(risk),
			"original_changed_lines": lines,
			"correction_budget":      budget,
			"policy_hash":            shaID("c"),
			"object_store": map[string]interface{}{
				"snapshot_directory":         ".git/gentle-ai/reviews",
				"object_directory":           ".git/objects",
				"alternate_object_directory": "",
				"metadata_path":              "",
				"sensitivity":                "workspace-content",
			},
		},
		"current_candidate_tree": snapshot.CandidateTree,
		"genesis_paths":          snapshot.Paths,
		"intended_untracked":     []string{},
		"policy_hash":            shaID("c"),
		"risk_tier":              string(risk),
		"selected_lenses":        lenses,
		"original_changed_lines": lines,
		"correction_budget":      budget,
		"lens_results":           results,
		"findings":               []interface{}{},
		"outcomes":               map[string]string{},
		"correction_ids":         []string{},
		"follow_ups":             []interface{}{},
		"runtime_identity": map[string]interface{}{
			"schema":             "gentle-ai.review-runtime-identity/v1",
			"compact_contract":   "v2",
			"operation_contract": "v2",
			"state_schema":       "gentle-ai.review-state/v2",
			"record_schema":      "gentle-ai.review-state-record/v2",
			"receipt_schema":     "gentle-ai.review-receipt/v2",
			"canonicalization":   "canonical-json/v1",
			"identity_hash":      "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
		"escalation_reasons": []string{},
	}

	piRecord := map[string]interface{}{
		"schema":   "gentle-ai.review-state-record/v2",
		"revision": "ec089d36258560776b4969ca2ee7eee3a1463e43b98acf0556b9d325d1ab465a",
		"state":    piState,
	}

	piRecordBytes, err := json.MarshalIndent(piRecord, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piStoreRoot, "review-state.json"), piRecordBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	piReceipt := map[string]interface{}{
		"body": map[string]interface{}{
			"schema":                  "gentle-ai.review-receipt-body/v2",
			"lineage_id":              lineage,
			"generation":              1,
			"authority_revision":      "ec089d36258560776b4969ca2ee7eee3a1463e43b98acf0556b9d325d1ab465a",
			"base_tree":               snapshot.BaseTree,
			"initial_review_tree":     snapshot.CandidateTree,
			"final_candidate_tree":    snapshot.CandidateTree,
			"genesis_paths_hash":      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"intended_untracked_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"policy_hash":             shaID("c"),
			"risk_tier":               string(risk),
			"selected_lenses":         lenses,
			"original_changed_lines":  lines,
			"correction_budget":       budget,
			"correction_ids":          []string{},
			"fix_diff_hash":           "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"evidence_hash":           "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"terminal_state":          "approved",
		},
		"receipt_hash": "d2383394d076ee3b15d80d7d124e45840bfdd2a3e7b2dd29446f69077eb0073b",
	}

	piReceiptBytes, err := json.MarshalIndent(piReceipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piStoreRoot, "review-receipt.json"), piReceiptBytes, 0o644); err != nil {
		t.Fatal(err)
	}
}
