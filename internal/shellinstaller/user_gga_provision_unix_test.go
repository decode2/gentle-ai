//go:build linux || darwin

package shellinstaller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUserGGAProvisionDoesNotActivateImplicitly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "untouched")
	for _, profile := range []*UserExperience{nil, {Persona: "unmanaged", BackgroundSubagents: "off"}, {Persona: "unmanaged", BackgroundSubagents: "off", GGA: "off"}} {
		if err := userGGAProvision(nil, root, "separate", profile); err != nil {
			t.Fatal("OFF/absence performed acquisition", err)
		}
	}
	profile := UserExperience{Persona: "unmanaged", BackgroundSubagents: "off", GGA: "on", GGAProvider: "codex"}
	if err := userGGAProvision(context.Background(), root, "shared", &profile); err == nil {
		t.Fatal("Shared accepted GGA provisioning")
	}
	if err := userGGAProvision(nil, root, "separate", &profile); err == nil {
		t.Fatal("ON accepted missing context")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("refused/absent provisioning changed filesystem", err)
	}
}
