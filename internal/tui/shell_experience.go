package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui/screens"
)

func (m Model) configureShellExperience(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		m.shellInstall.cursor = (m.shellInstall.cursor + 5) % 6
	case "down", "j", "tab":
		m.shellInstall.cursor = (m.shellInstall.cursor + 1) % 6
	case "esc", "ctrl+c":
		return m.cancelShellInstall()
	case "enter":
		action := screens.UserExperienceActionAtCursor(m.shellInstall.experience, m.shellInstall.cursor)
		switch action {
		case screens.UserExperienceGentleman, screens.UserExperienceNeutral, screens.UserExperienceUnmanaged:
			m.shellInstall.experience.Persona = string(action)
		case screens.UserExperienceBackgroundSubagents:
			if m.shellInstall.experience.BackgroundSubagents == "on" {
				m.shellInstall.experience.BackgroundSubagents = "off"
			} else {
				m.shellInstall.experience.BackgroundSubagents = "on"
			}
		case screens.UserExperienceContinue:
			m.shellInstall.configuring = false
			m.shellInstall.child = cli.NewShellInstallModelWithExperience(nil, m.shellInstall.experience)
			return m.updateShellInstall(tea.WindowSizeMsg{Width: m.Width, Height: m.Height})
		case screens.UserExperienceCancel:
			return m.cancelShellInstall()
		}
	}
	return m, nil
}

func (m Model) cancelShellInstall() (tea.Model, tea.Cmd) {
	m.shellInstall.child, m.shellInstall.request = nil, nil
	m.shellInstall.configuring = false
	m.setScreen(ScreenWelcome)
	return m, nil
}

func (m Model) shellInstallView() string {
	if m.shellInstall.configuring {
		return screens.RenderUserExperienceConfig(m.shellInstall.experience, m.shellInstall.cursor, m.Width, m.Height)
	}
	state, ok := cli.ShellInstallPresentationOf(m.shellInstall.child)
	if !ok {
		return ""
	}
	if state.Review {
		return screens.RenderUserInstallReview(state.Request, state.Preview,
			m.shellInstall.reviewCursor, m.shellInstall.reviewOffset, m.Width, m.Height)
	}
	return screens.RenderUserInstallDestination(state.Request, state.Field, state.Error, m.Width, m.Height)
}

// Review navigation changes only presentation; actions still use the existing
// inspected child's confirmation/cancellation protocol.
func (m *Model) routeShellReview(key tea.KeyMsg, state cli.ShellInstallPresentation) (tea.KeyMsg, bool) {
	last := screens.UserInstallReviewLastOffset(state.Request, state.Preview, m.Width, m.Height)
	switch key.String() {
	case "up", "k":
		m.shellInstall.reviewCursor = (m.shellInstall.reviewCursor + 2) % 3
	case "down", "j", "tab":
		m.shellInstall.reviewCursor = (m.shellInstall.reviewCursor + 1) % 3
	case "pgup":
		m.shellInstall.reviewOffset -= max(1, m.Height-3)
	case "pgdown":
		m.shellInstall.reviewOffset += max(1, m.Height-3)
	case "home":
		m.shellInstall.reviewOffset = 0
	case "end":
		m.shellInstall.reviewOffset = last
	case "enter":
		switch m.shellInstall.reviewCursor {
		case 0:
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, true
		case 1:
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, true
		case 2:
			return tea.KeyMsg{Type: tea.KeyEsc}, true
		}
	default:
		return key, true
	}
	m.shellInstall.reviewOffset = min(max(0, m.shellInstall.reviewOffset), last)
	return key, false
}
