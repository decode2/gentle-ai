//go:build linux || darwin

package shellinstaller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserGGAPrivateStageConfiguration(t *testing.T) {
	for _, choice := range []string{"on", "off", "absent"} {
		t.Run(choice, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "stage")
			profile := UserExperience{Persona: "unmanaged", BackgroundSubagents: "off"}
			if choice != "absent" {
				profile.GGA = choice
			}
			if choice == "on" {
				profile.GGAProvider = "codex"
			}
			if err := userPrepareInstallStage(context.Background(), root, "separate", &profile); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "config/gga/config")
			if choice == "on" {
				data, err := os.ReadFile(config)
				if err != nil || string(data) != "PROVIDER=\"codex\"\n" {
					t.Fatalf("canonical config %q: %v", data, err)
				}
				for path, mode := range map[string]os.FileMode{filepath.Dir(config): 0700, config: 0600} {
					info, err := os.Lstat(path)
					if err != nil || info.Mode().Perm() != mode {
						t.Fatal("private GGA permissions differ", path, info, err)
					}
				}
			} else if _, err := os.Lstat(filepath.Dir(config)); !os.IsNotExist(err) {
				t.Fatal("OFF/absent created GGA configuration", err)
			}
			for _, name := range []string{"config/persona.json", "home/.config/gga", "project/.gga", "project/.git"} {
				if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatal("stage touched unrelated persona/project/unused config", name, err)
				}
			}
		})
	}
}

func TestUserGGAStageKeepsExistingConfiguration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stage")
	if err := userPrepareInstallStage(context.Background(), root, "separate", nil); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config/gga")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "config"), []byte("preserve existing bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := userTreeStamp(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := userStageGGA(context.Background(), root, "separate", "on", "codex"); err == nil {
		t.Fatal("existing directory admitted")
	}
	after, err := userTreeStamp(root)
	if err != nil || before != after {
		t.Fatal("existing configuration changed", err)
	}
}

func TestUserGGAStageRefusesBeforeEffects(t *testing.T) {
	for _, choice := range []string{"on", "off"} {
		t.Run(choice+"/shared", func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "untouched")
			profile := UserExperience{Persona: "unmanaged", BackgroundSubagents: "off", GGA: choice}
			if choice == "on" {
				profile.GGAProvider = "codex"
			}
			if err := userPrepareInstallStage(context.Background(), root, "shared", &profile); err == nil || !strings.Contains(err.Error(), "GGA") {
				t.Fatal("unqualified preparation accepted GGA", err)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("refusal changed filesystem", err)
			}
		})
	}
	root := filepath.Join(t.TempDir(), "invalid")
	profile := UserExperience{Persona: "unmanaged", BackgroundSubagents: "off", GGA: "on", GGAProvider: "pi"}
	if err := userPrepareInstallStage(context.Background(), root, "separate", &profile); err == nil {
		t.Fatal("invalid full profile accepted")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("invalid profile produced effects", err)
	}
}
