package screens

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestUserInstallDestinationPresentation(t *testing.T) {
	p := shellinstaller.UserExperience{Persona: "neutral", BackgroundSubagents: "off"}
	req := shellinstaller.UserInstallRequest{Destination: "/owned/new-target", Mode: "shared", SharedPrefix: "/prefix", SharedAgent: "/agent", Experience: &p}
	for field, label := range []string{"Target", "Mode", "Shared prefix", "Shared agent"} {
		view := ansi.Strip(RenderUserInstallDestination(req, field, nil, 120, 50))
		for _, want := range []string{"Choose installation destination", "> " + label, "About:", "│", req.Destination, "neutral", "off", "project overrides", "Esc"} {
			if !strings.Contains(view, want) {
				t.Fatalf("missing %q in destination view: %s", want, view)
			}
		}
	}
	if view := ansi.Strip(RenderUserInstallDestination(req, 0, errors.New("physical custody refused"), 120, 50)); !strings.Contains(view, "physical custody refused") {
		t.Fatal("inspection error lost")
	}
}

func TestUserInstallDestinationViewport(t *testing.T) {
	req := shellinstaller.UserInstallRequest{Destination: strings.Repeat("long界", 90), Mode: "shared"}
	for _, size := range [][2]int{{120, 50}, {80, 24}, {40, 10}, {10, 4}, {8, 3}} {
		for field := 0; field < 4; field++ {
			view := ansi.Strip(RenderUserInstallDestination(req, field, nil, size[0], size[1]))
			if len(strings.Split(view, "\n")) > size[1] || !strings.Contains(view, ">") || !strings.Contains(strings.ToLower(view), "esc") {
				t.Fatalf("focus/bounds/escape lost at %v: %q", size, view)
			}
			for _, row := range strings.Split(view, "\n") {
				if ansi.StringWidth(row) > size[0] {
					t.Fatalf("destination exceeds physical width: %q", row)
				}
			}
		}
	}
}
