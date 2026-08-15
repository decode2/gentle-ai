package workqueue

import (
	"errors"
	"strings"
	"testing"
)

func queueState() State {
	return State{Items: []ItemState{{ID: "b", Status: ItemPending}, {ID: "a", Status: ItemPending}}}
}
func queueGraph(t *testing.T) GraphSnapshot { return snapshot(t, t.TempDir(), testInput(t.TempDir())) }

func TestEnvelopeCanonicalizesAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	graph := snapshot(t, root, testInput(root))
	input := queueState()
	input.Schema, input.GraphRevision, input.Revision = "forged", digest("3"), digest("4")
	canonical, err := Canonicalize(graph, input)
	if err != nil || canonical.Schema != QueueSchemaVersion || canonical.GraphRevision != graph.GraphRevision() || canonical.Revision == digest("4") {
		t.Fatalf("canonical state = %#v, %v", canonical, err)
	}
	first, err := Encode(graph, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(graph, input)
	if err != nil || string(first) != string(second) {
		t.Fatalf("encoding is not deterministic: %v", err)
	}
	if input.Items[0].ID != "b" {
		t.Fatal("caller slice mutated")
	}
	state, err := Decode(graph, first)
	if err != nil || state.Schema != QueueSchemaVersion || state.GraphRevision != graph.GraphRevision() || state.Revision == digest("4") || state.Items[0].ID != "a" {
		t.Fatalf("round trip = %#v, %v", state, err)
	}
	input.Items[1].ID = "changed"
	if canonical.Items[1].ID != "b" || state.Items[1].ID != "b" {
		t.Fatal("canonical state aliases caller data")
	}
}

func TestEnvelopeRejectsInvalidJSON(t *testing.T) {
	root := t.TempDir()
	graph := snapshot(t, root, testInput(root))
	valid, _ := Encode(graph, queueState())
	state, _ := Decode(graph, valid)
	cases := []struct {
		name, raw string
		want      error
	}{
		{"missing schema", `{"graph_revision":"` + graph.GraphRevision() + `","items":[],"revision":"` + digest("1") + `"}`, ErrInvalidEnvelope},
		{"missing item key", strings.Replace(string(valid), `,"status":"pending"`, "", 1), ErrInvalidEnvelope},
		{"unsupported schema", strings.Replace(string(valid), QueueSchemaVersion, "future", 1), ErrUnsupportedSchema},
		{"unknown key", strings.Replace(string(valid), `}`, `,"extra":1}`, 1), ErrInvalidEnvelope},
		{"case variant key", strings.Replace(string(valid), `"schema"`, `"Schema"`, 1), ErrInvalidEnvelope},
		{"duplicate key", strings.Replace(string(valid), `"schema":`, `"schema":"x","schema":`, 1), ErrInvalidEnvelope},
		{"trailing input", string(valid) + `{}`, ErrInvalidEnvelope},
		{"malformed shape", `[]`, ErrInvalidEnvelope},
		{"items object", strings.Replace(string(valid), `[{"id":"a","status":"pending"},{"id":"b","status":"pending"}]`, `{}`, 1), ErrInvalidEnvelope},
		{"item field shape", strings.Replace(string(valid), `"id":"a"`, `"id":1`, 1), ErrInvalidEnvelope},
		{"nested duplicate", strings.Replace(string(valid), `"id":"a",`, `"id":"a","id":"a",`, 1), ErrInvalidEnvelope},
		{"tampered revision", strings.Replace(string(valid), state.Revision, digest("9"), 1), ErrInvalidEnvelope},
		{"malformed digest", strings.Replace(string(valid), graph.GraphRevision(), "sha256:nope", 1), ErrInvalidEnvelope},
		{"wrong graph", strings.Replace(string(valid), graph.GraphRevision(), digest("9"), 1), ErrGraphMismatch},
		{"missing item", strings.Replace(string(valid), `,{"id":"b","status":"pending"}`, "", 1), ErrGraphMismatch},
		{"unknown item", strings.Replace(string(valid), `"id":"b"`, `"id":"c"`, 1), ErrGraphMismatch},
		{"duplicate item", strings.Replace(string(valid), `"id":"b"`, `"id":"a"`, 1), ErrGraphMismatch},
		{"invalid ID", strings.Replace(string(valid), `"id":"a"`, `"id":"bad--id"`, 1), ErrInvalidEnvelope},
		{"invalid status", strings.Replace(string(valid), `"status":"pending"`, `"status":"running"`, 1), ErrInvalidEnvelope},
		{"noncanonical order", strings.Replace(string(valid), `{"id":"a","status":"pending"},{"id":"b","status":"pending"}`, `{"id":"b","status":"pending"},{"id":"a","status":"pending"}`, 1), ErrInvalidEnvelope},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(graph, []byte(tt.raw))
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestEnvelopeSentinels(t *testing.T) {
	root := t.TempDir()
	graph := snapshot(t, root, testInput(root))
	if _, err := Canonicalize(graph, State{Items: []ItemState{{ID: "a", Status: ItemPending}}}); !errors.Is(err, ErrGraphMismatch) {
		t.Fatal(err)
	}
	if _, err := Canonicalize(graph, State{Items: []ItemState{{ID: "a", Status: "bad"}, {ID: "b", Status: ItemPending}}}); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatal(err)
	}
	if _, err := Decode(graph, []byte(`{"schema":"future","graph_revision":"`+graph.GraphRevision()+`","items":[],"revision":"`+digest("1")+`"}`)); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal(err)
	}
}
