package sdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/opencode"
)

func TestProfileRoleMergePreservesUnownedAndDriftedEntries(t *testing.T) {
	tests := []struct {
		name      string
		legacy    bool
		mutate    func(map[string]any)
		wantClass opencode.OwnershipClassification
	}{
		{
			name:      "legacy agents key",
			legacy:    true,
			mutate:    func(entry map[string]any) { delete(entry, opencode.ManagedMetadataKey) },
			wantClass: opencode.OwnershipMissingMetadata,
		},
		{
			name: "missing metadata",
			mutate: func(entry map[string]any) {
				delete(entry, opencode.ManagedMetadataKey)
			},
			wantClass: opencode.OwnershipMissingMetadata,
		},
		{
			name: "wrong owner",
			mutate: func(entry map[string]any) {
				metadata := entry[opencode.ManagedMetadataKey].(opencode.ManagedAgentMetadata)
				metadata.Owner = "another-tool"
				entry[opencode.ManagedMetadataKey] = metadata
			},
			wantClass: opencode.OwnershipWrongOwner,
		},
		{
			name: "malformed metadata",
			mutate: func(entry map[string]any) {
				entry[opencode.ManagedMetadataKey] = map[string]any{"schema": opencode.ManagedMetadataSchema}
			},
			wantClass: opencode.OwnershipMalformedMetadata,
		},
		{
			name: "fingerprint drift",
			mutate: func(entry map[string]any) {
				entry["description"] = "user replacement"
			},
			wantClass: opencode.OwnershipFingerprintDrift,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			settingsPath := filepath.Join(home, "opencode.json")
			original := managedDirectRoleEntryForTest(t, opencode.GentleReviewerAgent, "cheap")
			tt.mutate(original)
			key := "agent"
			if tt.legacy {
				key = "agents"
			}
			writeJSONSettings(t, settingsPath, map[string]any{key: map[string]any{
				opencode.GentleReviewerAgent + "-cheap": original,
			}})
			before := readJSONSettings(t, settingsPath)[key].(map[string]any)[opencode.GentleReviewerAgent+"-cheap"].(map[string]any)

			overlay, err := GenerateProfileOverlay(model.Profile{Name: "cheap"}, home, settingsPath, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			_, report, err := mergeProfileJSONFile(settingsPath, overlay, "cheap")
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Preserved) != 1 || report.Preserved[0].Key != opencode.GentleReviewerAgent+"-cheap" || report.Preserved[0].Classification != tt.wantClass {
				t.Fatalf("report = %#v, want one %s conflict", report.Preserved, tt.wantClass)
			}

			root := readJSONSettings(t, settingsPath)
			agent := root["agent"].(map[string]any)[opencode.GentleReviewerAgent+"-cheap"]
			if !reflect.DeepEqual(agent, before) {
				t.Fatalf("conflicting entry was overwritten: got %#v, want %#v", agent, before)
			}
		})
	}
}
func TestProfileRoleMergeIsIdempotentForManagedEntries(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, "opencode.json")
	writeJSONSettings(t, settingsPath, map[string]any{"agent": map[string]any{}})
	overlay, err := GenerateProfileOverlay(model.Profile{Name: "cheap"}, home, settingsPath, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, report, err := mergeProfileJSONFile(settingsPath, overlay, "cheap"); err != nil || len(report.Preserved) != 0 {
		t.Fatalf("first merge report=%#v err=%v", report, err)
	}
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, report, err := mergeProfileJSONFile(settingsPath, overlay, "cheap"); err != nil || len(report.Preserved) != 0 {
		t.Fatalf("second merge report=%#v err=%v", report, err)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("managed profile merge is not byte-idempotent")
	}
}

func TestProfileRoleSyncCreatesRequestedRoles(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, "opencode.json")
	writeJSONSettings(t, settingsPath, map[string]any{"agent": map[string]any{}})
	overlay, err := GenerateProfileOverlay(model.Profile{Name: "cheap"}, home, settingsPath, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, report, err := mergeProfileJSONFile(settingsPath, overlay, "cheap", RoleReconciliationSync); err != nil || len(report.Preserved) != 0 {
		t.Fatalf("sync merge report=%#v err=%v", report, err)
	}
	agents := readJSONSettings(t, settingsPath)["agent"].(map[string]any)
	for _, role := range opencode.DirectRoles() {
		if _, ok := agents[role+"-cheap"]; !ok {
			t.Fatalf("sync did not create requested profile role %q", role)
		}
	}
}
func TestRemoveProfileAgentsWithReportPreservesRoleReplacement(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, "opencode.json")
	writeJSONSettings(t, settingsPath, map[string]any{"agent": map[string]any{
		opencode.GentleReviewerAgent + "-cheap": map[string]any{"mode": "subagent", "description": "user replacement"},
		opencode.GentleWorkerAgent + "-cheap":   wrongOwnerDirectRoleEntryForTest(t, opencode.GentleWorkerAgent),
		"sdd-orchestrator-cheap":                map[string]any{"mode": "primary"},
	}})

	report, err := RemoveProfileAgentsWithReport(settingsPath, "cheap")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Preserved) != 2 || report.Preserved[0].Classification != opencode.OwnershipMissingMetadata || report.Preserved[1].Classification != opencode.OwnershipWrongOwner {
		t.Fatalf("report = %#v, want unowned reviewer and wrong-owner worker", report.Preserved)
	}
	root := readJSONSettings(t, settingsPath)
	agents := root["agent"].(map[string]any)
	if _, ok := agents[opencode.GentleReviewerAgent+"-cheap"]; !ok {
		t.Fatal("unowned reviewer replacement was removed")
	}
	if _, ok := agents[opencode.GentleWorkerAgent+"-cheap"]; !ok {
		t.Fatal("wrong-owner worker was removed")
	}
	if _, ok := agents["sdd-orchestrator-cheap"]; ok {
		t.Fatal("profile orchestrator was not removed")
	}
}
func wrongOwnerDirectRoleEntryForTest(t *testing.T, role string) map[string]any {
	t.Helper()
	entry := managedDirectRoleEntryForTest(t, role, "cheap")
	metadata := entry[opencode.ManagedMetadataKey].(opencode.ManagedAgentMetadata)
	metadata.Owner = "another-tool"
	entry[opencode.ManagedMetadataKey] = metadata
	return entry
}
func writeJSONSettings(t *testing.T, path string, root map[string]any) {
	t.Helper()
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
func readJSONSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	var root map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return root
}
