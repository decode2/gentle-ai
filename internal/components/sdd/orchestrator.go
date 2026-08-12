package sdd

import (
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/assets"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

const openCodeBackgroundAddendumMarker = "<!-- gentle-ai:opencode-background-subagents -->"

// composeOrchestratorPrompt is the renderer-owned source seam for every SDD
// orchestrator. It composes the selected historical asset before the existing
// bounded-review and runtime-identity substitutions. The provider addendum is
// intentionally empty until the next issue #3043 slice adds OpenCode policy.
func composeOrchestratorPrompt(agent model.AgentID) string {
	path := sddOrchestratorAsset(agent)
	content := assets.MustRead(path)
	if addendum := resolveOrchestratorProviderAddendum(agent); addendum != "" {
		content = appendOrchestratorProviderAddendum(content, addendum)
	}
	return bindRuntimeAgentIdentity(renderBoundedReviewAssetBodyFromContent(path, content), agent)
}

// resolveOrchestratorProviderAddendum reserves one OpenCode-only extension
// point. Returning empty is deliberate: this slice must not change prompt
// bytes, and Kilocode must never inherit the later OpenCode policy.
func resolveOrchestratorProviderAddendum(agent model.AgentID) string {
	if agent != model.AgentOpenCode {
		return ""
	}
	return ""
}

func appendOrchestratorProviderAddendum(content, addendum string) string {
	if strings.Contains(content, openCodeBackgroundAddendumMarker) {
		return content
	}
	return strings.TrimRight(content, "\n") + "\n\n" + openCodeBackgroundAddendumMarker + "\n" + strings.TrimSpace(addendum) + "\n"
}

// sddOrchestratorAsset returns the embedded asset path for the SDD orchestrator
// content based on the agent. Agent-specific assets take priority; generic is fallback.
func sddOrchestratorAsset(agent model.AgentID) string {
	switch agent {
	case model.AgentClaudeCode:
		return "claude/sdd-orchestrator.md"
	case model.AgentGeminiCLI:
		return "gemini/sdd-orchestrator.md"
	case model.AgentCodex:
		return "codex/sdd-orchestrator.md"
	case model.AgentAntigravity:
		return "antigravity/sdd-orchestrator.md"
	case model.AgentWindsurf:
		return "windsurf/sdd-orchestrator.md"
	case model.AgentCursor:
		return "cursor/sdd-orchestrator.md"
	case model.AgentKimi:
		return "kimi/sdd-orchestrator.md"
	case model.AgentQwenCode:
		return "qwen/sdd-orchestrator.md"
	case model.AgentKiroIDE:
		return "kiro/sdd-orchestrator.md"
	case model.AgentHermes:
		return "hermes/sdd-orchestrator.md"
	case model.AgentOpenCode, model.AgentKilocode:
		return "opencode/sdd-orchestrator.md"
	default:
		return "generic/sdd-orchestrator.md"
	}
}
