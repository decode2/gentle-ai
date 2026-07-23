package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFinalVerificationIncidentSchemaIsClosedToProceduralToolingFailure(t *testing.T) {
	var output bytes.Buffer
	if err := RunReviewSchema([]string{"final-verification-incident"}, &output); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(output.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false || schema["$id"] != "https://gentle-ai.dev/contracts/review-integration/v1/schemas/final-verification-incident.schema.json" {
		t.Fatalf("incident schema header = %#v", schema)
	}
	properties := schema["properties"].(map[string]any)
	if properties["schema"].(map[string]any)["const"] != "gentle-ai.review-final-verification-incident/v1" ||
		properties["class"].(map[string]any)["const"] != "procedural_tooling_failure" {
		t.Fatalf("incident identity = %#v", properties)
	}
	if len(schemaStringArray(t, schema["required"])) != 8 {
		t.Fatalf("incident required fields = %#v", schema["required"])
	}
	contractPayload, err := os.ReadFile(filepath.Join("..", "..", "contracts", "review-integration", "v1", "schemas", "final-verification-incident.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract map[string]any
	if err := json.Unmarshal(contractPayload, &contract); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schema, contract) {
		t.Fatal("runtime and packaged final-verification incident schemas differ")
	}
}

func TestReviewerSchemaMatchesProviderAdmissionEnvelope(t *testing.T) {
	var output bytes.Buffer
	if err := RunReviewSchema([]string{"reviewer"}, &output); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(output.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	required := schemaStringArray(t, schema["required"])
	for _, field := range []string{"subject_hash", "inspection", "findings", "evidence"} {
		if !containsString(required, field) {
			t.Fatalf("reviewer schema required fields = %v, missing %q", required, field)
		}
	}
	properties := schema["properties"].(map[string]any)
	if properties["subject_hash"].(map[string]any)["pattern"] != "^sha256:[0-9a-f]{64}$" {
		t.Fatalf("reviewer subject_hash schema = %#v", properties["subject_hash"])
	}
	inspection := properties["inspection"].(map[string]any)
	if inspection["additionalProperties"] != false {
		t.Fatalf("reviewer inspection schema is not strict: %#v", inspection)
	}
	inspectionRequired := schemaStringArray(t, inspection["required"])
	if !containsString(inspectionRequired, "status") || !containsString(inspectionRequired, "paths") {
		t.Fatalf("reviewer inspection required fields = %v", inspectionRequired)
	}
	finding := properties["findings"].(map[string]any)["items"].(map[string]any)
	id := finding["properties"].(map[string]any)["id"].(map[string]any)
	if id["pattern"] != "^R[1-4]-[A-Za-z0-9][A-Za-z0-9._-]*$" {
		t.Fatalf("reviewer finding id pattern = %#v", id)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
