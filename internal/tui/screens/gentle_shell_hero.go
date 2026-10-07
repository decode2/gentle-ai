package screens

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui/styles"
)

const gentleShellInstallTitle = "Install Gentle-Shell, our own agent"

// Restore the original install hero, without advertising unobserved managed,
// repair or adoption states from the retired prototype.
func renderGentleShellInstallHero(selected bool, width int) string {
	frame := styles.PanelStyle.Border(lipgloss.RoundedBorder()).BorderForeground(styles.ColorMauve)
	innerWidth := 0
	if width > 0 {
		innerWidth = max(1, width-frame.GetHorizontalFrameSize())
		frame = frame.Width(innerWidth)
	}
	title, titleStyle := gentleShellInstallTitle, styles.TitleStyle
	if selected {
		title, titleStyle = styles.Cursor+title, styles.SelectedStyle
	}
	capabilities := []string{
		styles.TitleStyle.Render("MEMORY"),
		styles.HeadingStyle.Render("ODD"),
		lipgloss.NewStyle().Foreground(styles.ColorBlue).Bold(true).Render("SKILLS"),
		lipgloss.NewStyle().Foreground(styles.ColorTeal).Bold(true).Render("SAFE AUTOMATION"),
	}
	content := []string{
		renderWelcomeText(titleStyle, title, innerWidth),
		renderWelcomeText(styles.SubtextStyle, "The complete Pi experience.", innerWidth),
		"",
		strings.Join(capabilities, styles.SubtextStyle.Render(" • ")),
		"",
		renderWelcomeText(styles.UnselectedStyle, "A managed foundation for your Pi workflow.", innerWidth),
	}
	return frame.Render(strings.Join(content, "\n"))
}
