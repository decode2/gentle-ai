//go:build linux

package shellinstaller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserExperiencePreservesUnmanagedFilesAndRefusesUnsafeConfig(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	persona := filepath.Join(config, "persona.json")
	sentinel := []byte(`{"mode":"neutral","unrelated":"keep"}`)
	if err := os.WriteFile(persona, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	prefs := UserExperience{Persona: "unmanaged", BackgroundSubagents: "on"}
	if err := userApplyExperience(root, prefs); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(persona); err != nil || string(data) != string(sentinel) {
		t.Fatal("unmanaged choice changed an existing persona preimage")
	}
	unsafeRoot := t.TempDir()
	unsafe := filepath.Join(unsafeRoot, "config")
	if err := os.Mkdir(unsafe, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	if err := userApplyExperience(unsafeRoot, DefaultUserExperience()); err == nil {
		t.Fatal("unsafe config directory admitted preferences")
	}
	if entries, err := os.ReadDir(unsafe); err != nil || len(entries) != 0 {
		t.Fatal("custody refusal wrote configuration")
	}
}
