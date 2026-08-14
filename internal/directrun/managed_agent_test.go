package directrun

import "testing"

func TestRecordAgentAcceptsOnlyManagedDirectRoles(t *testing.T) {
	for _, tt := range []struct {
		name, agent string
		want        bool
	}{
		{"worker", "gentle-worker", true}, {"reviewer", "gentle-reviewer", true},
		{"worker profile", "gentle-worker-fast", true}, {"reviewer profile", "gentle-reviewer-audit", true},
		{"sdd", "sdd-apply", false}, {"rdd", "review-risk", false}, {"four r", "4r-worker", false},
		{"prose", "please use gentle-worker", false}, {"empty profile", "gentle-worker-", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := recordAgent(tt.agent); got != tt.want {
				t.Fatalf("recordAgent(%q) = %v, want %v", tt.agent, got, tt.want)
			}
		})
	}
}
