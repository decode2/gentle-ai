//go:build linux && amd64

package shellinstaller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUserCodeGraphFreshProfileReviewAndStage(t *testing.T) {
	for _, choice := range []string{"on", "off"} {
		for _, persona := range []string{"gentleman", "neutral", "unmanaged"} {
			for _, background := range []string{"on", "off"} {
				t.Run(choice+"/"+persona+"/"+background, func(t *testing.T) {
					parent := t.TempDir()
					target := filepath.Join(parent, "target")
					profile := UserExperience{Persona: persona, BackgroundSubagents: background, CodeGraph: choice}
					req := UserInstallRequest{Destination: target, Mode: "separate", Experience: &profile}
					token, err := InspectUserInstall(req)
					if err != nil || token == "" {
						t.Fatal("qualified CodeGraph selection cannot reach physical review", token, err)
					}
					entries, err := os.ReadDir(parent)
					if err != nil || len(entries) != 0 {
						t.Fatal("review installed before consent", entries, err)
					}
					if err := userPrepareInstallStage(context.Background(), target, req.Mode, &profile); err != nil {
						t.Fatal("selected profile cannot reach canonical stage", err)
					}
					assertExperienceFile(t, filepath.Join(target, "config/background-subagents.json"), "{\n  \"schema\": \"gentle-pi.background-subagents/v1\",\n  \"policy\": \""+background+"\"\n}\n")
					if persona != "unmanaged" {
						assertExperienceFile(t, filepath.Join(target, "config/persona.json"), "{\n  \"mode\": \""+persona+"\"\n}\n")
					} else if _, err := os.Lstat(filepath.Join(target, "config/persona.json")); !os.IsNotExist(err) {
						t.Fatal("unmanaged writes persona", err)
					}
					if choice == "off" {
						if err := userCodeGraphProvision(context.Background(), target, req.Mode, &profile); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := os.Lstat(filepath.Join(target, "runtime")); !os.IsNotExist(err) {
						t.Fatal("configuration acquired or exposed CodeGraph", err)
					}
					if token, err := InspectUserInstall(req); err == nil || token != "" {
						t.Fatal("explicit profile may replace an existing target")
					}
				})
			}
		}
	}
}
