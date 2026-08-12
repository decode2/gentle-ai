package cli

import (
	"fmt"
	"os"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
	"github.com/gentleman-programming/gentle-ai/v2/internal/verify"
)

const OpenCodeBackgroundSubagentsEnv = "GENTLE_AI_OPENCODE_BACKGROUND_SUBAGENTS"

// OpenCodeBackgroundResolveInput contains already-discovered sources. The
// resolver is pure so loading state and reading the process environment remain
// caller responsibilities in the activation child.
type OpenCodeBackgroundResolveInput struct {
	CLISet       bool
	CLIValue     model.OpenCodeBackgroundIntent
	EnvSet       bool
	EnvValue     string
	PriorManaged model.OpenCodeBackgroundIntent
	Interactive  bool
}

// OpenCodeBackgroundResolution separates the winning intent from the concrete
// runtime outcome. Persist is non-empty only for a newly supplied on/off
// choice; prior state is consulted by auto but is never republished here.
type OpenCodeBackgroundResolution struct {
	Intent      model.OpenCodeBackgroundIntent
	Effective   model.OpenCodeBackgroundIntent
	Persist     model.OpenCodeBackgroundIntent
	NeedsPrompt bool
}

// ResolveOpenCodeBackground applies CLI > non-empty env > prior managed state
// > auto. Auto consults prior on/off state; only implicit unresolved auto
// prompts interactively, while explicit auto stays auto without enabling.
func ResolveOpenCodeBackground(input OpenCodeBackgroundResolveInput) (OpenCodeBackgroundResolution, error) {
	cliValue, err := parseBackgroundIntent("explicit CLI", string(input.CLIValue), input.CLISet)
	if err != nil {
		return OpenCodeBackgroundResolution{}, err
	}
	envValue, err := parseBackgroundIntent(OpenCodeBackgroundSubagentsEnv, input.EnvValue, input.EnvSet && input.EnvValue != "")
	if err != nil {
		return OpenCodeBackgroundResolution{}, err
	}
	prior, err := parseBackgroundIntent("prior managed state", string(input.PriorManaged), input.PriorManaged != "")
	if err != nil {
		return OpenCodeBackgroundResolution{}, err
	}

	selected := model.OpenCodeBackgroundAuto
	explicit := false
	switch {
	case input.CLISet:
		selected = cliValue
		explicit = true
	case input.EnvSet && input.EnvValue != "":
		selected = envValue
		explicit = true
	case prior == model.OpenCodeBackgroundOn || prior == model.OpenCodeBackgroundOff:
		selected = prior
	}

	result := OpenCodeBackgroundResolution{Intent: selected}
	switch selected {
	case model.OpenCodeBackgroundOn, model.OpenCodeBackgroundOff:
		result.Effective = selected
		if explicit {
			result.Persist = selected
		}
	case model.OpenCodeBackgroundAuto:
		// Explicit/env auto is still auto policy: an existing managed choice is
		// safe to reuse, but auto never creates a managed choice by itself.
		if prior == model.OpenCodeBackgroundOn || prior == model.OpenCodeBackgroundOff {
			result.Effective = prior
		} else if explicit && input.Interactive {
			result.Effective = model.OpenCodeBackgroundAuto
		} else if input.Interactive {
			result.Effective = model.OpenCodeBackgroundAuto
			result.NeedsPrompt = true
		} else {
			result.Effective = model.OpenCodeBackgroundOff
		}
	}
	return result, nil
}

func parseBackgroundIntent(source, raw string, present bool) (model.OpenCodeBackgroundIntent, error) {
	if !present {
		return "", nil
	}
	parsed, err := model.ParseOpenCodeBackgroundIntent(raw)
	if err != nil {
		return "", fmt.Errorf("%s=%q: %w; use auto, on, or off", source, raw, err)
	}
	return parsed, nil
}

func resolveOpenCodeBackgroundCLI(set bool, raw string, persisted state.InstallState) (OpenCodeBackgroundResolution, error) {
	envValue, envSet := os.LookupEnv(OpenCodeBackgroundSubagentsEnv)
	return ResolveOpenCodeBackground(OpenCodeBackgroundResolveInput{
		CLISet:       set,
		CLIValue:     model.OpenCodeBackgroundIntent(raw),
		EnvSet:       envSet,
		EnvValue:     envValue,
		PriorManaged: persisted.BackgroundIntent,
		Interactive:  false,
	})
}

func withOpenCodeBackgroundPending(report verify.Report, resolution OpenCodeBackgroundResolution, runtimeReady bool, agents []model.AgentID) verify.Report {
	if !containsAgent(agents, model.AgentOpenCode) || resolution.Effective != model.OpenCodeBackgroundOn || runtimeReady {
		return report
	}
	report.FinalNote += " OpenCode background policy is prepared but runtime activation remains pending; execution stays foreground until upstream capability activation is available."
	return report
}
