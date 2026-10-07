package screens

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestUserExperienceSemantics(t *testing.T) {
	p := shellinstaller.DefaultUserExperience()
	want := []UserExperienceAction{UserExperienceGentleman, UserExperienceNeutral, UserExperienceUnmanaged, UserExperienceBackgroundSubagents, UserExperienceContinue, UserExperienceCancel}
	labels := []string{"Gentleman", "Neutral", "Custom", "Background subagents", "Continue", "Cancel"}
	count := 0
	for _, entry := range UserExperienceEntries(p) {
		if !entry.Selectable {
			if entry.Cursor != -1 || entry.Action != UserExperienceNone {
				t.Fatalf("informational entry creates an action: %+v", entry)
			}
			continue
		}
		if count >= len(want) || entry.Cursor != count || entry.Label != labels[count] || entry.Action != want[count] || UserExperienceActionAtCursor(p, count) != want[count] {
			t.Fatalf("unexpected semantic choice: %+v", entry)
		}
		count++
	}
	if count != 6 || UserExperienceActionAtCursor(p, -1) != UserExperienceNone || UserExperienceActionAtCursor(p, 6) != UserExperienceNone {
		t.Fatal("cursor space must contain exactly six actions")
	}
	for _, persona := range []string{"gentleman", "neutral", "unmanaged"} {
		for _, background := range []string{"on", "off"} {
			p = shellinstaller.UserExperience{Persona: persona, BackgroundSubagents: background}
			current := 0
			for _, e := range UserExperienceEntries(p) {
				if e.Category == "Persona" && e.Current {
					current++
					if string(e.Action) != persona {
						t.Fatalf("wrong persona: %+v", e)
					}
				}
				if e.Cursor == 3 && (e.Current != (background == "on") || e.Value != background) {
					t.Fatalf("wrong toggle: %+v", e)
				}
			}
			if current != 1 {
				t.Fatal("exactly one persona must be current")
			}
			view := ansi.Strip(RenderUserExperienceConfig(p, 3, 80, 50))
			label := map[string]string{"gentleman": "Gentleman", "neutral": "Neutral", "unmanaged": "Custom"}[persona]
			if !strings.Contains(view, "(*) "+label) || !strings.Contains(view, "Background subagents: "+background) {
				t.Fatalf("current choices not rendered: %q", view)
			}
		}
	}
}

func TestUserExperienceWidePresentation(t *testing.T) {
	p := shellinstaller.UserExperience{Persona: "unmanaged", BackgroundSubagents: "off"}
	view := ansi.Strip(RenderUserExperienceConfig(p, 2, 120, 50))
	for _, text := range []string{"Configure Gentle-Shell experience", "Required foundation", "Persona", "Recommended", "Release channel", "Optional workflows", "Advanced", "Actions", "About: Custom", "Current persona: unmanaged", "Background subagents: off", "project", "Gentleman fallback", "not connected", "4.0.0", "j/k: navigate", "enter: select", "esc: back"} {
		if !strings.Contains(view, text) {
			t.Errorf("missing %q", text)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "About: Custom") && (!strings.Contains(line, "│") || strings.Index(line, "About:") < 59) {
			t.Fatal("detail must be right of chooser")
		}
	}
	for _, label := range []string{"Strict TDD", "global RDD", "CodeGraph", "Context7", "SDD/OpenSpec", "GGA", "Main preview"} {
		found := false
		for _, e := range UserExperienceEntries(p) {
			if e.Label == label {
				found = !e.Selectable && e.Value == "unavailable" && strings.Contains(e.Description, "not connected")
			}
		}
		if !found {
			t.Errorf("missing honest unavailable information: %s", label)
		}
	}
}

func TestUserExperienceViewport(t *testing.T) {
	p := shellinstaller.DefaultUserExperience()
	for _, size := range [][2]int{{120, 50}, {80, 50}, {80, 8}, {40, 8}, {10, 8}, {40, 4}, {40, 3}, {10, 3}, {10, 2}, {1, 3}, {1, 1}} {
		for cursor, label := range []string{"Gentleman", "Neutral", "Custom", "Background subagents", "Continue", "Cancel"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], label), func(t *testing.T) {
				view := RenderUserExperienceConfig(p, cursor, size[0], size[1])
				lines := strings.Split(view, "\n")
				if len(lines) > size[1] {
					t.Fatalf("height overflow: %q", view)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("width overflow: %q", line)
					}
				}
				plain := ansi.Strip(view)
				if !strings.Contains(plain, ansi.Truncate(label, size[0], "")) {
					t.Fatalf("focus lost: %q", plain)
				}
				if size[0] >= 40 && size[1] >= 3 && (!strings.Contains(plain, "Cancel") || !strings.Contains(plain, "esc: back")) {
					t.Fatalf("escape/actions lost: %q", plain)
				}
				if size[0] >= 40 && size[1] >= 8 && !strings.Contains(plain, "About:") {
					t.Fatalf("focused detail lost: %q", plain)
				}
			})
		}
	}
}
