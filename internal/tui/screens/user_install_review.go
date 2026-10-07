package screens

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui/styles"
)

var userInstallReviewActions = []string{"Install Gentle-Shell", "Back", "Cancel"}

// RenderUserInstallReview is an inert review of the caller's inspected request.
// The caller owns cursors (0 Install, 1 Back, 2 Cancel), paging and backend consent.
func RenderUserInstallReview(req shellinstaller.UserInstallRequest, preview string, cursor, offset, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	cursor = min(max(cursor, 0), 2)
	contentWidth, slots, wide := userInstallReviewLayout(width, height)
	data := userInstallReviewData(req, preview, contentWidth)
	last := max(0, len(data)-slots)
	offset = min(max(offset, 0), last)
	page := data[offset:min(len(data), offset+slots)]
	var lines []string
	if height >= 6 {
		lines = append(lines, userExperienceLine(styles.TitleStyle, "Ready to install Gentle-Shell", width))
	}
	if wide {
		leftWidth := width - contentWidth - 1
		left := []string{userExperienceLine(styles.HeadingStyle, "Plan", leftWidth)}
		for i, action := range userInstallReviewActions {
			style, prefix := styles.UnselectedStyle, " "
			if i == cursor {
				style, prefix = styles.SelectedStyle, ">"
			}
			left = append(left, userExperienceLine(style, prefix+action, leftWidth))
		}
		left = append(left, "Install: confirm handoff", "Back: edit destination", "Cancel: do not install")
		summary := []string{"Target: " + req.Destination, "Mode: " + req.Mode}
		if req.Experience != nil {
			summary = append(summary, "Persona: "+req.Experience.Persona, "Background subagents: "+req.Experience.BackgroundSubagents)
		}
		// Summary-only clipping; the complete request remains pageable on the right.
		for _, text := range summary[:min(len(summary), slots+1-len(left))] {
			left = append(left, userExperienceLine(styles.SubtextStyle, text, leftWidth))
		}
		right := append([]string{userExperienceLine(styles.HeadingStyle, "About: "+userInstallReviewActions[cursor], contentWidth)}, page...)
		for i := 0; i < slots+1; i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			lines = append(lines, l+strings.Repeat(" ", max(0, leftWidth-ansi.StringWidth(l)))+styles.SubtextStyle.Render("│")+r)
		}
	} else {
		action := userExperienceFit(width, ">"+userInstallReviewActions[cursor], ">"+[]string{"Install", "Back", "Cancel"}[cursor], ">"+[]string{"I", "B", "C"}[cursor])
		lines = append(lines, userExperienceLine(styles.SelectedStyle, action, width))
		lines = append(lines, page...)
	}
	footer := userExperienceFit(width,
		fmt.Sprintf("[%d/%d] j/k: action • enter: select • pgup/pgdn: review • esc: cancel", offset, last),
		fmt.Sprintf("[%d/%d] pgup/pgdn: review • esc: cancel", offset, last),
		"pgup/pgdn • esc: cancel", "pg esc", "esc", "←")
	lines = append(lines, userExperienceLine(styles.HelpStyle, footer, width))
	return userExperienceJoin(lines, width, height)
}

// UserInstallReviewLastOffset shares the renderer's exact wrapping and viewport.
// Every review line, including the entire supplied preview, remains pageable.
func UserInstallReviewLastOffset(req shellinstaller.UserInstallRequest, preview string, width, height int) int {
	if width <= 0 || height <= 0 {
		return 0
	}
	contentWidth, slots, _ := userInstallReviewLayout(width, height)
	return max(0, len(userInstallReviewData(req, preview, contentWidth))-slots)
}

func userInstallReviewLayout(width, height int) (contentWidth, slots int, wide bool) {
	wide = width >= 100 && height >= 10
	contentWidth, slots = width, max(1, height-2)
	if height >= 6 {
		slots = height - 3
	}
	if wide {
		contentWidth = width - width/3 - 1
	}
	return
}

func userInstallReviewData(req shellinstaller.UserInstallRequest, preview string, width int) []string {
	logical := []string{
		"Review action only; not installed health or qualification proof.",
		"Install Gentle-Shell: confirm backend handoff. Back: edit destination. Cancel: leave without installing.",
		"Target: " + req.Destination,
		"Mode: " + req.Mode,
		"Commands: " + req.Destination + "/bin/gentle-shell",
		req.Destination + "/bin/pi",
		"Physical confirmation SHA: " + req.Confirmation,
		"Frozen Supplier: 4.0.0; Native: 4.0.0.",
		"SRI / age3 provenance is informational, not a new health claim.",
	}
	if req.Mode == "shared" {
		logical = append(logical, "Shared prefix: "+req.SharedPrefix, "Shared agent: "+req.SharedAgent)
	}
	if req.Experience == nil {
		logical = append(logical, "Experience: existing defaults unchanged.")
	} else {
		logical = append(logical,
			"New-target-only choices, from the validated experience contract.",
			"Persona: "+req.Experience.Persona,
			"Background subagents: "+req.Experience.BackgroundSubagents,
			"Scope: target/config global default, not your personal Pi root. Retained project overrides may override these choices.",
		)
		if req.Experience.Persona == "unmanaged" {
			logical = append(logical, "Unmanaged: preserve persona files; Gentleman fallback if absent.")
		}
	}
	logical = append(logical,
		"Existing manager, kernel, custody and authentic acquisition are still revalidated after terminal restored.",
		"No installation or preference writes before confirmed backend handoff.",
		"Inspection preview:", preview,
	)
	// Preserve spaces, blank lines, ANSI and long unbroken paths/SHA values.
	// Only chrome is clipped; no review data is permanently shortened.
	return strings.Split(ansi.Hardwrap(strings.Join(logical, "\n"), width, true), "\n")
}
