package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

type shellInstallState struct {
	enabled bool
	child   tea.Model
	request *shellinstaller.UserInstallRequest
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
	m.shellInstall.child = cli.NewShellInstallModel(nil)
	m.shellInstall.request = nil
	m.setScreen(ScreenShellInstall)
	return m.updateShellInstall(tea.WindowSizeMsg{Width: m.Width, Height: m.Height})
}

func (m Model) updateShellInstall(msg tea.Msg) (tea.Model, tea.Cmd) {
	child, cmd := m.shellInstall.child.Update(msg)
	m.shellInstall.child = child
	req, confirmed, finished, _ := cli.ShellInstallOutcome(child)
	if !finished {
		return m, cmd
	}
	if confirmed {
		m.shellInstall.request = &req
		return m, tea.Quit
	}
	// Consume the child's quit: cancellation returns to the same parent menu.
	m.shellInstall.child, m.shellInstall.request = nil, nil
	m.setScreen(ScreenWelcome)
	return m, nil
}
