package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui/screens"
)

type shellInstallState struct {
	enabled                            bool
	child                              tea.Model
	request                            *shellinstaller.UserInstallRequest
	configuring                        bool
	experience                         shellinstaller.UserExperience
	cursor, reviewCursor, reviewOffset int
}

// WithShellInstaller enables the original welcome action without changing
// ordinary menu contracts for other model callers.
func (m Model) WithShellInstaller() Model {
	m.shellInstall.enabled = true
	return m
}

// ShellInstallSelection returns only an explicitly confirmed child selection.
// Execution belongs to the caller after the parent program restores the terminal.
func (m Model) ShellInstallSelection() (shellinstaller.UserInstallRequest, bool) {
	if m.shellInstall.request == nil {
		return shellinstaller.UserInstallRequest{}, false
	}
	return *m.shellInstall.request, true
}

func (m Model) startShellInstall() (tea.Model, tea.Cmd) {
	m.shellInstall.child, m.shellInstall.request = nil, nil
	m.shellInstall.configuring = true
	m.shellInstall.experience = shellinstaller.DefaultUserExperience()
	m.shellInstall.cursor, m.shellInstall.reviewCursor, m.shellInstall.reviewOffset = 0, 0, 0
	m.setScreen(ScreenShellInstall)
	return m.updateShellInstall(tea.WindowSizeMsg{Width: m.Width, Height: m.Height})
}

func (m Model) updateShellInstall(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.shellInstall.configuring {
		return m.configureShellExperience(msg)
	}
	state, ok := cli.ShellInstallPresentationOf(m.shellInstall.child)
	if !ok {
		return m, nil
	}
	if key, isKey := msg.(tea.KeyMsg); isKey && state.Review {
		key, forward := m.routeShellReview(key, state)
		if !forward {
			return m, nil
		}
		msg = key
	}
	child, cmd := m.shellInstall.child.Update(msg)
	m.shellInstall.child = child
	after, _ := cli.ShellInstallPresentationOf(child)
	if state.Review != after.Review {
		m.shellInstall.reviewCursor, m.shellInstall.reviewOffset = 0, 0
	}
	if after.Review {
		last := screens.UserInstallReviewLastOffset(after.Request, after.Preview, m.Width, m.Height)
		m.shellInstall.reviewOffset = min(m.shellInstall.reviewOffset, last)
	}
	req, confirmed, finished, _ := cli.ShellInstallOutcome(child)
	if !finished {
		return m, cmd
	}
	if confirmed {
		m.shellInstall.request = &req
		return m, tea.Quit
	}
	// Consume the child's quit: cancellation returns to the same parent menu.
	return m.cancelShellInstall()
}
