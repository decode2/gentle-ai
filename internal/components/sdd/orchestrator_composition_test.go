package sdd

import (
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestCanonicalCompositionPreservesHistoricalOrchestratorBytes(t *testing.T) {
	for _, agent := range catalog.AllAgents() {
		t.Run(string(agent.ID), func(t *testing.T) {
			path := sddOrchestratorAsset(agent.ID)
			before := renderBoundedReviewAsset(agent.ID, path)
			after := composeOrchestratorPrompt(agent.ID)
			if after != before {
				t.Fatalf("canonical composition changed %s orchestrator bytes", agent.ID)
			}
		})
	}
}

func TestCanonicalCompositionFeedsBaseAndNamedProfile(t *testing.T) {
	canonical := composeOrchestratorPrompt(model.AgentOpenCode)
	if got := renderSDDOrchestratorAsset(model.AgentOpenCode); got != canonical {
		t.Fatal("default OpenCode rendering bypassed canonical composition")
	}

	profile, err := buildProfileOrchestratorPrompt(model.Profile{Name: "rapid"})
	if err != nil {
		t.Fatalf("buildProfileOrchestratorPrompt() error = %v", err)
	}
	for _, marker := range []string{
		"### Lossless Blocking Prompts (MANDATORY)",
		"### Native SDD Dispatcher Guard",
		"### SDD Session Preflight (HARD GATE)",
		"#### Review Execution Contract",
	} {
		if strings.Count(profile, marker) != 1 {
			t.Fatalf("named profile marker %q count = %d, want 1", marker, strings.Count(profile, marker))
		}
		if !strings.Contains(canonical, marker) {
			t.Fatalf("canonical OpenCode composition missing profile marker %q", marker)
		}
	}
}

func TestCanonicalCompositionHasNoProviderAddendumOrDuplicateSections(t *testing.T) {
	if got := resolveOrchestratorProviderAddendum(model.AgentOpenCode); got != "" {
		t.Fatalf("OpenCode provider addendum = %q, want empty for lossless slice", got)
	}
	if got := resolveOrchestratorProviderAddendum(model.AgentKilocode); got != "" {
		t.Fatalf("Kilocode provider addendum = %q, want empty baseline", got)
	}

	withAddendum := appendOrchestratorProviderAddendum("base", "future addendum")
	withAddendum = appendOrchestratorProviderAddendum(withAddendum, "future addendum")
	if strings.Count(withAddendum, openCodeBackgroundAddendumMarker) != 1 {
		t.Fatalf("provider addendum marker count = %d, want 1", strings.Count(withAddendum, openCodeBackgroundAddendumMarker))
	}

	for _, agent := range []model.AgentID{model.AgentOpenCode, model.AgentKilocode} {
		content := composeOrchestratorPrompt(agent)
		if strings.Contains(content, openCodeBackgroundAddendumMarker) {
			t.Fatalf("%s inherited the reserved OpenCode background addendum marker", agent)
		}
		for _, marker := range []string{
			"<!-- gentle-ai:sdd-model-assignments -->",
			"<!-- /gentle-ai:sdd-model-assignments -->",
			"#### Review Execution Contract",
		} {
			if count := strings.Count(content, marker); count != 1 {
				t.Fatalf("%s marker %q count = %d, want 1", agent, marker, count)
			}
		}
	}
}
