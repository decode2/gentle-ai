package cli

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

// ShellInstallArguments carries the entire consent-bound selection as argv,
// never shell syntax. Legacy selections retain their original flags.
func ShellInstallArguments(req shellinstaller.UserInstallRequest) ([]string, error) {
	args := []string{"install", "--target", req.Destination, "--mode", req.Mode,
		"--prefix", req.SharedPrefix, "--agent", req.SharedAgent, "--confirm", req.Confirmation}
	if req.Experience != nil {
		raw, err := req.Experience.Encode()
		if err != nil {
			return nil, err
		}
		args = append(args, "--experience", raw)
	}
	return args, nil
}

// NewShellInstallModelWithExperience selects target-local defaults without
// applying preferences or starting a program. The caller's value is copied.
func NewShellInstallModelWithExperience(cancel func(), p shellinstaller.UserExperience) tea.Model {
	m := NewShellInstallModel(cancel).(shellInstallModel)
	m.req.Experience = &p
	m.err = p.Validate()
	return m
}

// ShellInstallPresentation is an inert copy for the containing TUI's renderer.
type ShellInstallPresentation struct {
	Request shellinstaller.UserInstallRequest
	Field   int
	Review  bool
	Preview string
	Error   error
}

func ShellInstallPresentationOf(model tea.Model) (ShellInstallPresentation, bool) {
	m, ok := model.(shellInstallModel)
	if !ok {
		return ShellInstallPresentation{}, false
	}
	req := m.req
	if req.Experience != nil {
		p := *req.Experience
		req.Experience = &p
	}
	return ShellInstallPresentation{req, m.field, m.review, m.preview, m.err}, true
}

func shellExperienceDisclosure(req shellinstaller.UserInstallRequest) string {
	if req.Experience == nil {
		return ""
	}
	return strings.Join([]string{
		"Persona: " + req.Experience.Persona,
		"Background subagents: " + req.Experience.BackgroundSubagents,
		"Preferences: new target/config only, not your personal Pi or shared agent.",
		"Existing project overrides are kept and may override these defaults.",
		"Unmanaged persona leaves files untouched; absent overrides fall back to Gentleman.",
	}, "\n")
}
