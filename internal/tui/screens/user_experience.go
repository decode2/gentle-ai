package screens

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui/styles"
)

// UserExperienceAction describes intent only; this screen never applies it.
type UserExperienceAction string

const (
	UserExperienceNone                UserExperienceAction = ""
	UserExperienceGentleman           UserExperienceAction = "gentleman"
	UserExperienceNeutral             UserExperienceAction = "neutral"
	UserExperienceUnmanaged           UserExperienceAction = "unmanaged"
	UserExperienceBackgroundSubagents UserExperienceAction = "background-subagents"
	UserExperienceContinue            UserExperienceAction = "continue"
	UserExperienceCancel              UserExperienceAction = "cancel"
)

// UserExperienceEntry separates informational rows from semantic cursors.
type UserExperienceEntry struct {
	Category, Label, Description, Value string
	Current, Selectable                 bool
	Cursor                              int
	Action                              UserExperienceAction
}

const userExperienceScope = "Global default for this target only, not your personal Pi root. Existing project overrides are kept and may override these choices."

// UserExperienceEntries projects validated choices, not installed health.
func UserExperienceEntries(p shellinstaller.UserExperience) []UserExperienceEntry {
	var entries []UserExperienceEntry
	cursor := 0
	add := func(category, label, description, value string, action UserExperienceAction, current bool) {
		e := UserExperienceEntry{Category: category, Label: label, Description: description, Value: value, Action: action, Current: current, Cursor: -1}
		if action != UserExperienceNone {
			e.Selectable, e.Cursor = true, cursor
			cursor++
		}
		entries = append(entries, e)
	}
	add("Required foundation", "Package + Engram memory integration", "Required package memory integration; this view does not inspect live Engram tools, installation health or MCP binary availability.", "required", UserExperienceNone, false)
	add("Required foundation", "ODD + core skills", "Required ODD and core skills cannot be disabled here.", "required", UserExperienceNone, false)
	add("Required foundation", "Subagent / interaction support", "Package-provided interaction support is foundation information, not a readiness check.", "required", UserExperienceNone, false)
	for _, choice := range []struct {
		label, description string
		action             UserExperienceAction
	}{
		{"Gentleman", "Gentleman conversational persona. Supplier 4.0.0 reads mode gentleman in root/config/persona.json.", UserExperienceGentleman},
		{"Neutral", "Neutral conversational persona. Supplier 4.0.0 reads mode neutral in root/config/persona.json.", UserExperienceNeutral},
		{"Custom", "Unmanaged: leave existing project persona files unchanged. Gentleman fallback if none exist; not a supplier custom mode or custom JavaScript editor.", UserExperienceUnmanaged},
	} {
		add("Persona", choice.label, choice.description+" "+userExperienceScope, string(choice.action), choice.action, p.Persona == string(choice.action))
	}
	add("Recommended", "Background subagents", "Target default only: root/config/background-subagents.json uses gentle-pi.background-subagents/v1 with on/off policy. Existing project overrides are kept and may override this choice.", p.BackgroundSubagents, UserExperienceBackgroundSubagents, p.BackgroundSubagents == "on")
	unavailable := "Unavailable: not connected to the qualified backend for the pinned supplier. No runnable preference or provisioning interface is verified here."
	for _, label := range []string{"Strict TDD", "global RDD", "CodeGraph", "Context7"} {
		add("Recommended", label, unavailable, "unavailable", UserExperienceNone, false)
	}
	add("Release channel", "Stable", "Frozen supplier release 4.0.0 only; no selectable release channel.", "4.0.0", UserExperienceNone, false)
	add("Release channel", "Main preview", unavailable, "unavailable", UserExperienceNone, false)
	add("Advanced", "GGA", unavailable, "unavailable", UserExperienceNone, false)
	add("Actions", "Continue", "Continue to the next confirmation step; choosing this row does not apply configuration.", "", UserExperienceContinue, false)
	add("Actions", "Cancel", "Go back without applying configuration.", "", UserExperienceCancel, false)
	return entries
}

func UserExperienceActionAtCursor(p shellinstaller.UserExperience, cursor int) UserExperienceAction {
	for _, e := range UserExperienceEntries(p) {
		if e.Selectable && e.Cursor == cursor {
			return e.Action
		}
	}
	return UserExperienceNone
}

// RenderUserExperienceConfig is a pure, viewport-bounded configuration surface.
// Callers own validated state, input handling, consent and eventual persistence.
func RenderUserExperienceConfig(p shellinstaller.UserExperience, cursor, width, height int) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 50
	}
	cursor = min(max(cursor, 0), 5)
	entries := UserExperienceEntries(p)
	var focused UserExperienceEntry
	for _, e := range entries {
		if e.Selectable && e.Cursor == cursor {
			focused = e
		}
	}
	footer := userExperienceFit(width, "j/k: navigate • enter: select • esc: back", "j/k enter esc: back", "esc: back", "esc", "←")
	title := userExperienceLine(styles.TitleStyle, "Configure Gentle-Shell experience", width)
	leftWidth := (width - 1) / 2
	rows, focusRow := userExperienceRows(entries, cursor, width)
	if width >= 100 && height >= len(rows)+2 {
		left, _ := userExperienceRows(entries, cursor, leftWidth)
		rightWidth := width - leftWidth - 1
		contentWidth := rightWidth - styles.PanelStyle.GetHorizontalFrameSize()
		about := userExperienceAbout(p, focused, contentWidth)
		right := strings.Split(styles.PanelStyle.Width(contentWidth).Render(strings.Join(about, "\n")), "\n")
		lines := []string{title}
		for i := 0; i < max(len(left), len(right)); i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			lines = append(lines, l+strings.Repeat(" ", max(0, leftWidth-ansi.StringWidth(l)))+styles.SubtextStyle.Render("│")+r)
		}
		lines = append(lines, userExperienceLine(styles.HelpStyle, footer, width))
		return userExperienceJoin(lines, width, height)
	}
	// Small viewports spend space on focus and escape before optional chrome.
	var lines []string
	if height >= 5 {
		lines = append(lines, title)
	}
	if height >= 12 {
		lines = append(lines, userExperienceLine(styles.SubtextStyle, "Unavailable controls: not connected to the qualified backend.", width))
	}
	reserved := 0
	if height >= 2 {
		reserved++ // footer
	}
	if height >= 3 {
		reserved++ // actions
	}
	detailSlots := 0
	if height >= 6 {
		detailSlots = min(6, height/3)
	}
	listSlots := max(1, height-len(lines)-reserved-detailSlots)
	if width < 24 || height <= 4 {
		lines = append(lines, userExperienceLine(styles.SelectedStyle, focused.Label, width))
	} else {
		listSlots = min(listSlots, len(rows))
		start := min(max(0, focusRow-listSlots/2), len(rows)-listSlots)
		lines = append(lines, rows[start:start+listSlots]...)
	}
	if detailSlots > 0 {
		// Prioritize the focused description over the global summary in compact view.
		about := append([]string{"About: " + focused.Label}, wrapPlainLine(focused.Description, width)...)
		for _, line := range about[:min(detailSlots, len(about))] {
			lines = append(lines, userExperienceLine(styles.SubtextStyle, line, width))
		}
	}
	if height >= 3 {
		actions := userExperienceFit(width, "Continue / Cancel", "C / Cancel", "C/X", "X")
		lines = append(lines, userExperienceLine(styles.UnselectedStyle, actions, width))
	}
	if height >= 2 {
		lines = append(lines, userExperienceLine(styles.HelpStyle, footer, width))
	}
	return userExperienceJoin(lines, width, height)
}

func userExperienceRows(entries []UserExperienceEntry, cursor, width int) ([]string, int) {
	var rows []string
	focusRow := 0
	category := ""
	for _, e := range entries {
		if category != e.Category {
			category = e.Category
			rows = append(rows, userExperienceLine(styles.HeadingStyle, category, width))
		}
		prefix, marker, style := "  ", "", styles.UnselectedStyle
		if !e.Selectable {
			style = styles.SubtextStyle
		}
		if e.Category == "Persona" {
			marker = "( ) "
			if e.Current {
				marker = "(*) "
			}
		}
		text := e.Label
		if e.Value == "unavailable" || e.Label == "Stable" {
			text += " — " + e.Value
		}
		if e.Action == UserExperienceBackgroundSubagents {
			text += ": " + e.Value
		}
		if e.Selectable && e.Cursor == cursor {
			prefix, style, focusRow = styles.Cursor, styles.SelectedStyle, len(rows)
		}
		rows = append(rows, userExperienceLine(style, prefix+marker+text, width))
	}
	return rows, focusRow
}

func userExperienceAbout(p shellinstaller.UserExperience, e UserExperienceEntry, width int) []string {
	lines := []string{"About: " + e.Label, "Current persona: " + p.Persona, "Background subagents: " + p.BackgroundSubagents, "", "Unavailable controls:", "not connected to the qualified backend.", ""}
	lines = append(lines, wrapPlainLine(e.Description, width)...)
	if e.Category == "Persona" {
		return lines
	}
	lines = append(lines, "")
	return append(lines, wrapPlainLine(userExperienceScope, width)...)
}

func userExperienceFit(width int, candidates ...string) string {
	for _, text := range candidates {
		if ansi.StringWidth(text) <= width {
			return text
		}
	}
	return ""
}

func userExperienceLine(style lipgloss.Style, text string, width int) string {
	return style.Render(ansi.Truncate(text, width, ""))
}

func userExperienceJoin(lines []string, width, height int) string {
	lines = lines[:min(len(lines), height)]
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}
