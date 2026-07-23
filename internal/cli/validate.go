package cli

import (
	"fmt"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/system"
)

type InstallInput struct {
	Selection model.Selection
	Scope     InstallScope
	Channel   InstallChannel
	DryRun    bool
}

func NormalizeInstallFlags(flags InstallFlags, detection system.DetectionResult) (InstallInput, error) {
	selection := model.Selection{}

	agents := defaultAgentsFromDetection(detection)
	if len(flags.Agents) > 0 {
		agents = asAgentIDs(flags.Agents)
	}
	selection.Agents = unique(agents)

	persona, err := normalizePersona(flags.Persona)
	if err != nil {
		return InstallInput{}, err
	}
	selection.Persona = persona

	preset, err := normalizePreset(flags.Preset)
	if err != nil {
		return InstallInput{}, err
	}
	selection.Preset = preset

	components, err := normalizeComponents(flags.Components, selection.Preset, selection.Persona)
	if err != nil {
		return InstallInput{}, err
	}
	if len(flags.Components) == 0 && strings.TrimSpace(flags.Preset) == "" && isPiOnlyAgents(selection.Agents) {
		components = piOnlyComponents()
	}

	selection.Components = components

	skills, err := normalizeSkills(flags.Skills)
	if err != nil {
		return InstallInput{}, err
	}
	selection.Skills = skills

	sddMode, err := normalizeSDDMode(flags.SDDMode)
	if err != nil {
		return InstallInput{}, err
	}
	selection.SDDMode = sddMode

	scope, err := ResolveInstallScope(flags.Scope)
	if err != nil {
		return InstallInput{}, err
	}

	channel, err := ResolveInstallChannel(flags.Channel)
	if err != nil {
		return InstallInput{}, err
	}

	return InstallInput{Selection: selection, Scope: scope, Channel: channel, DryRun: flags.DryRun}, nil
}

func normalizePersona(value string) (model.PersonaID, error) {
	if strings.TrimSpace(value) == "" {
		return model.PersonaGentleman, nil
	}

	switch model.PersonaID(value) {
	case model.PersonaGentleman, model.PersonaGentlemanNeutralArtifacts, model.PersonaNeutral, model.PersonaCustom:
		return model.PersonaID(value), nil
	default:
		return "", fmt.Errorf("unsupported persona %q", value)
	}
}

func normalizePreset(value string) (model.PresetID, error) {
	if strings.TrimSpace(value) == "" {
		return model.PresetFullGentleman, nil
	}

	switch model.PresetID(value) {
	case model.PresetFullGentleman, model.PresetEcosystemOnly, model.PresetMinimal, model.PresetCustom:
		return model.PresetID(value), nil
	default:
		return "", fmt.Errorf("unsupported preset %q", value)
	}
}

func normalizeComponents(values []string, preset model.PresetID, persona model.PersonaID) ([]model.ComponentID, error) {
	if len(values) == 0 {
		return componentsForPreset(preset, persona), nil
	}

	allowed := map[model.ComponentID]struct{}{}
	for _, component := range catalog.MVPComponents() {
		allowed[component.ID] = struct{}{}
	}

	components := []model.ComponentID{}
	for _, raw := range values {
		component := model.ComponentID(raw)
		if _, ok := allowed[component]; !ok {
			return nil, fmt.Errorf("unsupported component %q", raw)
		}
		components = append(components, component)
	}

	return unique(components), nil
}

func normalizeSkills(values []string) ([]model.SkillID, error) {
	if len(values) == 0 {
		return nil, nil
	}

	allowed := map[model.SkillID]struct{}{}
	for _, skill := range catalog.MVPSkills() {
		allowed[skill.ID] = struct{}{}
	}

	skills := []model.SkillID{}
	for _, raw := range values {
		skill := model.SkillID(raw)
		if _, ok := allowed[skill]; !ok {
			return nil, fmt.Errorf("unsupported skill %q", raw)
		}
		skills = append(skills, skill)
	}

	return unique(skills), nil
}

func normalizeSDDMode(value string) (model.SDDModeID, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}

	switch model.SDDModeID(value) {
	case model.SDDModeSingle, model.SDDModeMulti:
		return model.SDDModeID(value), nil
	default:
		return "", fmt.Errorf("unsupported sdd-mode %q (valid: single, multi)", value)
	}
}

func componentsForPreset(preset model.PresetID, persona model.PersonaID) []model.ComponentID {
	return model.ComponentsForPreset(preset, persona)
}

func defaultAgentsFromDetection(detection system.DetectionResult) []model.AgentID {
	agents := []model.AgentID{}
	for _, state := range detection.Configs {
		if !state.Exists {
			continue
		}

		switch normalized := model.NormalizeAgentID(state.Agent); normalized {
		case model.AgentClaudeCode, model.AgentOpenCode, model.AgentKilocode, model.AgentCursor,
			model.AgentVSCodeCopilot, model.AgentCodex, model.AgentAntigravity, model.AgentWindsurf,
			model.AgentKimi, model.AgentQwenCode, model.AgentKiroIDE, model.AgentOpenClaw,
			model.AgentPi, model.AgentTrae, model.AgentHermes:
			agents = append(agents, normalized)
		}
	}

	if len(agents) > 0 {
		return agents
	}

	catalogAgents := catalog.AllAgents()
	agents = make([]model.AgentID, 0, len(catalogAgents))
	for _, agent := range catalogAgents {
		agents = append(agents, agent.ID)
	}

	return agents
}

func asAgentIDs(values []string) []model.AgentID {
	agents := make([]model.AgentID, 0, len(values))
	for _, value := range values {
		agents = append(agents, model.NormalizeAgentID(value))
	}

	return agents
}

func isPiOnlyAgents(agents []model.AgentID) bool {
	return len(agents) == 1 && agents[0] == model.AgentPi
}

func piOnlyComponents() []model.ComponentID {
	return []model.ComponentID{model.ComponentEngram}
}

func unique[T comparable](items []T) []T {
	seen := make(map[T]struct{}, len(items))
	result := make([]T, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}

	return result
}
