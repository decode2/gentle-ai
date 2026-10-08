package shellinstaller

import (
	"reflect"
	"testing"
)

// Literal protocol strings are not native filesystem or installation proof.
func TestUserInstallEntryPortableRoundTrip(t *testing.T) {
	layouts := []struct{ name, root, prefix, agent string }{
		{"linux-syntax", "/owned/runtime with spaces", "/owned/npm", "/owned/agent"},
		{"darwin-syntax", "/Users/example/Library/Application Support/runtime", "/Users/example/npm", "/Users/example/agent"},
		{"windows-syntax", `C:\Users\Example\AppData\Local\runtime`, `C:\Programs\Pi`, `C:\Users\Example\agent`},
	}
	for _, layout := range layouts {
		for _, mode := range []string{"separate", "shared"} {
			t.Run(layout.name+"/"+mode, func(t *testing.T) {
				values := []string{layout.root, mode, layout.prefix, layout.agent, "physical-selection-sha"}
				want := UserInstallRequest{Destination: layout.root, Mode: mode, SharedPrefix: layout.prefix, SharedAgent: layout.agent, Confirmation: values[4]}
				got, err := UserInstallFromEntry(values)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("protocol changed literal selection: %+v %v", got, err)
				}
				values[0] = "changed caller input"
				if got.Destination != want.Destination {
					t.Fatal("parsed request aliases caller argument storage")
				}
			})
		}
	}
	for _, values := range [][]string{nil, {"target", "separate", "", ""}, {"target", "separate", "", "", "sha", "extra"}} {
		if _, err := UserInstallFromEntry(values); err == nil {
			t.Fatalf("accepted invalid legacy arity: %q", values)
		}
	}
}

func TestUserConfirmationPortableBindsSelectionNotPriorApproval(t *testing.T) {
	req := UserInstallRequest{Destination: "literal-target", Mode: "shared", SharedPrefix: "literal-prefix", SharedAgent: "literal-agent"}
	before := userConfirmation(req, "physical-identity")
	changes := map[string]func(*UserInstallRequest){
		"destination": func(r *UserInstallRequest) { r.Destination += "-new" },
		"mode":        func(r *UserInstallRequest) { r.Mode = "separate" },
		"prefix":      func(r *UserInstallRequest) { r.SharedPrefix += "-new" },
		"agent":       func(r *UserInstallRequest) { r.SharedAgent += "-new" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			changed := req
			change(&changed)
			if userConfirmation(changed, "physical-identity") == before {
				t.Fatal("changed selection reused confirmation")
			}
		})
	}
	if len(before) != 64 || userConfirmation(req, "replacement-identity") == before {
		t.Fatal("physical identity is not represented in the confirmation digest")
	}
	req.Confirmation = "old-approval"
	if userConfirmation(req, "physical-identity") != before {
		t.Fatal("prior approval became part of the selection identity")
	}
}
