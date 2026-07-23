package assets

import (
	"strings"
	"testing"
)

// TestCoordinatorOrchestratorsCarryLosslessBlockingPromptRule pins the fix for
// issue #615: orchestrators must never summarize a blocking prompt returned by
// a sub-agent or tool. Variants with a native interactive question tool must
// re-issue the prompt through that tool with every option grouped and intact;
// the rest must restate every option. Windsurf is excluded because it is a
// solo-agent platform with no sub-agents.
func TestCoordinatorOrchestratorsCarryLosslessBlockingPromptRule(t *testing.T) {
	const heading = "**Lossless Blocking Prompts**"

	// Enumerate all sdd-orchestrator.md files discovered in assets.
	allPaths := allSDDOrchestratorAssetPaths(t)

	// Map discovered paths to their expected question-tool wording.
	// "" means fallback restatement shape.
	expectedTools := map[string]string{
		"claude/sdd-orchestrator.md":      "`AskUserQuestion` tool",
		"opencode/sdd-orchestrator.md":    "`question` tool",
		"cursor/sdd-orchestrator.md":      "",
		"gemini/sdd-orchestrator.md":      "",
		"generic/sdd-orchestrator.md":     "",
		"hermes/sdd-orchestrator.md":      "",
		"kimi/sdd-orchestrator.md":        "",
		"qwen/sdd-orchestrator.md":        "",
		"kiro/sdd-orchestrator.md":        "",
		"antigravity/sdd-orchestrator.md": "",
		"codex/sdd-orchestrator.md":       "",
	}

	for _, path := range allPaths {
		t.Run(path, func(t *testing.T) {
			content := MustRead(path)

			// Windsurf is excluded from the rule but must declare "There are no sub-agents".
			if strings.HasPrefix(path, "windsurf/") {
				if !strings.Contains(content, "There are no sub-agents") {
					t.Fatalf("%s solo-agent exclusion guard: must contain 'There are no sub-agents'; if this wording disappeared, re-evaluate the exclusion", path)
				}
				// Windsurf exclusion: skip the rule check.
				return
			}

			// All other variants must carry the rule.
			// Check that this is an orchestrator file (contains orchestrator/delegation language).
			if !strings.Contains(content, "orchestrator") && !strings.Contains(content, "COORDINATOR") {
				t.Fatalf("%s no longer carries orchestrator language; update this contract test deliberately", path)
			}

			idx := strings.Index(content, heading)
			if idx == -1 {
				t.Fatalf("%s missing the %s rule", path, heading)
			}

			// Extract the rule (first line after heading).
			rule := content[idx:]
			if end := strings.Index(rule, "\n"); end != -1 {
				rule = rule[:end]
			}

			// All variants must forbid summarizing.
			if !strings.Contains(rule, "Never summarize or abbreviate the option list") {
				t.Fatalf("%s lossless rule must forbid summarizing the option list: %q", path, rule)
			}

			// Variants with a question tool must re-issue through that tool.
			questionTool, ok := expectedTools[path]
			if !ok {
				// Unknown discovered variant: require fallback shape.
				questionTool = ""
			}

			if questionTool != "" {
				// Claude and OpenCode: re-issue through their respective tools.
				if !strings.Contains(rule, "re-issue it through the "+questionTool) {
					t.Fatalf("%s lossless rule must re-issue blocking prompts through the %s: %q", path, questionTool, rule)
				}
				if !strings.Contains(rule, "never render it as plain chat text") {
					t.Fatalf("%s lossless rule must forbid plain-chat-text rendering: %q", path, rule)
				}
				if !strings.Contains(rule, "as a single grouped call with every option intact") {
					t.Fatalf("%s lossless rule must require a single grouped call with every option intact: %q", path, rule)
				}
				return
			}

			// Fallback variants (kiro, antigravity, codex, cursor, etc.): restate every option.
			if !strings.Contains(rule, "restate every single option fully") {
				t.Fatalf("%s lossless rule must require full restatement: %q", path, rule)
			}
		})
	}
}
