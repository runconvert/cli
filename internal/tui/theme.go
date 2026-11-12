package tui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// theme is RunConvert's look in the terminal: ink, paper, lime. It is
// rebuilt when the terminal reports its background (tea.BackgroundColorMsg),
// which Bubble Tea v2 asks for without blocking start-up.
type theme struct {
	ok, err, muted, accent, bold    lipgloss.Style
	box, input, chip, picked, plain lipgloss.Style
	choice                          lipgloss.Style
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	lime := ld(lipgloss.Color("#3A6D12"), lipgloss.Color("#BEF050"))
	ink := lipgloss.Color("#0E0F12")
	fg := ld(lipgloss.Color("#0E0F12"), lipgloss.Color("#E7E8E4"))
	muted := ld(lipgloss.Color("#616369"), lipgloss.Color("#A2A4AB"))
	line := ld(lipgloss.Color("#D3D2CE"), lipgloss.Color("#3A3D44"))
	soft := ld(lipgloss.Color("#ECEBE8"), lipgloss.Color("#2C2E34"))
	return theme{
		ok:     lipgloss.NewStyle().Foreground(ld(lipgloss.Color("#048149"), lipgloss.Color("#7BD9A6"))),
		err:    lipgloss.NewStyle().Foreground(ld(lipgloss.Color("#D42325"), lipgloss.Color("#FF8A80"))),
		muted:  lipgloss.NewStyle().Foreground(muted),
		accent: lipgloss.NewStyle().Foreground(lime).Bold(true),
		bold:   lipgloss.NewStyle().Bold(true),
		plain:  lipgloss.NewStyle().Foreground(fg),
		box:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(line).Padding(0, 1),
		input:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lime).Padding(0, 1),
		chip:   lipgloss.NewStyle().Background(ld(lipgloss.Color("#BEF050"), lipgloss.Color("#BEF050"))).Foreground(ink).Bold(true).Padding(0, 1),
		picked: lipgloss.NewStyle().Background(lipgloss.Color("#BEF050")).Foreground(ink).Bold(true),
		choice: lipgloss.NewStyle().Background(soft).Foreground(fg),
	}
}

// startDark guesses the background until the terminal answers:
// RUNCONVERT_THEME=light|dark, else COLORFGBG, else dark.
func startDark() bool {
	switch strings.ToLower(os.Getenv("RUNCONVERT_THEME")) {
	case "light":
		return false
	case "dark":
		return true
	}
	if v := os.Getenv("COLORFGBG"); v != "" {
		parts := strings.Split(v, ";")
		if b := parts[len(parts)-1]; b == "7" || b == "15" {
			return false
		}
	}
	return true
}

// forcedTheme reports whether RUNCONVERT_THEME fixes the colours.
func forcedTheme() bool {
	t := strings.ToLower(os.Getenv("RUNCONVERT_THEME"))
	return t == "light" || t == "dark"
}
