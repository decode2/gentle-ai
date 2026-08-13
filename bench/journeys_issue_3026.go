package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const issue3026LegacyDirectFallback = "Use OpenCode's native `explore` agent for read-only mapping and `general` agent for implementation or command execution."

var issue3026ProfileArgs = []string{"--profile", "lean:openai/gpt-5", "--profile-phase", "lean:gentle-reviewer:openai/gpt-5-mini", "--profile-phase", "lean:gentle-worker:openrouter/qwen/qwen3.6-plus:free"}

var issue3026SyncCapability = &Capability{
	Probe: append([]string{"sync", "--agents", "opencode", "--sdd-mode", "multi", "--dry-run"}, issue3026ProfileArgs...),
}

var issue3026InstallCapability = &Capability{
	Verb:  []string{"install"},
	Flags: []string{"--agent", "--component", "--sdd-mode"},
}

type issue3026Agent struct {
	Mode       string         `json:"mode"`
	Hidden     bool           `json:"hidden"`
	Prompt     string         `json:"prompt"`
	Model      string         `json:"model"`
	Permission map[string]any `json:"permission"`
}

type issue3026OpenCodeSettings struct {
	Agent map[string]issue3026Agent `json:"agent"`
}

// issue3026Journeys replays the non-SDD gap from #3026: a legacy orchestrator
// had only native direct fallbacks, while public install and sync must publish
// safe managed roles, conditional routing, and named profile model assignments.
func issue3026Journeys() []Journey {
	return []Journey{{
		ID:     "j100-opencode-managed-direct-role-profile-routing",
		Title:  "OpenCode install and sync replace native direct fallback with managed roles and profile models",
		Source: "https://github.com/Gentleman-Programming/gentle-ai/issues/3026",
		Steps: []Step{
			{Name: "fixture: repository", Fixture: baseRepo},
			{Name: "fixture: legacy non-SDD routing without managed roles", Fixture: seedIssue3026LegacyRouting},
			{Name: "public sync preserves Kilocode legacy SDD projection", Requires: issue3026SyncCapability, Args: func(*Sandbox) ([]string, error) {
				return append([]string{"sync", "--agents", "kilocode", "--sdd-mode", "multi"}, issue3026ProfileArgs...), nil
			}, After: assertIssue3026KilocodeProjection},
			{Name: "fixture: installed OpenCode runtime prerequisite", Fixture: issue3026InstalledOpenCodeRuntime},
			{
				Name:     "public install creates default managed roles",
				Requires: issue3026InstallCapability,
				Args: func(*Sandbox) ([]string, error) {
					return []string{
						"install", "--agent", "opencode", "--component", "sdd", "--sdd-mode", "multi",
					}, nil
				},
				After: assertIssue3026InstallCreatedRoles,
			},
			{
				Name:     "public sync refreshes managed roles and renders named profile assignments",
				Requires: issue3026SyncCapability,
				Args: func(*Sandbox) ([]string, error) {
					return append([]string{"sync", "--agents", "opencode", "--sdd-mode", "multi"}, issue3026ProfileArgs...), nil
				},
				After: assertIssue3026ManagedDirectRoles,
			},
		},
	}}
}

func assertIssue3026InstallCreatedRoles(sandbox *Sandbox, observation Observation) error {
	if observation.ExitCode != 0 {
		return fmt.Errorf("public install failed (bounded stderr): %s", issue3026BoundedStderr(observation.Stderr))
	}
	settings, err := readIssue3026Settings(sandbox)
	if err != nil {
		return err
	}
	for _, role := range []string{"gentle-reviewer", "gentle-worker"} {
		if _, ok := settings.Agent[role]; !ok {
			return fmt.Errorf("public install did not create managed role %q", role)
		}
	}
	return nil
}

// issue3026InstalledOpenCodeRuntime proves only the LookPath prerequisite used
// by the public install boundary. It copies the already-built test binary so
// the fixture is executable on every supported host, but it is intentionally
// never invoked as OpenCode and never consults or mutates operator config.
func issue3026InstalledOpenCodeRuntime(sandbox *Sandbox) error {
	binDir := filepath.Join(sandbox.Root, "runtime", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	path := sandboxExecutablePath(binDir, "opencode")
	binary, err := os.ReadFile(sandbox.Binary)
	if err != nil {
		return fmt.Errorf("read built test binary for private OpenCode runtime presence fixture: %w", err)
	}
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		return fmt.Errorf("write private OpenCode runtime presence fixture: %w", err)
	}
	sandbox.PathPrepend = binDir
	return nil
}

const issue3026InstallDiagnosticMaxBytes = 4096

var issue3026SecretPattern = regexp.MustCompile(`(?i)(\b(api[-_]?key|access[-_]?token|authorization|password|secret|token)\b\s*[:=]\s*)(bearer\s+)?[^\s,;]+`)
var issue3026BearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;]+`)

func issue3026BoundedStderr(stderr string) string {
	stderr = issue3026SecretPattern.ReplaceAllString(stderr, `${1}<redacted>`)
	stderr = issue3026BearerPattern.ReplaceAllString(stderr, "Bearer <redacted>")
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return "(no stderr emitted)"
	}
	if len(stderr) <= issue3026InstallDiagnosticMaxBytes {
		return stderr
	}
	const suffix = "\n[stderr truncated]"
	return stderr[:issue3026InstallDiagnosticMaxBytes-len(suffix)] + suffix
}

func seedIssue3026LegacyRouting(sandbox *Sandbox) error {
	path := issue3026SettingsPath(sandbox)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	settings := map[string]any{"agent": map[string]any{
		"gentle-orchestrator": map[string]any{
			"mode":   "primary",
			"prompt": issue3026LegacyDirectFallback,
		},
	}}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(sandbox.Repo, "openspec", "changes")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("legacy fixture unexpectedly has SDD changes: %v", err)
	}
	return nil
}

func assertIssue3026ManagedDirectRoles(sandbox *Sandbox, observation Observation) error {
	if observation.ExitCode != 0 {
		return fmt.Errorf("public sync failed: %s", firstLine(observation.Stderr))
	}
	if !strings.Contains(observation.Stdout, "Agents synced: opencode") {
		return fmt.Errorf("sync output did not identify OpenCode: %s", observation.Stdout)
	}

	settings, err := readIssue3026Settings(sandbox)
	if err != nil {
		return err
	}
	for _, role := range []struct {
		name string
		edit string
	}{
		{name: "gentle-reviewer", edit: "deny"},
		{name: "gentle-worker", edit: "allow"},
	} {
		agent, ok := settings.Agent[role.name]
		if !ok || agent.Mode != "subagent" || !agent.Hidden {
			return fmt.Errorf("managed role %q is not a hidden subagent: %#v", role.name, agent)
		}
		if agent.Permission["edit"] != role.edit || agent.Permission["task"] != "deny" || agent.Permission["question"] != "deny" {
			return fmt.Errorf("managed role %q has unsafe permissions: %#v", role.name, agent.Permission)
		}
		bash, ok := agent.Permission["bash"].(map[string]any)
		if !ok || bash["*"] != "deny" {
			return fmt.Errorf("managed role %q does not deny unspecified bash commands: %#v", role.name, agent.Permission["bash"])
		}
		if _, ok := agent.Permission["tools"]; ok {
			return fmt.Errorf("managed role %q still exposes deprecated tools: %#v", role.name, agent.Permission["tools"])
		}
	}

	orchestrator := settings.Agent["gentle-orchestrator"]
	for _, evidence := range []string{
		"when `gentle-reviewer` is defined in the active configuration",
		"when `gentle-worker` is defined in the active configuration",
		"explicit compatibility fallback only when the managed direct role is unavailable",
	} {
		if !strings.Contains(orchestrator.Prompt, evidence) {
			return fmt.Errorf("orchestrator routing omitted %q", evidence)
		}
	}
	if strings.Contains(orchestrator.Prompt, issue3026LegacyDirectFallback) {
		return fmt.Errorf("orchestrator retained the legacy unconditional fallback")
	}
	task, ok := orchestrator.Permission["task"].(map[string]any)
	if !ok || task["gentle-reviewer"] != "allow" || task["gentle-worker"] != "allow" {
		return fmt.Errorf("orchestrator cannot delegate to default direct roles: %#v", orchestrator.Permission["task"])
	}

	for _, assignment := range []struct {
		name  string
		model string
	}{
		{name: "sdd-orchestrator-lean", model: "openai/gpt-5"},
		{name: "gentle-reviewer-lean", model: "openai/gpt-5-mini"},
		{name: "gentle-worker-lean", model: "openrouter/qwen/qwen3.6-plus:free"},
	} {
		agent, ok := settings.Agent[assignment.name]
		if !ok || agent.Model != assignment.model {
			return fmt.Errorf("profile assignment %q = %#v, want model %q", assignment.name, agent, assignment.model)
		}
	}
	profilePrompt := settings.Agent["sdd-orchestrator-lean"].Prompt
	for _, evidence := range []string{
		"`gentle-reviewer-lean`",
		"`gentle-worker-lean`",
		"| gentle-reviewer-lean | openai/gpt-5-mini |",
		"| gentle-worker-lean | openrouter/qwen/qwen3.6-plus:free |",
	} {
		if !strings.Contains(profilePrompt, evidence) {
			return fmt.Errorf("named profile prompt omitted %q", evidence)
		}
	}
	profileTask, ok := settings.Agent["sdd-orchestrator-lean"].Permission["task"].(map[string]any)
	if !ok || profileTask["gentle-reviewer-lean"] != "allow" || profileTask["gentle-worker-lean"] != "allow" {
		return fmt.Errorf("named profile cannot delegate to its direct roles: %#v", settings.Agent["sdd-orchestrator-lean"].Permission["task"])
	}

	for _, statePath := range []string{
		filepath.Join(sandbox.Repo, "openspec", "changes"),
		filepath.Join(sandbox.Repo, ".git", "gentle-ai"),
	} {
		if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return fmt.Errorf("public sync entered lifecycle state at %q", statePath)
			}
			return fmt.Errorf("inspect lifecycle state %q: %w", statePath, err)
		}
	}
	return nil
}

func assertIssue3026KilocodeProjection(sandbox *Sandbox, observation Observation) error {
	if observation.ExitCode != 0 {
		return fmt.Errorf("Kilocode sync failed: %s", issue3026BoundedStderr(observation.Stderr))
	}
	data, err := os.ReadFile(filepath.Join(sandbox.Home, ".config", "kilo", "opencode.json"))
	if err != nil {
		return err
	}
	text := string(data)
	if strings.Contains(text, "gentle-reviewer") || strings.Contains(text, "gentle-worker") || !strings.Contains(text, `"sdd-apply"`) {
		return fmt.Errorf("Kilocode projection lost legacy SDD compatibility or received direct roles")
	}
	return nil
}

func issue3026SettingsPath(sandbox *Sandbox) string {
	return filepath.Join(sandbox.Home, ".config", "opencode", "opencode.json")
}

func readIssue3026Settings(sandbox *Sandbox) (issue3026OpenCodeSettings, error) {
	data, err := os.ReadFile(issue3026SettingsPath(sandbox))
	if err != nil {
		return issue3026OpenCodeSettings{}, fmt.Errorf("read rendered OpenCode settings: %w", err)
	}
	var settings issue3026OpenCodeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return issue3026OpenCodeSettings{}, fmt.Errorf("decode rendered OpenCode settings: %w", err)
	}
	return settings, nil
}
