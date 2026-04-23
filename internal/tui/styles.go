package tui

import "github.com/charmbracelet/lipgloss"

// One place for every TUI style. When a renderer needs a new visual
// treatment, add it here so the palette stays coherent and nobody has to
// grep through render code to find which ANSI code is for what.
var (
	styleSelected  = lipgloss.NewStyle().Reverse(true)
	styleUnread    = lipgloss.NewStyle().Bold(true)
	styleDraft     = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("11"))
	styleDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleHeader    = lipgloss.NewStyle().Bold(true).Underline(true)
	styleMuted     = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleStatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("238"))
	styleFlash = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("11")).
			Background(lipgloss.Color("238"))
)
