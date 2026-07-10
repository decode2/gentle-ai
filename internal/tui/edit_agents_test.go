package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/system"
	"github.com/gentleman-programming/gentle-ai/internal/tui/screens"
)

// ─── Edit installed agents shortcut ───────────────────────────────────────

// TestEditAgents_WelcomeDispatchesScreenEditAgents verifies that selecting
// "Edit installed agents" from the Welcome menu sets EditAgentsMode and
// navigates to ScreenEditAgents.
func TestEditAgents_WelcomeDispatchesScreenEditAgents(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenWelcome
	// "Edit installed agents" is at index 7 (0-based) when showProfiles=false:
	// 0=Start, 1=Upgrade, 2=Sync, 3=Upgrade+Sync, 4=Configure models,
	// 5=Create Agent, 6=OC Plugins, 7=Edit installed agents
	m.Cursor = 7

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.Screen != ScreenEditAgents {
		t.Fatalf("Welcome cursor=7: screen = %v, want ScreenEditAgents", got.Screen)
	}
	if !got.EditAgentsMode {
		t.Fatalf("EditAgentsMode should be true after entering ScreenEditAgents from Welcome")
	}
}

// TestEditAgents_SpaceTogglesAgent verifies that pressing space on ScreenEditAgents
// toggles the agent at the cursor position in EditAgentsSelection.
func TestEditAgents_SpaceTogglesAgent(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.EditAgentsSelection = append([]model.AgentID(nil), m.Selection.Agents...)
	m.Cursor = 0

	before := len(m.EditAgentsSelection)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	after := len(got.EditAgentsSelection)

	if after == before {
		t.Fatalf("spacebar on ScreenEditAgents should toggle agent in draft selection; selection unchanged at %d agents", before)
	}
}

// TestEditAgents_ConfirmTransitionsToSync verifies that confirming with at least
// one agent selected transitions to ScreenSync, clears EditAgentsMode, and
// populates PendingSyncOverrides.TargetAgents.
func TestEditAgents_ConfirmTransitionsToSync(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.EditAgentsSelection = screens.AgentOptions()[:1]
	m.Cursor = len(screens.AgentOptions()) // "Next" button

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.Screen != ScreenSync {
		t.Fatalf("ScreenEditAgents confirm: screen = %v, want ScreenSync", got.Screen)
	}
	if got.EditAgentsMode {
		t.Fatalf("EditAgentsMode should be cleared after confirm")
	}
	if got.PendingSyncOverrides == nil {
		t.Fatalf("PendingSyncOverrides should be set after EditAgents confirm")
	}
	if len(got.PendingSyncOverrides.TargetAgents) == 0 {
		t.Fatalf("PendingSyncOverrides.TargetAgents should contain the selected agents")
	}
}

// TestEditAgents_BackButtonReturnsToWelcome verifies that pressing Enter on the
// "Back" button (cursor = agentCount+1) from ScreenEditAgents returns to ScreenWelcome.
func TestEditAgents_BackButtonReturnsToWelcome(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.Cursor = len(screens.AgentOptions()) + 1 // "Back" button

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.Screen != ScreenWelcome {
		t.Fatalf("ScreenEditAgents back: screen = %v, want ScreenWelcome", got.Screen)
	}
	if got.EditAgentsMode {
		t.Fatalf("EditAgentsMode should be cleared on back")
	}
}

// TestEditAgents_EscReturnsToWelcome verifies that pressing Esc from ScreenEditAgents
// navigates back to ScreenWelcome via the router.
func TestEditAgents_EscReturnsToWelcome(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)

	if got.Screen != ScreenWelcome {
		t.Fatalf("Esc from ScreenEditAgents: screen = %v, want ScreenWelcome", got.Screen)
	}
}

// TestEditAgents_DeselectingAgentCleansUpConfig verifies that if an agent is
// deselected during the Edit Installed Agents flow, its configurations are
// cleaned up (uninstalled) upon confirmation.
func TestEditAgents_DeselectingAgentCleansUpConfig(t *testing.T) {
	// Initialize the TUI model
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true

	// Simulate that the user previously had two agents selected/installed
	m.Selection.Agents = []model.AgentID{model.AgentClaudeCode, model.AgentOpenCode}

	// The user deselects open-code, so only claude-code remains in EditAgentsSelection
	m.EditAgentsSelection = []model.AgentID{model.AgentClaudeCode}

	// We set a custom SyncFn that intercepts the overrides
	var capturedOverrides *model.SyncOverrides
	m.SyncFn = func(overrides *model.SyncOverrides) ([]string, error) {
		capturedOverrides = overrides
		return nil, nil
	}

	// Cursor on the "Continue" action
	agentCount := len(screens.AgentOptions())
	m.Cursor = agentCount

	// Press Enter to confirm selection on ScreenEditAgents
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	// Verify TUI screen transition
	if got.Screen != ScreenSync {
		t.Fatalf("expected ScreenSync, got %v", got.Screen)
	}

	// Press Enter on ScreenSync to trigger the sync command execution
	_, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from ScreenSync enter")
	}

	_ = findSyncDoneMsgInBatch(t, cmd)

	// Verify captured overrides contains the deselected agent (open-code)
	if capturedOverrides == nil {
		t.Fatal("expected capturedOverrides to be non-nil")
	}
	if len(capturedOverrides.DeselectedAgents) != 1 || capturedOverrides.DeselectedAgents[0] != model.AgentOpenCode {
		t.Fatalf("expected deselected agents list to be [opencode], got %v", capturedOverrides.DeselectedAgents)
	}
}
