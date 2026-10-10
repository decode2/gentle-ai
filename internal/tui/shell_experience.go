package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui/screens"
)

func (m *Model) beginShellExperience() {
	profile := shellinstaller.DefaultUserExperience()
	m.shellExperience = &shellExperienceConfig{profile: profile}
	m.shellInstallModel = cli.NewShellInstallModelWithExperience(nil, profile)
}

type shellExperienceConfig struct {
	profile shellinstaller.UserExperience
	cursor  int
	err     error
}

func (m Model) configureShellExperience(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.Type == tea.KeyEsc || key.Type == tea.KeyCtrlC {
		m.shellExperience, m.shellInstallModel = nil, nil
		m.shellInstallSelection = cli.ShellInstallSelection{}
		m.setScreen(ScreenWelcome)
		return m, nil
	}
	config := *m.shellExperience
	config.err = nil
	if config.cursor == 7 && key.Type == tea.KeyRunes {
		text := string(key.Runes)
		if len(config.profile.GGAProvider)+len(text) <= 64 && strings.IndexFunc(text, func(r rune) bool { return r < 32 || r > 126 }) < 0 {
			config.profile.GGAProvider += text
		}
	} else {
		switch key.String() {
		case "up", "k":
			config.cursor = (config.cursor + 8) % 9
		case "down", "j", "tab":
			config.cursor = (config.cursor + 1) % 9
		case "home":
			config.cursor = 0
		case "end":
			config.cursor = 8
		case "backspace":
			if config.cursor == 7 && len(config.profile.GGAProvider) > 0 {
				config.profile.GGAProvider = config.profile.GGAProvider[:len(config.profile.GGAProvider)-1]
			}
		case "enter":
			switch config.cursor {
			case 0:
				choices := []string{"gentleman", "neutral", "unmanaged"}
				for i, p := range choices {
					if config.profile.Persona == p {
						config.profile.Persona = choices[(i+1)%3]
						break
					}
				}
			case 1:
				if config.profile.BackgroundSubagents == "on" {
					config.profile.BackgroundSubagents = "off"
				} else {
					config.profile.BackgroundSubagents = "on"
				}
			case 7: // Provider is typed explicitly, never inferred from Pi.
			case 8:
				if config.err = config.profile.Validate(); config.err == nil {
					state, _ := cli.ShellInstallPresentationOf(m.shellInstallModel)
					state.Request.Experience = &config.profile
					m.shellInstallModel = cli.NewShellInstallModelWithRequest(nil, state.Request)
					m.shellInstallModel, _ = m.shellInstallModel.Update(tea.WindowSizeMsg{Width: m.Width, Height: m.Height})
					m.shellExperience, m.shellReadyOffset = nil, 0
					return m, nil
				}
			default:
				fields := []*string{&config.profile.StrictTDD, &config.profile.GlobalRDD, &config.profile.CodeGraph, &config.profile.Context7, &config.profile.GGA}
				field := fields[config.cursor-2]
				switch *field {
				case "":
					*field = "on"
				case "on":
					*field = "off"
				default:
					*field = ""
				}
				if config.profile.GGA != "on" {
					config.profile.GGAProvider = ""
				}
			}
		}
	}
	m.shellExperience = &config
	return m, nil
}

func (m Model) shellInstallView() string {
	if m.shellExperience != nil {
		return screens.RenderShellExperience(m.shellExperience.profile, m.shellExperience.cursor, m.shellExperience.err, m.Width, m.Height)
	}
	state, ok := cli.ShellInstallPresentationOf(m.shellInstallModel)
	if ok && state.Review {
		return screens.RenderShellReady(state.Request, state.Preview, m.Width, m.Height, m.shellReadyOffset)
	}
	return m.shellInstallModel.View()
}

func (m Model) shellExperienceNavigation(key tea.KeyMsg) (Model, bool) {
	state, ok := cli.ShellInstallPresentationOf(m.shellInstallModel)
	if !ok {
		return m, false
	}
	if key.Type == tea.KeyCtrlE {
		profile := shellinstaller.DefaultUserExperience()
		if state.Request.Experience != nil {
			profile = *state.Request.Experience
		}
		m.shellExperience = &shellExperienceConfig{profile: profile}
		return m, true
	}
	if !state.Review {
		return m, false
	}
	switch key.String() {
	case "pgup":
		m.shellReadyOffset -= max(1, m.Height-2)
	case "pgdown":
		m.shellReadyOffset += max(1, m.Height-2)
	case "home":
		m.shellReadyOffset = 0
	case "end":
		m.shellReadyOffset = screens.ShellReadyLastOffset(state.Request, state.Preview, m.Width, m.Height)
	default:
		return m, false
	}
	m.shellReadyOffset = min(max(0, m.shellReadyOffset), screens.ShellReadyLastOffset(state.Request, state.Preview, m.Width, m.Height))
	return m, true
}
