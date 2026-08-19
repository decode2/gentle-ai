package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func issue3336Fixture(s *Sandbox) error {
	if err := baseRepo(s); err != nil {
		return err
	}
	return s.write(s.Home+"/.config/opencode/opencode.json", `{"agent":{"gentle-orchestrator":{"prompt":"USER_OWNED_ORCHESTRATOR_BYTES"}}}`)
}
func issue3336AssertGeneratedPrompt(s *Sandbox, observation Observation) error {
	content, readErr := os.ReadFile(s.Home + "/.config/opencode/opencode.json")
	var settings map[string]any
	parseErr := json.Unmarshal(content, &settings)
	agents, _ := settings["agent"].(map[string]any)
	orchestrator, _ := agents["gentle-orchestrator"].(map[string]any)
	prompt, shapeOK := orchestrator["prompt"].(string)
	if observation.ExitCode != 0 || readErr != nil || parseErr != nil || !shapeOK {
		return fmt.Errorf("sync/read/parse failed (exit %d, read %v, parse %v, shape %t): %s", observation.ExitCode, readErr, parseErr, shapeOK, observation.Stderr)
	}
	if strings.Count(prompt, "<!-- gentle-ai:sdd-session-preflight -->") != 1 || strings.Count(prompt, "<!-- /gentle-ai:sdd-session-preflight -->") != 1 || !strings.Contains(prompt, "Both -> `hybrid`") || !strings.Contains(prompt, "fixed at 400 changed lines") || !strings.Contains(prompt, "USER_OWNED_ORCHESTRATOR_BYTES") {
		return fmt.Errorf("generated prompt lost required canonical or user-owned content")
	}
	if strings.Contains(prompt, "Both -> `both`") || strings.Contains(prompt, "4. **Review policy**") || strings.Contains(prompt, "Review: 400 lines") || strings.Contains(prompt, "800 lines") || strings.Contains(prompt, ", Other") || strings.Contains(prompt, "Other ->") || strings.Contains(prompt, "custom review budget") {
		return fmt.Errorf("generated prompt retained a selectable or legacy review budget")
	}
	return nil
}
func issue3336Journeys() []Journey {
	return []Journey{{ID: "j3336-opencode-sdd-sync-preserves-user-prompt", Review: reviewUntouched, Title: "OpenCode SDD sync preserves user prompt while canonicalizing session preflight", Source: "https://github.com/Gentleman-Programming/gentle-ai/issues/3336", Steps: []Step{{Name: "fixture: pre-existing user-owned OpenCode config", Fixture: issue3336Fixture}, {Name: "public OpenCode SDD sync", Requires: &Capability{Verb: []string{"sync"}, Flags: []string{"--agents", "--sdd-mode", "--sdd-profile-strategy"}}, Args: func(*Sandbox) ([]string, error) {
		return []string{"sync", "--agents", "opencode", "--sdd-mode", "multi", "--sdd-profile-strategy", "external-single-active"}, nil
	}, After: issue3336AssertGeneratedPrompt}}}}
}
