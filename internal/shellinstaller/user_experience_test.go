package shellinstaller

import (
	"strings"
	"testing"
)

func TestUserExperienceContract(t *testing.T) {
	defaults := DefaultUserExperience()
	if defaults.Persona != "gentleman" || defaults.BackgroundSubagents != "on" {
		t.Fatalf("defaults differ from the recovered new-install choices: %+v", defaults)
	}
	for _, persona := range []string{"gentleman", "neutral", "unmanaged"} {
		for _, policy := range []string{"on", "off"} {
			t.Run(persona+"/"+policy, func(t *testing.T) {
				want := UserExperience{Persona: persona, BackgroundSubagents: policy}
				raw, err := want.Encode()
				if err != nil {
					t.Fatal(err)
				}
				got, err := DecodeUserExperience(raw)
				if err != nil || got != want {
					t.Fatalf("round trip: %+v %v", got, err)
				}
				canonical, err := got.Encode()
				if err != nil || canonical != raw {
					t.Fatalf("unstable confirmation input: %q %v", canonical, err)
				}
			})
		}
	}
}

func TestUserExperienceRefusesAmbiguousOrUnsupportedChoices(t *testing.T) {
	valid := `{"persona":"neutral","background_subagents":"off"}`
	for _, raw := range []string{
		"", "null", "[]", "{}", valid + "{}",
		`{"persona":"neutral"}`,
		`{"background_subagents":"on"}`,
		`{"persona":"neutral","background_subagents":true}`,
		`{"persona":"neutral","persona":"gentleman","background_subagents":"on"}`,
		`{"persona":"custom","background_subagents":"on"}`,
		`{"persona":"Gentleman","background_subagents":"on"}`,
		`{"persona":"neutral","background_subagents":"auto"}`,
		`{"persona":"neutral","background_subagents":"on","sdd":true}`,
		`{"persona":"neutral","background_subagents":"on","channel":"main"}`,
		strings.Repeat(" ", 1024) + valid,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := DecodeUserExperience(raw); err == nil {
				t.Fatal("accepted ambiguous or unsupported experience")
			}
		})
	}
	if _, err := (UserExperience{Persona: "custom", BackgroundSubagents: "on"}).Encode(); err == nil {
		t.Fatal("custom must preserve files, never write an unsupported supplier mode")
	}
}
