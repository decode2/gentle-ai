package opencode

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

var testIdentity = ManagedAgentIdentity{
	Owner:     ManagedOwner,
	Component: ManagedComponent,
	Role:      GentleReviewerAgent,
}

func testAgent() map[string]any {
	return map[string]any{
		"mode":        "subagent",
		"hidden":      true,
		"description": "reviewer",
		"permission": map[string]any{
			"read": "allow",
			"bash": map[string]any{"git status*": "allow", "*": "deny"},
		},
	}
}

func TestFingerprintIsStableAndMapOrderIndependent(t *testing.T) {
	first := testAgent()
	second := map[string]any{
		"permission": map[string]any{
			"bash": map[string]any{"*": "deny", "git status*": "allow"},
			"read": "allow",
		},
		"description": "reviewer",
		"hidden":      true,
		"mode":        "subagent",
	}

	one, err := Fingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Fingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatalf("fingerprints differ for equivalent maps: %q != %q", one, two)
	}
	if one != "sha256:a67f881cb6871a8669e1c6f08be756310a65f3b127f4457879de29ba7fb41173" {
		t.Fatalf("fingerprint = %q, want the stable canonical digest", one)
	}
	if oneAgain, err := Fingerprint(first); err != nil || oneAgain != one {
		t.Fatalf("fingerprint is not stable: %q, %v", oneAgain, err)
	}
}

func TestFingerprintExcludesOwnershipMetadata(t *testing.T) {
	agent := testAgent()
	before, err := Fingerprint(agent)
	if err != nil {
		t.Fatal(err)
	}
	agent[ManagedMetadataKey] = map[string]any{
		"owner":       "someone-else",
		"fingerprint": "not-used-for-definition",
	}
	after, err := Fingerprint(agent)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("metadata changed definition fingerprint: %q != %q", before, after)
	}
	canonical, err := CanonicalAgentJSON(agent)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte(ManagedMetadataKey)) {
		t.Fatalf("canonical agent JSON contains ownership metadata: %s", canonical)
	}
}

func TestWithManagedMetadataRoundTripsAsExactManaged(t *testing.T) {
	agent := testAgent()
	managed, err := WithManagedMetadata(agent, testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if got := ClassifyOwnership(managed, testIdentity); got != OwnershipManaged {
		t.Fatalf("classification = %q, want %q", got, OwnershipManaged)
	}
	raw, err := json.Marshal(managed)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if got := ClassifyOwnership(roundTrip, testIdentity); got != OwnershipManaged {
		t.Fatalf("round-trip classification = %q, want %q", got, OwnershipManaged)
	}
}

func TestClassifyOwnershipPreservesNonOwnedStates(t *testing.T) {
	managed, err := WithManagedMetadata(testAgent(), testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   OwnershipClassification
	}{
		{name: "missing metadata", mutate: func(agent map[string]any) { delete(agent, ManagedMetadataKey) }, want: OwnershipMissingMetadata},
		{name: "wrong owner", mutate: func(agent map[string]any) {
			metadata := agent[ManagedMetadataKey].(ManagedAgentMetadata)
			metadata.Owner = "other"
			agent[ManagedMetadataKey] = metadata
		}, want: OwnershipWrongOwner},
		{name: "wrong component", mutate: func(agent map[string]any) {
			metadata := agent[ManagedMetadataKey].(ManagedAgentMetadata)
			metadata.Component = "other"
			agent[ManagedMetadataKey] = metadata
		}, want: OwnershipWrongComponent},
		{name: "wrong role", mutate: func(agent map[string]any) {
			metadata := agent[ManagedMetadataKey].(ManagedAgentMetadata)
			metadata.Role = GentleWorkerAgent
			agent[ManagedMetadataKey] = metadata
		}, want: OwnershipWrongRole},
		{name: "malformed metadata", mutate: func(agent map[string]any) {
			agent[ManagedMetadataKey] = map[string]any{"schema": ManagedMetadataSchema}
		}, want: OwnershipMalformedMetadata},
		{name: "fingerprint drift", mutate: func(agent map[string]any) { agent["description"] = "changed" }, want: OwnershipFingerprintDrift},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneAgent(managed)
			tt.mutate(candidate)
			if got := ClassifyOwnership(candidate, testIdentity); got != tt.want {
				t.Fatalf("classification = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWithManagedMetadataDoesNotMutateCallerMap(t *testing.T) {
	agent := testAgent()
	before := cloneAgent(agent)
	managed, err := WithManagedMetadata(agent, testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(agent, before) {
		t.Fatalf("caller map mutated: before=%#v after=%#v", before, agent)
	}
	if _, ok := agent[ManagedMetadataKey]; ok {
		t.Fatal("caller map acquired ownership metadata")
	}
	managed["description"] = "changed"
	if agent["description"] != "reviewer" {
		t.Fatal("returned map shares top-level state with caller")
	}
}

func cloneAgent(agent map[string]any) map[string]any {
	clone := make(map[string]any, len(agent))
	for key, value := range agent {
		clone[key] = value
	}
	return clone
}
