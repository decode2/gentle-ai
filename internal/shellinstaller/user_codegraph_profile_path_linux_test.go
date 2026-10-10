//go:build linux && amd64

package shellinstaller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserCodeGraphReviewRefusesAmbiguousLaunchPath(t *testing.T) {
	parent := t.TempDir()
	profile := DefaultUserExperience()
	profile.CodeGraph = "on"
	req := UserInstallRequest{Destination: filepath.Join(parent, "selected:alias"), Mode: "separate", Experience: &profile}
	if token, err := InspectUserInstall(req); err == nil || token != "" {
		t.Fatal("review authorizes destination already refused by private launch", token, err)
	} else {
		t.Log("existing destination refusal:", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("refusal acquired or changed files", entries, err)
	}
}
