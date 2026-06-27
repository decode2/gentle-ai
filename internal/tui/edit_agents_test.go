package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
// toggles the agent at the cursor position.
func TestEditAgents_SpaceTogglesAgent(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.Cursor = 0

	before := len(m.Selection.Agents)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	after := len(got.Selection.Agents)

	if after == before {
		t.Fatalf("spacebar on ScreenEditAgents should toggle agent; selection unchanged at %d agents", before)
	}
}

// TestEditAgents_ConfirmTransitionsToSync verifies that confirming with at least
// one agent selected transitions to ScreenSync, clears EditAgentsMode, and
// populates PendingSyncOverrides.TargetAgents.
func TestEditAgents_ConfirmTransitionsToSync(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.Selection.Agents = screens.AgentOptions()[:1]
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
