//go:build linux || darwin

package shellinstaller

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUserCodeGraphStageRefusesBeforeEffects(t *testing.T) {
	for _, mode := range []string{"separate", "shared"} {
		for _, choice := range []string{"on", "off"} {
			t.Run(mode+"/"+choice, func(t *testing.T) {
				profile, err := DecodeUserExperience(`{"persona":"unmanaged","background_subagents":"off","codegraph":"` + choice + `"}`)
				if err != nil {
					t.Fatal(err)
				}
				parent := t.TempDir()
				root := filepath.Join(parent, "stage")
				if mode == "separate" && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
					if err := userPrepareInstallStage(context.Background(), root, mode, &profile); err != nil {
						t.Fatal("qualified CodeGraph stage refused", err)
					}
					return
				}
				if err := userPrepareInstallStage(context.Background(), root, mode, &profile); err == nil || !strings.Contains(err.Error(), "CodeGraph") {
					t.Fatal("unqualified CodeGraph stage was admitted", err)
				}
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 0 {
					t.Fatal("CodeGraph refusal changed the destination parent", entries, err)
				}
			})
		}
	}
}
