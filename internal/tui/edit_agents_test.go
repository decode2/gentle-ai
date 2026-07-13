package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/system"
	"github.com/gentleman-programming/gentle-ai/internal/tui/screens"
)

func TestEditAgents_WelcomeDispatchesScreenEditAgents(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenWelcome
	m.Cursor = 11

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.Screen != ScreenEditAgents {
		t.Fatalf("Welcome cursor=11: screen = %v, want ScreenEditAgents", got.Screen)
	}
	if !got.EditAgentsMode {
		t.Fatalf("EditAgentsMode should be true after entering ScreenEditAgents from Welcome")
	}
}

func TestEditAgents_SpaceTogglesAgent(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.EditAgentsSelection = append([]model.AgentID(nil), m.Selection.Agents...)
	m.Cursor = 0

	want := screens.AgentOptions()[0]
	wasSelected := slices.Contains(m.EditAgentsSelection, want)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	if slices.Contains(got.EditAgentsSelection, want) == wasSelected {
		t.Fatalf("agent %s was not toggled: %v", want, got.EditAgentsSelection)
	}
}

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

func TestEditAgents_BackButtonReturnsToWelcome(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.Cursor = len(screens.AgentOptions()) + 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.Screen != ScreenWelcome {
		t.Fatalf("ScreenEditAgents back: screen = %v, want ScreenWelcome", got.Screen)
	}
	if got.EditAgentsMode {
		t.Fatalf("EditAgentsMode should be cleared on back")
	}
}

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

func TestEditAgents_DeselectingAgentCleansUpConfig(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true

	m.Selection.Agents = []model.AgentID{model.AgentClaudeCode, model.AgentOpenCode}
	m.EditAgentsSelection = []model.AgentID{model.AgentClaudeCode}
	var capturedOverrides *model.SyncOverrides
	m.SyncFn = func(overrides *model.SyncOverrides) ([]string, error) {
		capturedOverrides = overrides
		return nil, nil
	}

	agentCount := len(screens.AgentOptions())
	m.Cursor = agentCount

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.Screen != ScreenSync {
		t.Fatalf("expected ScreenSync, got %v", got.Screen)
	}

	_, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from ScreenSync enter")
	}

	_ = findSyncDoneMsgInBatch(t, cmd)

	if capturedOverrides == nil {
		t.Fatal("expected capturedOverrides to be non-nil")
	}
	if len(capturedOverrides.DeselectedAgents) != 1 || capturedOverrides.DeselectedAgents[0] != model.AgentOpenCode {
		t.Fatalf("expected deselected agents list to be [opencode], got %v", capturedOverrides.DeselectedAgents)
	}
}

func TestEditAgents_SelectionCommitsOnlyAfterSuccessfulSync(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want model.AgentID
	}{
		{name: "success", want: model.AgentClaudeCode},
		{name: "failure", err: fmt.Errorf("sync failed"), want: model.AgentOpenCode},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(system.DetectionResult{}, "dev")
			m.Selection.Agents = []model.AgentID{model.AgentOpenCode}
			m.PendingAgentSelection = []model.AgentID{model.AgentClaudeCode}
			updated, _ := m.Update(SyncDoneMsg{Err: tt.err})
			got := updated.(Model)
			if len(got.Selection.Agents) != 1 || got.Selection.Agents[0] != tt.want {
				t.Fatalf("agents = %v, want [%s]", got.Selection.Agents, tt.want)
			}
			if got.PendingAgentSelection != nil {
				t.Fatal("pending selection was not cleared")
			}
		})
	}
}

func TestEditAgents_EmptySelectionDoesNotSync(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	m.EditAgentsMode = true
	m.Cursor = len(screens.AgentOptions())

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.Screen != ScreenEditAgents || got.PendingSyncOverrides != nil {
		t.Fatalf("empty confirmation changed state: screen=%v overrides=%+v", got.Screen, got.PendingSyncOverrides)
	}
}

func TestEditAgents_RendersOnNarrowScreen(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenEditAgents
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 45, Height: 20})
	if view := updated.(Model).View(); !strings.Contains(view, "Edit Installed Agents") {
		t.Fatalf("narrow view omitted edit screen: %q", view)
	}
}

func TestEditAgents_CancelledSyncDiscardsPendingSelection(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenSync
	m.Selection.Agents = []model.AgentID{model.AgentOpenCode}
	m.PendingAgentSelection = []model.AgentID{model.AgentClaudeCode}
	m.PendingSyncOverrides = &model.SyncOverrides{TargetAgents: []model.AgentID{model.AgentClaudeCode}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.PendingAgentSelection != nil || got.PendingSyncOverrides != nil {
		t.Fatalf("cancel retained pending edit: selection=%v overrides=%+v", got.PendingAgentSelection, got.PendingSyncOverrides)
	}
	if len(got.Selection.Agents) != 1 || got.Selection.Agents[0] != model.AgentOpenCode {
		t.Fatalf("committed selection changed on cancel: %v", got.Selection.Agents)
	}
}
