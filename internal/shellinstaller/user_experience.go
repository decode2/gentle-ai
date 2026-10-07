package shellinstaller

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// UserExperience selects only preferences consumed by the authenticated supplier.
// It is inert: decoding or changing a choice never writes configuration or installs.
// Required ODD, package pins and unsupported legacy workflows are not toggles.
type UserExperience struct {
	Persona             string `json:"persona"`
	BackgroundSubagents string `json:"background_subagents"`
}

func DefaultUserExperience() UserExperience {
	return UserExperience{Persona: "gentleman", BackgroundSubagents: "on"}
}

func (p UserExperience) Validate() error {
	switch p.Persona {
	case "gentleman", "neutral", "unmanaged":
		// Unmanaged means preserve files, not a supplier persona mode named custom.
	default:
		return errors.New("choose gentleman, neutral or unmanaged persona")
	}
	if p.BackgroundSubagents != "on" && p.BackgroundSubagents != "off" {
		return errors.New("choose on or off for background subagents")
	}
	return nil
}

// Encode gives confirmation and handoff one deterministic representation.
func (p UserExperience) Encode() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	return string(data), err
}

func DecodeUserExperience(raw string) (UserExperience, error) {
	var profile UserExperience
	if len(raw) > 256 {
		return profile, errors.New("experience exceeds its bounded input size")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return profile, errors.New("experience must be a JSON object")
	}
	seen := make(map[string]bool, 2)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return UserExperience{}, err
		}
		key, ok := token.(string)
		if !ok || seen[key] || (key != "persona" && key != "background_subagents") {
			return UserExperience{}, errors.New("duplicate or unsupported experience choice")
		}
		seen[key] = true
		var value string
		if err := decoder.Decode(&value); err != nil {
			return UserExperience{}, err
		}
		if key == "persona" {
			profile.Persona = value
		} else {
			profile.BackgroundSubagents = value
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return UserExperience{}, errors.New("unterminated experience object")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return UserExperience{}, errors.New("trailing experience data")
	}
	if err := profile.Validate(); err != nil {
		return UserExperience{}, err
	}
	return profile, nil
}
