package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

func TestMainShellExperienceConfiguration(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "test").WithShellInstaller()
	shellInstallIdleUpdate(t, &m, tea.WindowSizeMsg{Width: 120, Height: 50})
	shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Configure Gentle-Shell experience") || !strings.Contains(view, "About: Gentleman") {
		t.Fatal("main entry still renders the raw destination form")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyEnter}, {Type: tea.KeyDown}, {Type: tea.KeyDown}, {Type: tea.KeyEnter}, {Type: tea.KeyDown}, {Type: tea.KeyEnter}} {
		shellInstallIdleUpdate(t, &m, key)
	}
	state, ok := cli.ShellInstallPresentationOf(m.shellInstall.child)
	if !ok || state.Request.Experience.Persona != "neutral" || state.Request.Experience.BackgroundSubagents != "off" {
		t.Fatalf("configured experience not handed to destination step: %+v", state)
	}
	if !strings.Contains(ansi.Strip(m.View()), "Choose installation destination") {
		t.Fatal("destination step lost grouped presentation")
	}
	// Escape preserves the established cancel-to-Welcome contract.
	shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEsc})
	if _, confirmed := m.ShellInstallSelection(); confirmed || m.Screen != ScreenWelcome {
		t.Fatal("configuration cancellation retained consent")
	}
}

func TestMainShellExperienceReviewActions(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "qshell")
	for _, action := range []string{"back", "cancel", "install"} {
		t.Run(action, func(t *testing.T) {
			m := NewModel(system.DetectionResult{}, "test").WithShellInstaller()
			shellInstallIdleUpdate(t, &m, tea.WindowSizeMsg{Width: 120, Height: 50})
			shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			shellExperienceContinue(t, &m)
			shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(target)})
			shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			if view := ansi.Strip(m.View()); !strings.Contains(view, "Ready to install Gentle-Shell") || !strings.Contains(view, "About:") || !strings.Contains(view, "gentleman") {
				t.Fatalf("not the agreed review:\n%s", view)
			}
			if action != "install" {
				shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyDown})
			}
			if action == "cancel" {
				shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyDown})
			}
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
			_, confirmed := m.ShellInstallSelection()
			if confirmed != (action == "install") || (cmd != nil) != confirmed {
				t.Fatal("review actions did not preserve explicit parent-only consent")
			}
			if action == "back" && !strings.Contains(ansi.Strip(m.View()), "Choose installation destination") {
				t.Fatal("review back did not return to physical editing")
			}
			if action == "cancel" && m.Screen != ScreenWelcome {
				t.Fatal("review cancel did not return to welcome")
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("review installed a target")
			}
		})
	}
}

func TestMainShellExperienceCancelBeforeDestination(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}, {Type: tea.KeyEnter}} {
		m := NewModel(system.DetectionResult{}, "test").WithShellInstaller()
		shellInstallIdleUpdate(t, &m, tea.WindowSizeMsg{Width: 120, Height: 50})
		shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
		if key.Type == tea.KeyEnter {
			for i := 0; i < 5; i++ {
				shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyDown})
			}
		}
		shellInstallIdleUpdate(t, &m, key)
		if _, confirmed := m.ShellInstallSelection(); confirmed || m.Screen != ScreenWelcome || m.shellInstall.child != nil {
			t.Fatal("cancel before destination retained a child or authority")
		}
	}
}

func shellReviewVisibleData(view string) string {
	var data strings.Builder
	for _, row := range strings.Split(ansi.Strip(view), "\n") {
		if _, right, ok := strings.Cut(row, "│"); ok {
			row = right
		}
		data.WriteString(strings.Join(strings.Fields(row), ""))
	}
	return data.String()
}

func shellExperienceContinue(t *testing.T, m *Model) {
	t.Helper()
	for i := 0; i < 4; i++ {
		shellInstallIdleUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	shellInstallIdleUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}
