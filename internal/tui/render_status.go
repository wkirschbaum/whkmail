package tui

import (
	"fmt"
	"strings"
)

// renderStatusBar paints the mode-line at the bottom of the screen. The
// left side carries the live status (sync spinner + progress, unread
// count, debounced flash); the right side carries the help hint. The
// inverted background supplies the "border" contrast — no explicit line
// is drawn. Always visible, even with zero unread, so the eye has a
// fixed anchor.
func (m Model) renderStatusBar() string {
	width := m.width
	if width < 1 {
		width = 40
	}

	left := m.statusLeftSegments()
	leftBase := " " + strings.Join(left, "  ·  ") + " "
	rightHint := fmt.Sprintf(" %s: help ", m.style.Key(ActHelp))

	// Reserve room for the right hint before rendering the left side so
	// truncation eats the subject, not the count or help key.
	leftBudget := width - len([]rune(rightHint))
	if leftBudget < 1 {
		// Terminal too narrow for the hint — drop it, show only the left.
		return styleStatusBar.Render(padRight(truncate(leftBase, width), width))
	}

	// When a flash is present it renders in a distinct style so new mail
	// pops out of the mode line.
	var (
		leftRendered string
		leftVisLen   int
	)
	if m.flash.text == "" {
		shown := truncate(leftBase, leftBudget)
		leftRendered = styleStatusBar.Render(shown)
		leftVisLen = len([]rune(shown))
	} else {
		flashText := "  ·  " + m.flash.text + " "
		remain := leftBudget - len([]rune(leftBase))
		if remain > 4 {
			flashText = truncate(flashText, remain)
		} else {
			flashText = ""
		}
		leftRendered = styleStatusBar.Render(leftBase) + styleFlash.Render(flashText)
		leftVisLen = len([]rune(leftBase)) + len([]rune(flashText))
	}

	gap := width - leftVisLen - len([]rune(rightHint))
	if gap < 0 {
		gap = 0
	}
	return leftRendered + styleStatusBar.Render(strings.Repeat(" ", gap)) + styleStatusBar.Render(rightHint)
}

// statusLeftSegments returns the "sync / unread / flash-prefix" parts
// that make up the left half of the status bar. Separated from
// renderStatusBar so tests can assert the content without walking the
// styled output.
func (m Model) statusLeftSegments() []string {
	segments := []string{fmt.Sprintf("unread: %d", m.totalUnread())}
	if m.sync.active {
		spin := "⟳ syncing…"
		if m.sync.folder != "" {
			if m.sync.total > 0 {
				spin = fmt.Sprintf("⟳ %s (%d/%d)", m.sync.folder, m.sync.current, m.sync.total)
			} else {
				spin = fmt.Sprintf("⟳ %s", m.sync.folder)
			}
		}
		segments = append([]string{spin}, segments...)
	}
	return segments
}

// totalUnread sums the per-folder unread counts across every account the
// TUI knows about. Multi-account users see the global total so the bar
// still tells them "something needs attention" when they're not on the
// account that got new mail.
func (m Model) totalUnread() uint32 {
	var total uint32
	for _, ac := range m.accounts {
		for _, f := range ac.Folders {
			total += f.Unread
		}
	}
	return total
}
