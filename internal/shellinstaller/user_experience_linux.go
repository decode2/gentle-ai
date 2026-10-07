//go:build linux

package shellinstaller

import (
	"encoding/json"
	"path/filepath"
)

// Preferences belong to the new private runtime's config home, including in
// shared mode. Never rewrite a selected agent, a project override or host config.
func userApplyExperience(root string, profile UserExperience) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	config := filepath.Join(root, "config")
	if _, err := privateDirectory(config); err != nil {
		return err
	}
	if profile.Persona != "unmanaged" {
		data, err := json.Marshal(struct {
			Mode string `json:"mode"`
		}{profile.Persona})
		if err != nil {
			return err
		}
		if err := userToolWrite(filepath.Join(config, "persona.json"), data, 0600); err != nil {
			return err
		}
	}
	data, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Policy string `json:"policy"`
	}{"gentle-pi.background-subagents/v1", profile.BackgroundSubagents})
	if err != nil {
		return err
	}
	return userToolWrite(filepath.Join(config, "background-subagents.json"), data, 0600)
}
