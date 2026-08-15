package opencode

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestResolveDirectRoleModels(t *testing.T) {
	base := DirectRoleModelRequest{Catalog: map[string]Provider{
		"a": {ID: "a", Models: map[string]Model{
			"reason":    {ID: "reason", ToolCall: true, Reasoning: true, Limit: ModelLimit{Context: 100, Output: 10}},
			"plain":     {ID: "plain", ToolCall: true, Limit: ModelLimit{Context: 200, Output: 20}},
			"tools-off": {ID: "tools-off", Reasoning: true},
		}},
		"b": {ID: "b", Models: map[string]Model{"wide": {ID: "wide", ToolCall: true, Reasoning: true, Limit: ModelLimit{Context: 200, Output: 10}}}},
	}, AvailableProviders: map[string]bool{"a": true, "b": true}}
	cases := []struct {
		name, role, wantSource, wantReason, wantModel string
		request                                       DirectRoleModelRequest
	}{
		{"explicit known valid wins without auth", GentleReviewerAgent, "explicit", "explicit-verified-catalog-assignment", "a/plain", withExplicit(base, GentleReviewerAgent, model.ModelAssignment{ProviderID: "a", ModelID: "plain"})},
		{"explicit unknown provider remains exact without discovery", GentleReviewerAgent, "explicit", "explicit-unverified-assignment", "openai/gpt-5-mini", withExplicit(DirectRoleModelRequest{}, GentleReviewerAgent, model.ModelAssignment{ProviderID: "openai", ModelID: "gpt-5-mini"})},
		{"incomplete falls through", GentleReviewerAgent, "catalog-default", "catalog-ranked-eligible-model", "b/wide", withExplicit(base, GentleReviewerAgent, model.ModelAssignment{ProviderID: "a"})},
		{"malformed falls through", GentleReviewerAgent, "catalog-default", "catalog-ranked-eligible-model", "b/wide", withExplicit(base, GentleReviewerAgent, model.ModelAssignment{ProviderID: " a", ModelID: "plain"})},
		{"known invalid explicit falls through semantic policy", GentleReviewerAgent, "semantic-policy", "semantic-policy-ranked-candidate", "a/reason", withCandidates(withExplicit(base, GentleReviewerAgent, model.ModelAssignment{ProviderID: "a", ModelID: "missing"}), GentleReviewerAgent, "a/reason")},
		{"reviewer requires reasoning", GentleReviewerAgent, "semantic-policy", "semantic-policy-ranked-candidate", "a/reason", withCandidates(base, GentleReviewerAgent, "a/plain", "a/reason")},
		{"worker prefers mid", GentleWorkerAgent, "semantic-policy", "semantic-policy-ranked-candidate", "a/plain", withCandidates(base, GentleWorkerAgent, "a/reason", "a/plain")},
		{"catalog ranking and tie breaks", GentleReviewerAgent, "catalog-default", "catalog-ranked-eligible-model", "b/wide", base},
		{"unavailable and missing models omit", GentleReviewerAgent, "runtime-default", "runtime-default-no-eligible-model", "", DirectRoleModelRequest{Catalog: map[string]Provider{"a": {ID: "a", Models: map[string]Model{"reason": {ID: "reason", ToolCall: true, Reasoning: true}}}}, AvailableProviders: map[string]bool{}, Explicit: map[string]model.ModelAssignment{GentleReviewerAgent: {ProviderID: "a", ModelID: "missing"}}}},
		{"ownership mismatch omits", GentleReviewerAgent, "runtime-default", "runtime-default-no-eligible-model", "", withExplicit(DirectRoleModelRequest{Catalog: map[string]Provider{"a": {ID: "other", Models: map[string]Model{"reason": {ID: "reason", ToolCall: true, Reasoning: true}}}}}, GentleReviewerAgent, model.ModelAssignment{ProviderID: "a", ModelID: "reason"})},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDirectRoleModels(tt.request)[tt.role]
			if string(got.Source) != tt.wantSource || got.Reason != tt.wantReason {
				t.Fatalf("source/reason = %q/%q", got.Source, got.Reason)
			}
			if tt.wantModel == "" {
				if got.Assignment != nil {
					t.Fatalf("unexpected assignment %#v", got.Assignment)
				}
				return
			}
			if got.Assignment == nil || got.Assignment.FullID() != tt.wantModel {
				t.Fatalf("assignment = %#v, want %q", got.Assignment, tt.wantModel)
			}
		})
	}
	if _, ok := ResolveDirectRoleModels(base)["sdd-apply"]; ok {
		t.Fatal("SDD role leaked into direct-role resolver")
	}
}

func withExplicit(request DirectRoleModelRequest, role string, assignment model.ModelAssignment) DirectRoleModelRequest {
	request.Explicit = map[string]model.ModelAssignment{role: assignment}
	return request
}
func withCandidates(request DirectRoleModelRequest, role string, values ...string) DirectRoleModelRequest {
	assignments := make([]model.ModelAssignment, 0, len(values))
	for _, value := range values {
		assignments = append(assignments, model.ModelAssignment{ProviderID: value[:1], ModelID: value[2:]})
	}
	request.SemanticCandidates = map[string][]model.ModelAssignment{role: assignments}
	return request
}
