package tui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui/screens"
)

func sdkSourceRow(m Model) Model {
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyHome})
	for i := 0; i < 8; i++ {
		m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	return m
}

func TestShellExperienceSDKSourceHandoff(t *testing.T) {
	m := sdkSourceRow(experienceStart(t))
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.shellExperience == nil || m.shellExperience.profile.SDKSource != "stable" {
		t.Fatal("SDK stock choice missing from Configure")
	}
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		if m.shellExperience.profile.SDKSource != "stable" || m.shellExperience.err == nil {
			t.Fatal("unqualified Main choice offered")
		}
		return
	}
	if m.shellExperience == nil || m.shellExperience.profile.SDKSource != "main-preview" || !strings.Contains(m.View(), "SDK Main + stock provider") {
		t.Fatal("Main composition not selected or disclosed")
	}
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	target := filepath.Join(t.TempDir(), "target")
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(target)})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	state, _ := cli.ShellInstallPresentationOf(m.shellInstallModel)
	if !state.Review || state.Error != nil || state.Request.Confirmation == "" || m.ShellInstallSelection().Confirmed {
		t.Fatalf("Main cannot reach unconfirmed Ready: %+v", state)
	}
	if !strings.Contains(m.View(), "SDK Main + stock provider") || !strings.Contains(m.View(), "not installed health") {
		t.Fatal("Ready hides composition or implies installed health")
	}
	confirmed := experienceKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}).ShellInstallSelection()
	args := cli.ShellInstallArguments(confirmed)
	if !confirmed.Confirmed || len(args) != 8 || args[6] != "--experience" {
		t.Fatal("Main consent changed positional worker ABI", args)
	}
	req, err := shellinstaller.UserInstallFromEntry(args[1:])
	if err != nil || req.Experience == nil || req.Experience.SDKSource != "main-preview" || req.Confirmation != state.Request.Confirmation {
		t.Fatal("Main selection lost before worker", err)
	}
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m = sdkSourceRow(m)
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // back to omitted stock
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	state, _ = cli.ShellInstallPresentationOf(m.shellInstallModel)
	if state.Review || state.Request.Confirmation != "" || state.Request.Experience.SDKSource != "" || state.Request.Destination != target {
		t.Fatal("SDK edit retains physical confirmation or loses destination")
	}
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		cancel := experienceKey(m, tea.KeyMsg{Type: key})
		if cancel.Screen != ScreenWelcome || len(cli.ShellInstallArguments(cancel.ShellInstallSelection())) != 0 {
			t.Fatal("cancelled SDK choice reaches worker")
		}
	}
}

func TestShellExperienceSDKSourcePreservesConflicts(t *testing.T) {
	m := experienceStart(t)
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // explicit Strict TDD on
	m = sdkSourceRow(m)
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // stock
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // Main refusal
	if m.shellExperience == nil || m.shellExperience.profile.StrictTDD != "on" || m.shellExperience.profile.SDKSource != "stable" || m.shellExperience.err == nil {
		t.Fatal("Main silently clears unsupported preferences")
	}
}

func TestShellExperienceSDKSourceEditingConflictAndClear(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return // Main is not an offered choice on these platforms.
	}
	m := sdkSourceRow(experienceStart(t))
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyHome})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.shellExperience == nil || m.shellExperience.err == nil || m.shellExperience.profile.SDKSource != "main-preview" || m.shellExperience.profile.StrictTDD != "on" {
		t.Fatal("editing Main combination silently changes selections or advances")
	}
	m = sdkSourceRow(m)
	m = experienceKey(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.shellExperience.profile.SDKSource != "" || m.shellExperience.profile.StrictTDD != "on" {
		t.Fatal("clearing SDK source changes unrelated preferences")
	}
}

func TestShellExperienceSDKSourceResponsiveDisclosure(t *testing.T) {
	profile := shellinstaller.DefaultUserExperience()
	profile.SDKSource = "main-preview"
	req := shellinstaller.UserInstallRequest{Destination: "/selected", Mode: "separate", Experience: &profile}
	for _, size := range [][2]int{{120, 30}, {80, 14}, {30, 7}, {5, 1}} {
		views := []string{screens.RenderShellExperience(profile, 8, nil, size[0], size[1]), screens.RenderShellReady(req, "", size[0], size[1], 0), screens.RenderShellReady(req, "", size[0], size[1], screens.ShellReadyLastOffset(req, "", size[0], size[1]))}
		for _, view := range views {
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatal("SDK disclosure exceeds viewport height")
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatal("SDK disclosure exceeds viewport width")
				}
			}
		}
	}
}
