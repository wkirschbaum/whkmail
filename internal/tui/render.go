package tui

import (
	"fmt"
	"strings"
)

// View is bubbletea's single rendering entry point. It routes around the
// three modal overlays (error page, help popup, config popup) before
// delegating to the per-view body renderer, then wraps the body in
// frame() so the status bar lands on the last row.
func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v\n\nPress any key to retry, or %s to quit.",
			m.err, m.style.Key(ActQuit))
	}
	// Overlay modals (help, style picker) replace the whole view so the
	// user's eye is undistracted. Inline modals (confirm) flow through
	// frame() and only take over the status bar.
	if m.modal != nil && m.modal.overlay() {
		return m.modal.render(m)
	}

	var body string
	switch m.view {
	case viewAccounts:
		body = m.renderAccounts()
	case viewFolders:
		body = m.renderFolders()
	case viewMessages:
		body = m.renderMessages()
	case viewMessage:
		body = m.renderMessage()
	}
	return m.frame(body)
}

// frame composes the view with the status bar pinned to the last row of
// the terminal. Content is padded with blank lines so the chrome always
// lands on the last row regardless of body length. An inline modal
// (confirm prompt) replaces the status bar outright — the prompt needs
// the full width and its own colour treatment.
func (m Model) frame(body string) string {
	bottom := m.renderStatusBar()
	if m.modal != nil && !m.modal.overlay() {
		bottom = m.modal.render(m)
	}

	trimmed := strings.TrimRight(body, "\n")
	bodyLines := 0
	if trimmed != "" {
		bodyLines = strings.Count(trimmed, "\n") + 1
	}

	var pad string
	if m.height > 0 {
		avail := m.height - 1 // one row reserved for the status bar
		if p := avail - bodyLines; p > 0 {
			pad = strings.Repeat("\n", p)
		}
	}

	var sb strings.Builder
	sb.WriteString(trimmed)
	if pad != "" {
		sb.WriteString(pad)
	}
	sb.WriteString("\n")
	sb.WriteString(bottom)
	return sb.String()
}

func (m Model) renderAccounts() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Accounts"))
	if m.loading {
		b.WriteString(styleMuted.Render(" (refreshing…)"))
	}
	b.WriteString("\n\n")
	for i, ac := range m.accounts {
		name := fmt.Sprintf("%-40s", ac.Account)
		if i == m.cursor {
			b.WriteString(styleSelected.Render("> " + name))
		} else {
			b.WriteString(styleDim.Render("  " + name))
		}
		if ac.Syncing {
			b.WriteString(" " + styleMuted.Render("(syncing)"))
		}
		b.WriteString("\n")
	}
	if len(m.accounts) == 0 {
		b.WriteString(styleDim.Render("  No accounts yet.") + "\n")
	}
	return b.String()
}

func (m Model) renderFolders() string {
	var b strings.Builder
	if m.account != "" {
		b.WriteString(styleDim.Render(m.account) + "\n")
	}
	b.WriteString(styleHeader.Render("Folders"))
	if m.loading {
		b.WriteString(styleMuted.Render(" (refreshing…)"))
	}
	b.WriteString("\n\n")
	visible := m.visibleFolders()
	for i, f := range visible {
		line := fmt.Sprintf("%-30s %d", f.Name, f.Unread)
		switch {
		case i == m.cursor:
			b.WriteString(styleSelected.Render("> "+line) + "\n")
		case f.Unread > 0:
			b.WriteString(styleUnread.Render("  "+line) + "\n")
		default:
			b.WriteString(styleDim.Render("  "+line) + "\n")
		}
	}
	if len(visible) == 0 {
		b.WriteString(styleDim.Render("  No folders yet. Daemon may still be syncing.") + "\n")
	}
	return b.String()
}

// folderBadgeCols is the fixed column width of the folder badge shown in the
// Combined tab. A space separator follows, so the total overhead per row is
// folderBadgeCols+1 characters.
const folderBadgeCols = 9

func (m Model) renderMessages() string {
	var b strings.Builder
	b.WriteString(m.renderTabBar())

	if len(m.messages) == 0 {
		if !m.loading {
			b.WriteString(styleDim.Render("  No messages.") + "\n")
		}
		return b.String()
	}

	visible := m.visibleRows()
	top := m.msgTop
	end := top + visible
	if end > len(m.messages) {
		end = len(m.messages)
	}
	rowWidth := m.width
	if rowWidth <= 0 {
		rowWidth = 80
	}

	combined := m.activeTab == 0
	for i := top; i < end; i++ {
		msg := m.messages[i]
		prefix := threadIndent(m.msgDepths, i)
		badge := ""
		if combined {
			label := truncate(msg.Folder, folderBadgeCols)
			badge = label + strings.Repeat(" ", folderBadgeCols-len([]rune(label))) + " "
		}
		avail := rowWidth - 2 - len([]rune(prefix)) - len([]rune(badge))
		row := formatMessageRow(msg, avail)
		if i == m.cursor {
			b.WriteString(styleSelected.Render(padRight("> "+badge+prefix+row, rowWidth)))
		} else {
			b.WriteString(messageStyle(msg).Render("  " + badge + prefix + row))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// tabName returns the display label for tab i.
// Tab 0 is always "Combined"; tabs 1..n are truncated folder names.
func (m Model) tabName(i int) string {
	if i == 0 {
		return "Combined"
	}
	return truncate(m.folders[i-1].Name, 10)
}

// renderTabBar renders the three-line classic tab bar:
//
//	      ┌──────────┐
//	  Foo │ Combined │  Bar   Baz
//	──────┘          └─────────────
func (m Model) renderTabBar() string {
	tabs := m.visibleTabIndices()

	activePos := 0
	for i, t := range tabs {
		if t == m.activeTab {
			activePos = i
			break
		}
	}

	activeLabel := m.tabName(m.activeTab)
	innerWidth := 1 + len([]rune(activeLabel)) + 1 // space + label + space

	// Visual width of inactive tabs to the left of the active tab.
	leftOffset := 0
	for i := 0; i < activePos; i++ {
		leftOffset += 2 + len([]rune(m.tabName(tabs[i]))) // "  label"
	}

	width := m.width
	if width <= 0 {
		width = 80
	}

	// Line 1: top border positioned over the active tab.
	line1 := strings.Repeat(" ", leftOffset) + "┌" + strings.Repeat("─", innerWidth) + "┐"

	// Line 2: inactive tabs (dim) interleaved with the active tab box.
	var b2 strings.Builder
	for _, t := range tabs {
		label := m.tabName(t)
		if t == m.activeTab {
			b2.WriteString("│ " + label + " │")
		} else {
			b2.WriteString(styleDim.Render("  " + label))
		}
	}
	if m.loading {
		b2.WriteString(styleMuted.Render(" (refreshing…)"))
	}

	// Line 3: full-width separator with gap beneath the active tab.
	activeBoxWidth := innerWidth + 2
	remaining := width - leftOffset - activeBoxWidth
	if remaining < 0 {
		remaining = 0
	}
	line3 := strings.Repeat("─", leftOffset) + "┘" + strings.Repeat(" ", innerWidth) + "└" + strings.Repeat("─", remaining)

	return line1 + "\n" + b2.String() + "\n" + line3 + "\n"
}

func (m Model) renderMessage() string {
	if m.message == nil {
		return styleMuted.Render("Loading…")
	}
	msg := m.message
	var b strings.Builder

	if m.account != "" {
		b.WriteString(styleDim.Render(m.account+" / "+msg.Folder) + "\n")
	}
	b.WriteString(styleHeader.Render(msg.Subject) + "\n")
	b.WriteString(styleDim.Render("From: "+msg.From) + "\n")
	if msg.To != "" {
		b.WriteString(styleDim.Render("To:   "+msg.To) + "\n")
	}
	b.WriteString(styleDim.Render("Date: "+msg.Date.Format("Mon, 02 Jan 2006 15:04")) + "\n")
	b.WriteString(styleDim.Render(strings.Repeat("─", 40)) + "\n\n")

	bodyErrKey := prefetchKey{account: m.account, folder: msg.Folder, uid: msg.UID}
	switch {
	case msg.BodyText != "":
		lines := m.bodyLines()
		visible := m.visibleBodyRows()
		start := m.bodyTop
		end := start + visible
		if end > len(lines) {
			end = len(lines)
		}
		if start < len(lines) {
			b.WriteString(strings.Join(lines[start:end], "\n"))
		}
	case m.bodyErr[bodyErrKey] != "":
		b.WriteString(styleMuted.Render("Failed to load body: " + m.bodyErr[bodyErrKey]))
	case msg.BodyFetched:
		b.WriteString(styleDim.Render("(no text content)"))
	default:
		b.WriteString(styleMuted.Render("Loading…"))
	}

	if m.compose != nil {
		b.WriteString("\n\n")
		b.WriteString(m.renderCompose())
	}
	return b.String()
}
