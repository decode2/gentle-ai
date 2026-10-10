//go:build linux && amd64

package shellinstaller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserGGAProfileReachesReviewAndCanonicalStage(t *testing.T) {
	for _, choice := range []string{"on", "off"} {
		t.Run(choice, func(t *testing.T) {
			parent := t.TempDir()
			profile := DefaultUserExperience()
			profile.CodeGraph, profile.Context7, profile.GGA = "on", "on", choice
			if choice == "on" {
				profile.GGAProvider = "codex"
			}
			before := profile
			root := filepath.Join(parent, "selected")
			req := UserInstallRequest{Destination: root, Mode: "separate", Experience: &profile}
			token, err := InspectUserInstall(req)
			if err != nil || token == "" {
				t.Fatal("GGA cannot reach physical review", token, err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("inspection wrote before consent", entries, err)
			}
			if choice == "on" {
				profile.GGAProvider = "claude"
				if changed, err := InspectUserInstall(req); err != nil || changed == token || changed == "" {
					t.Fatal("physical token did not bind provider", err)
				}
				profile = before
			}
			if err := userPrepareInstallStage(context.Background(), root, "separate", &profile); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "config/gga/config")
			if choice == "on" {
				assertExperienceFile(t, config, "PROVIDER=\"codex\"\n")
			} else if _, err := os.Lstat(config); !os.IsNotExist(err) {
				t.Fatal("OFF configured GGA", err)
			}
			if before != profile {
				t.Fatal("preparer changed the selection")
			}
			shared := filepath.Join(parent, "shared")
			if err := userPrepareInstallStage(context.Background(), shared, "shared", &profile); err == nil {
				t.Fatal("Shared accepted explicit GGA/CodeGraph")
			}
			if _, err := os.Lstat(shared); !os.IsNotExist(err) {
				t.Fatal("Shared refusal wrote a stage", err)
			}
			if token, err := InspectUserInstall(req); err == nil || token != "" {
				t.Fatal("explicit GGA replaced existing preferences")
			}
		})
	}
}

func TestUserGGAProfileMixedProducerKeepsOwnContextGate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "untouched")
	profile := DefaultUserExperience()
	profile.CodeGraph, profile.Context7, profile.GGA, profile.GGAProvider = "on", "on", "on", "codex"
	if err := userGGAProvision(nil, root, "separate", &profile); err == nil || !strings.Contains(err.Error(), "live private Separate stage") {
		t.Fatal("mixed profile never reaches GGA's own context gate", err)
	}
	profile.CodeGraph = "invalid"
	if err := userGGAProvision(nil, root, "separate", &profile); err == nil {
		t.Fatal("mixed producer dropped malformed CodeGraph")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("refused producer acquired or staged", err)
	}
}
