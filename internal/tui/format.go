package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// messageStyle returns the lipgloss style for a non-selected message row.
// Unread is bold, drafts are italic yellow, everything else is dim.
func messageStyle(msg types.Message) lipgloss.Style {
	switch {
	case msg.Unread:
		return styleUnread
	case msg.Draft:
		return styleDraft
	default:
		return styleDim
	}
}

// threadIndent returns the visual prefix for a message row based on its
// depth. Depth 0 (root) gets no prefix; replies get "  " per ancestor
// level plus "↳ ".
func threadIndent(depths []int, i int) string {
	if i >= len(depths) || depths[i] == 0 {
		return ""
	}
	d := depths[i]
	return strings.Repeat("  ", d-1) + "↳ "
}

func formatMessageRow(msg types.Message, width int) string {
	date := msg.Date.Format("Jan 02")
	// Priority: unread dot wins over the answered arrow — users care
	// most about what they haven't read yet. An already-read reply
	// still shows the ↩ so they can tell at a glance which threads
	// they've already weighed in on.
	flag := " "
	switch {
	case msg.Unread:
		flag = "●"
	case msg.Answered:
		flag = "↩"
	}
	fromWidth := 24
	dateWidth := 6
	// flag + space + from + 2 + subject + 2 + date
	subjectWidth := width - 1 - 1 - fromWidth - 2 - 2 - dateWidth
	if subjectWidth < 10 {
		subjectWidth = 40
	}
	from := truncate(msg.From, fromWidth)
	subject := truncate(msg.Subject, subjectWidth)
	return fmt.Sprintf("%s %-*s  %-*s  %s", flag, fromWidth, from, subjectWidth, subject, date)
}

// padRight pads s with spaces so the visible width reaches width runes.
// Used so the selected-row background extends to the right edge.
func padRight(s string, width int) string {
	diff := width - len([]rune(s))
	if diff <= 0 {
		return s
	}
	return s + strings.Repeat(" ", diff)
}

// truncate shortens s to at most max runes, appending "…" if cut.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// wrapBody wraps long lines in a plain-text body at word boundaries.
// Existing line breaks are preserved.
func wrapBody(text string, width int) string {
	if width <= 10 {
		return text
	}
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		out = append(out, wrapLine(line, width))
	}
	return strings.Join(out, "\n")
}

func wrapLine(line string, width int) string {
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	var parts []string
	for len(runes) > width {
		cut := width
		for cut > 0 && runes[cut-1] != ' ' {
			cut--
		}
		if cut == 0 {
			cut = width
		}
		parts = append(parts, string(runes[:cut]))
		runes = runes[cut:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	if len(runes) > 0 {
		parts = append(parts, string(runes))
	}
	return strings.Join(parts, "\n")
}
