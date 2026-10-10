package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

func experienceKey(m Model, key tea.KeyMsg) Model {
	model, _ := m.Update(key)
	return model.(Model)
}

func experienceStart(t *testing.T) Model {
	t.Helper()
	m := NewModel(system.DetectionResult{}, "dev").WithShellInstaller()
	m.Width, m.Height, m.Cursor = 120, 30, 11
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "Configure experience") {
		t.Fatal("ordinary installer skips Configure")
	}
	return m
}

func TestShellExperienceConfigureDestinationReady(t *testing.T) {
	m := experienceStart(t)
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // neutral
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // unmanaged
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // background off
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	state, ok := cli.ShellInstallPresentationOf(m.shellInstallModel)
	if !ok || state.Request.Experience == nil || state.Request.Experience.Persona != "unmanaged" || state.Request.Experience.BackgroundSubagents != "off" {
		t.Fatal("Configure lost profile at Destination")
	}
	target := filepath.Join(t.TempDir(), "target")
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(target)})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	state, _ = cli.ShellInstallPresentationOf(m.shellInstallModel)
	if !state.Review || state.Request.Confirmation == "" {
		t.Fatalf("working profile cannot reach Ready: %+v", state)
	}
	for _, text := range []string{"Ready", "Selected experience", "Changes + scopes", "unmanaged", "gentle-shell"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("Ready hides %q", text)
		}
	}
	for _, size := range [][2]int{{120, 30}, {80, 14}, {30, 7}, {5, 1}} {
		m.Width, m.Height = size[0], size[1]
		for _, key := range []tea.KeyType{tea.KeyHome, tea.KeyEnd} {
			m = experienceKey(m, tea.KeyMsg{Type: key})
			lines := strings.Split(m.View(), "\n")
			if len(lines) > m.Height {
				t.Fatal("Ready exceeds terminal height")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > m.Width {
					t.Fatal("Ready exceeds terminal width")
				}
			}
		}
	}
	m.Width, m.Height = 120, 30
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // gentleman
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	state, _ = cli.ShellInstallPresentationOf(m.shellInstallModel)
	if state.Review || state.Request.Confirmation != "" || state.Request.Destination != target || state.Request.Experience.Persona != "gentleman" {
		t.Fatal("editing configuration retains old consent or loses destination")
	}
}

func TestShellExperiencePreservesUnsupportedChoicesAndCancel(t *testing.T) {
	m := experienceStart(t)
	for i := 0; i < 2; i++ {
		m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // Strict TDD on
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(filepath.Join(t.TempDir(), "target"))})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	state, _ := cli.ShellInstallPresentationOf(m.shellInstallModel)
	if state.Error == nil || state.Review || state.Request.Experience.StrictTDD != "on" {
		t.Fatal("unsupported choice dropped or admitted")
	}
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		cancel := experienceStart(t)
		cancel = experienceKey(cancel, tea.KeyMsg{Type: key})
		if cancel.Screen != ScreenWelcome || cancel.ShellInstallSelection().Confirmed {
			t.Fatal("Configure cancel installs or leaves Welcome")
		}
	}
}
