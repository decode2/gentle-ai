package cli

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestShellExperienceArgumentRoundTrip(t *testing.T) {
	for _, persona := range []string{"gentleman", "neutral", "unmanaged"} {
		for _, policy := range []string{"on", "off"} {
			t.Run(persona+"/"+policy, func(t *testing.T) {
				p := shellinstaller.UserExperience{Persona: persona, BackgroundSubagents: policy}
				want := shellinstaller.UserInstallRequest{Destination: "/owned/path with spaces", Mode: "shared", SharedPrefix: "/prefix", SharedAgent: "/agent", Confirmation: "physical", Experience: &p}
				args, err := ShellInstallArguments(want)
				if err != nil {
					t.Fatal(err)
				}
				got, inspect, err := parseShellInstall(args[1:], io.Discard)
				if err != nil || inspect || !reflect.DeepEqual(got, want) {
					t.Fatalf("selection lost between parent and CLI: %+v, %v", got, err)
				}
				values, err := shellinstaller.UserInstallEntryValues(got)
				if err != nil || len(values) != 6 {
					t.Fatal("experience did not survive supervisor serialization", err)
				}
				entry, err := shellinstaller.UserInstallFromEntry(values)
				if err != nil || !reflect.DeepEqual(entry, want) {
					t.Fatal("experience changed across entry", err)
				}
			})
		}
	}
	bad := shellinstaller.UserExperience{Persona: "custom", BackgroundSubagents: "on"}
	if _, err := ShellInstallArguments(shellinstaller.UserInstallRequest{Experience: &bad}); err == nil {
		t.Fatal("invalid supplier mode admitted by parent handoff")
	}
	for _, raw := range []string{"", "{}", `{"persona":"neutral","background_subagents":"off","extra":true}`} {
		if _, _, err := parseShellInstall([]string{"--experience", raw}, io.Discard); err == nil {
			t.Fatalf("malformed explicit experience accepted: %q", raw)
		}
	}
}

func TestShellExperienceModelConsentAndPresentation(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	p := shellinstaller.UserExperience{Persona: "neutral", BackgroundSubagents: "off"}
	m := NewShellInstallModelWithExperience(nil, p)
	p.Persona = "gentleman" // caller mutation must not reconfigure the model
	for _, msg := range []tea.Msg{tea.WindowSizeMsg{Width: 120, Height: 50}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(filepath.Join(parent, "shell"))}, tea.KeyMsg{Type: tea.KeyEnter}} {
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		if cmd != nil {
			t.Fatal("inspection scheduled installation")
		}
	}
	state, ok := ShellInstallPresentationOf(m)
	if !ok || !state.Review || state.Request.Experience.Persona != "neutral" || state.Request.Confirmation == "" {
		t.Fatalf("missing consent-bound presentation: %+v", state)
	}
	if view := m.View(); !strings.Contains(view, "neutral") || !strings.Contains(view, "Background subagents: off") || !strings.Contains(view, "project overrides") {
		t.Fatal("review did not disclose chosen preferences and precedence")
	}
	state.Request.Experience.Persona = "gentleman"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	req, confirmed, finished, err := ShellInstallOutcome(next)
	if cmd == nil || !confirmed || !finished || err != nil || req.Experience.Persona != "neutral" {
		t.Fatal("presentation copy mutated confirmed profile", err)
	}
	args, err := ShellInstallArguments(req)
	if err != nil || !strings.Contains(strings.Join(args, " "), `"persona":"neutral"`) {
		t.Fatal("confirmed experience lost at parent handoff", err)
	}
	if _, err := os.Lstat(req.Destination); !os.IsNotExist(err) {
		t.Fatal("selection wrote a target")
	}
}
