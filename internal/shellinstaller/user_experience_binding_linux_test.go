//go:build linux

package shellinstaller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserExperienceBoundToPhysicalSelectionAndEntry(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	req := UserInstallRequest{Destination: filepath.Join(home, "shell"), Mode: "separate"}
	legacy, err := InspectUserInstall(req)
	if err != nil {
		t.Fatal(err)
	}
	prefs := DefaultUserExperience()
	req.Experience = &prefs
	selected, err := InspectUserInstall(req)
	if err != nil || selected == legacy {
		t.Fatalf("experience was not bound to physical confirmation: %q %v", selected, err)
	}
	req.Confirmation = selected
	values, err := UserInstallEntryValues(req)
	if err != nil || len(values) != 6 {
		t.Fatalf("experience missing from handoff: %q %v", values, err)
	}
	got, err := UserInstallFromEntry(values)
	if err != nil || got.Experience == nil || *got.Experience != prefs || got.Confirmation != selected {
		t.Fatalf("entry lost choices: %+v %v", got, err)
	}
	prefs.Persona = "neutral"
	changed, err := InspectUserInstall(req)
	if err != nil || changed == selected {
		t.Fatal("changed persona reused approval")
	}
	prefs.BackgroundSubagents = "off"
	changedAgain, err := InspectUserInstall(req)
	if err != nil || changedAgain == changed {
		t.Fatal("changed background policy reused approval")
	}
	values[5] = `{"persona":"custom","background_subagents":"on"}`
	if _, err := UserInstallFromEntry(values); err == nil {
		t.Fatal("supervisor accepted unsupported preferences")
	}
	if _, err := UserInstallFromEntry(append(values, "extra")); err == nil {
		t.Fatal("supervisor accepted unbound extra data")
	}
	req.Experience = nil
	values, err = UserInstallEntryValues(req)
	if err != nil || len(values) != 5 {
		t.Fatal("legacy entry changed")
	}
	if _, err := UserInstallFromEntry(values); err != nil {
		t.Fatal(err)
	}
	prefs.Persona = "custom"
	req.Experience = &prefs
	if _, err := InspectUserInstall(req); err == nil {
		t.Fatal("invalid preferences reached physical inspection")
	}
	prefs.Persona = "neutral"
	if err := os.Mkdir(req.Destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUserInstall(req); err == nil {
		t.Fatal("experience silently reconfigured an installed target")
	}
}

func TestUserExperienceWritesOnlyNewOwnedConfig(t *testing.T) {
	for _, persona := range []string{"gentleman", "neutral", "unmanaged"} {
		t.Run(persona, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config")
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			prefs := UserExperience{Persona: persona, BackgroundSubagents: "off"}
			if err := userApplyExperience(root, prefs); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(config, "persona.json")
			if persona == "unmanaged" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("unmanaged persona created a supplier override")
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(data), `"mode":"`+persona+`"`) {
					t.Fatalf("supplier persona contract differs: %s %v", data, err)
				}
			}
			path = filepath.Join(config, "background-subagents.json")
			data, err := os.ReadFile(path)
			if err != nil || string(data) != `{"schema":"gentle-pi.background-subagents/v1","policy":"off"}` {
				t.Fatalf("supplier policy contract differs: %s %v", data, err)
			}
			if err := userApplyExperience(root, prefs); err == nil {
				t.Fatal("profile overwrite silently replaced an existing preimage")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("config custody: %v %v", info, err)
			}
		})
	}
}
