//go:build linux && amd64

package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
)

func TestShellExperienceCodeGraphReachesReady(t *testing.T) {
	for _, choice := range []string{"on", "off"} {
		t.Run(choice, func(t *testing.T) {
			m := experienceStart(t)
			for i := 0; i < 4; i++ {
				m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
			}
			m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
			if choice == "off" {
				m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnd})
			m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
			m = experienceKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(filepath.Join(t.TempDir(), "target"))})
			m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
			state, ok := cli.ShellInstallPresentationOf(m.shellInstallModel)
			if !ok || state.Error != nil || !state.Review || state.Request.Confirmation == "" || state.Request.Experience.CodeGraph != choice {
				t.Fatalf("CodeGraph selection cannot reach exact pre-install Ready: %+v", state)
			}
			if m.ShellInstallSelection().Confirmed {
				t.Fatal("Ready installs without physical confirmation")
			}
		})
	}
}
