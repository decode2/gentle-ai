//go:build linux || darwin

package shellinstaller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserGGAStageRefusesBeforeEffects(t *testing.T) {
	for _, choice := range []string{"on", "off"} {
		for _, mode := range []string{"separate", "shared"} {
			t.Run(choice+"/"+mode, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "untouched")
				profile := UserExperience{Persona: "unmanaged", BackgroundSubagents: "off", GGA: choice}
				if choice == "on" {
					profile.GGAProvider = "codex"
				}
				if err := userPrepareInstallStage(context.Background(), root, mode, &profile); err == nil || !strings.Contains(err.Error(), "GGA") {
					t.Fatal("unqualified preparation accepted GGA", err)
				}
				if _, err := os.Lstat(root); !os.IsNotExist(err) {
					t.Fatal("refusal changed filesystem", err)
				}
			})
		}
	}
}
