package sdd

import (
	"encoding/json"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/opencode"
)

func TestReconcileDefaultOpenCodeRoles(t *testing.T) {
	overlay := directRoleOverlay(t)
	resolved := map[string]opencode.DirectRoleModelResolution{
		opencode.GentleReviewerAgent: {Role: opencode.GentleReviewerAgent, Assignment: assignment("a", "review"), Source: opencode.DirectRoleModelSemanticPolicy, Reason: "semantic-policy-ranked-candidate"},
	}
	cases := []struct {
		name, base string
		mode       RoleReconciliationMode
		wantRoles  []string
		wantModel  string
	}{
		{"fresh install", `{}`, RoleReconciliationInstall, opencode.DirectRoles(), "a/review"},
		{"sync does not recreate absent", `{"agent":{"custom":{}}}`, RoleReconciliationSync, nil, ""},
		{"user role preserved", `{"agent":{"gentle-reviewer":{"prompt":"user"}}}`, RoleReconciliationInstall, []string{opencode.GentleWorkerAgent}, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := reconcileDefaultOpenCodeRoles([]byte(tt.base), overlay, tt.mode, resolved)
			if err != nil {
				t.Fatal(err)
			}
			root := map[string]any{}
			if err := json.Unmarshal(got, &root); err != nil {
				t.Fatal(err)
			}
			agents, _ := root["agent"].(map[string]any)
			for _, role := range opencode.DirectRoles() {
				_, exists := agents[role]
				want := false
				for _, expected := range tt.wantRoles {
					if expected == role {
						want = true
					}
				}
				if exists != want {
					t.Fatalf("role %q exists=%v, want %v", role, exists, want)
				}
			}
			if tt.wantModel != "" && agents[opencode.GentleReviewerAgent].(map[string]any)["__replace__"].(map[string]any)["model"] != tt.wantModel {
				t.Fatal("resolved model was not materialized")
			}
		})
	}
}

func assignment(provider, modelID string) *model.ModelAssignment {
	return &model.ModelAssignment{ProviderID: provider, ModelID: modelID}
}

func directRoleOverlay(t *testing.T) []byte {
	t.Helper()
	agents := map[string]any{}
	for _, role := range opencode.DirectRoles() {
		definition, ok := opencode.DirectRoleDefinitionFor(role)
		if !ok {
			t.Fatal("missing role definition")
		}
		agents[role] = map[string]any{"mode": "subagent", "prompt": definition.Prompt}
	}
	data, err := json.Marshal(map[string]any{"agent": agents})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
