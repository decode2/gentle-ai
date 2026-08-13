package workqueue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digest(c string) string { return "sha256:" + strings.Repeat(c, 64) }
func testItem(id string) QueueItem {
	return QueueItem{ID: id, Payload: "payload-" + id, Scopes: []string{"src/" + id + ".go", "src/shared.go"}, Conflicts: []ConflictKey{{ConflictKeySchema, PathConflictDomain, "git", "src/" + id + ".go", "1"}}, Evidence: EvidenceRequirements{[]string{"test-" + id}, []string{"go test " + id}}, Delivery: DeliveryBoundary{"remove " + id, "merge " + id}}
}
func testInput(root string) GraphInput {
	a, b := testItem("a"), testItem("b")
	a.DependsOn = []string{"b"}
	return GraphInput{GitCommonDirBinding{GitCommonDirBindingSchema, root, digest("1")}, ChangeIdentity{"change-1", "plan-1"}, SourceArtifactBinding{Locator: ArtifactLocator{"change-1", "openspec/changes/change-1/proposal.md"}, Schema: SourceArtifactBindingSchema, Revision: digest("2")}, []QueueItem{b, a}}
}
func require(t *testing.T, ok bool, message string) {
	if !ok {
		t.Fatal(message)
	}
}
func snapshot(t *testing.T, root string, input GraphInput) GraphSnapshot {
	got, _ := NewSnapshot(root, input)
	require(t, got.GraphRevision() != "", "invalid graph")
	return got
}
func TestSnapshotIdentity(t *testing.T) {
	root := t.TempDir()
	input := testInput(root)
	first := snapshot(t, root, input)
	revision := first.GraphRevision()
	input.Items[0].Payload, input.Items[0].Scopes[0] = "changed", "src/mutated.go"
	require(t, first.GraphRevision() == revision, "snapshot changed after caller mutation")
	input = testInput(root)
	input.Items[0], input.Items[1] = input.Items[1], input.Items[0]
	input.Items[0].Scopes[0], input.Items[0].Scopes[1] = input.Items[0].Scopes[1], input.Items[0].Scopes[0]
	require(t, first.GraphRevision() == snapshot(t, root, input).GraphRevision() && strings.HasPrefix(revision, "sha256:"), "revision is not deterministic SHA-256")
	real, alias := filepath.Join(root, "real"), filepath.Join(root, "alias")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	left, right := testInput(root), testInput(root)
	left.Items[1].Scopes, right.Items[1].Scopes = []string{"real/new.go"}, []string{"alias/new.go"}
	left.Items[1].Conflicts[0].Name, right.Items[1].Conflicts[0].Name = "real/new.go", "alias/new.go"
	require(t, snapshot(t, root, left).GraphRevision() == snapshot(t, root, right).GraphRevision(), "equivalent symlink paths differ")
}
func TestRejectsInvalidGraphInputs(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	cases := []struct {
		name   string
		mutate func(*GraphInput)
	}{{"binding schema", func(in *GraphInput) { in.Repository.Schema = "" }}, {"empty common", func(in *GraphInput) { in.Repository.CommonDir = "" }}, {"foreign common", func(in *GraphInput) { in.Repository.CommonDir = other }}, {"unsafe common", func(in *GraphInput) { in.Repository.CommonDir = filepath.Join(root, "missing") }}, {"empty identity", func(in *GraphInput) { in.Repository.CommonDirIdentity = "" }}, {"source revision", func(in *GraphInput) { in.Source.Revision = "" }}, {"source topic", func(in *GraphInput) { in.Source.Locator.Topic = "change--1" }}, {"foreign source", func(in *GraphInput) { in.Source.Locator.Path = "openspec/changes/other/proposal.md" }}, {"unsafe source", func(in *GraphInput) { in.Source.Locator.Path = "openspec/changes/change-1/../proposal.md" }}, {"unsafe scope", func(in *GraphInput) { in.Items[1].Scopes = []string{"src/../out"} }}, {"duplicate ID", func(in *GraphInput) { in.Items[1].ID = in.Items[0].ID }}, {"missing dependency", func(in *GraphInput) { in.Items[1].DependsOn = []string{"missing"} }}, {"self dependency", func(in *GraphInput) { in.Items[0].DependsOn = []string{"a"} }}, {"cycle", func(in *GraphInput) { in.Items[0].DependsOn, in.Items[1].DependsOn = []string{"a"}, []string{"a"} }}, {"duplicate conflict", func(in *GraphInput) { in.Items[1].Conflicts = append(in.Items[1].Conflicts, in.Items[1].Conflicts[0]) }}, {"duplicate semantic", func(in *GraphInput) {
		in.Items[1].Conflicts = []ConflictKey{{ConflictKeySchema, SemanticConflictDomain, "queue", "Need  A", "1"}, {ConflictKeySchema, SemanticConflictDomain, "queue", "Need A", "1"}}
	}}, {"unknown domain", func(in *GraphInput) { in.Items[1].Conflicts[0].Domain = "future" }}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			input := testInput(root)
			tt.mutate(&input)
			_, err := NewSnapshot(root, input)
			require(t, err != nil, "accepted invalid graph")
		})
	}
	outside, link := t.TempDir(), filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	input := testInput(root)
	input.Items[1].Scopes = []string{"escape/new.go"}
	_, err := NewSnapshot(root, input)
	require(t, err != nil, "accepted a symlink escape")
}
func TestRevisionDriftAndNormalization(t *testing.T) {
	root := t.TempDir()
	base := snapshot(t, root, testInput(root))
	cases := []struct {
		name   string
		mutate func(*GraphInput)
		same   bool
	}{{"change", func(in *GraphInput) { in.Change.Revision = "plan-2" }, false}, {"payload", func(in *GraphInput) { in.Items[1].Payload = "other" }, false}, {"source", func(in *GraphInput) { in.Source.Revision = digest("3") }, false}, {"repository", func(in *GraphInput) { in.Repository.CommonDirIdentity = digest("3") }, false}, {"dependency", func(in *GraphInput) { in.Items[1].DependsOn = nil }, false}, {"scope", func(in *GraphInput) { in.Items[1].Scopes[0] = "src/new.go" }, false}, {"conflict", func(in *GraphInput) { in.Items[1].Conflicts[0].Version = "2" }, false}, {"evidence", func(in *GraphInput) { in.Items[1].Evidence.Required[0] = "other" }, false}, {"delivery", func(in *GraphInput) { in.Items[1].Delivery.IntegrationBoundary = "other" }, false}, {"duplicate dependency", func(in *GraphInput) { in.Items[1].DependsOn = append(in.Items[1].DependsOn, "b") }, true}, {"duplicate scope", func(in *GraphInput) { in.Items[1].Scopes = append(in.Items[1].Scopes, "src/shared.go") }, true}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			input := testInput(root)
			tt.mutate(&input)
			require(t, (snapshot(t, root, input).GraphRevision() == base.GraphRevision()) == tt.same, "unexpected revision equality")
		})
	}
	input := testInput(root)
	input.Items[1].Conflicts = []ConflictKey{{ConflictKeySchema, SemanticConflictDomain, "Vendor", "  Need  A ", "1"}}
	spaced := snapshot(t, root, input)
	input.Items[1].Conflicts[0].Name = "Need A"
	require(t, spaced.GraphRevision() == snapshot(t, root, input).GraphRevision(), "semantic whitespace was not normalized")
	input.Items[1].Conflicts[0].Name = "need a"
	require(t, snapshot(t, root, input).GraphRevision() != spaced.GraphRevision(), "semantic key was blindly lowercased")
}
