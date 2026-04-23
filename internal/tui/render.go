package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/wkirschbaum/whkmail/internal/types"
)

var (
	styleSelected = lipgloss.NewStyle().Reverse(true)
	styleUnread   = lipgloss.NewStyle().Bold(true)
	styleDraft    = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("11"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleHeader   = lipgloss.NewStyle().Bold(true).Underline(true)
	styleMuted    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v\n\nPress r to retry or q to quit.", m.err)
	}

	switch m.view {
	case viewAccounts:
		return m.renderAccounts()
	case viewFolders:
		return m.renderFolders()
	case viewMessages:
		return m.renderMessages()
	case viewMessage:
		return m.renderMessage()
	}
	return ""
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
	b.WriteString("\n" + styleDim.Render("enter: open  r: refresh  q: quit"))
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
	for i, f := range m.folders {
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
	if len(m.folders) == 0 {
		b.WriteString(styleDim.Render("  No folders yet. Daemon may still be syncing.") + "\n")
	}
	b.WriteString("\n" + styleDim.Render("enter: open  r: refresh  q: quit"))
	return b.String()
}

func (m Model) renderMessages() string {
	var b strings.Builder
	if m.account != "" {
		b.WriteString(styleDim.Render(m.account+" / "+m.folder) + "\n")
	}

	title := styleHeader.Render("Messages")
	counter := ""
	if n := len(m.messages); n > 0 {
		counter = fmt.Sprintf("  %d/%d", m.cursor+1, n)
	}
	b.WriteString(title)
	if counter != "" {
		b.WriteString(styleDim.Render(counter))
	}
	if m.loading {
		b.WriteString(styleMuted.Render(" (refreshing…)"))
	}
	b.WriteString("\n")

	if len(m.messages) == 0 {
		b.WriteString(styleDim.Render("  No messages.") + "\n")
		b.WriteString(styleDim.Render("j/k: move  enter: open  r: refresh  esc: back  q: quit"))
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
	for i := top; i < end; i++ {
		msg := m.messages[i]
		row := formatMessageRow(msg, rowWidth-2)
		line := "  " + row
		if i == m.cursor {
			line = "> " + row
			b.WriteString(styleSelected.Render(padRight(line, rowWidth)))
		} else {
			b.WriteString(messageStyle(msg).Render(line))
		}
		b.WriteString("\n")
	}

	b.WriteString(m.footer("j/k: move  g/G: top/bottom  enter: open  d: trash  r: refresh  esc: back  q: quit"))
	return b.String()
}

// footer renders the confirmation prompt when one is pending, otherwise the
// supplied help text. Centralised so every view gets the same treatment.
func (m Model) footer(help string) string {
	if m.confirmPrompt != "" {
		return styleMuted.Render(m.confirmPrompt)
	}
	return styleDim.Render(help)
}

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

func (m Model) renderMessage() string {
	if m.message == nil {
		return styleMuted.Render("Loading…")
	}
	msg := m.message
	var b strings.Builder

	if m.account != "" {
		b.WriteString(styleDim.Render(m.account+" / "+m.folder) + "\n")
	}
	b.WriteString(styleHeader.Render(msg.Subject) + "\n")
	b.WriteString(styleDim.Render("From: "+msg.From) + "\n")
	if msg.To != "" {
		b.WriteString(styleDim.Render("To:   "+msg.To) + "\n")
	}
	b.WriteString(styleDim.Render("Date: "+msg.Date.Format("Mon, 02 Jan 2006 15:04")) + "\n")
	b.WriteString(styleDim.Render(strings.Repeat("─", 40)) + "\n\n")

	bodyWidth := m.width - 2
	if bodyWidth < 40 {
		bodyWidth = 80
	}
	switch {
	case msg.BodyText != "":
		body := strings.ReplaceAll(msg.BodyText, "\r\n", "\n")
		body = strings.ReplaceAll(body, "\r", "\n")
		b.WriteString(wrapBody(body, bodyWidth))
	case m.bodyErr[prefetchKey{account: m.account, folder: msg.Folder, uid: msg.UID}] != "":
		reason := m.bodyErr[prefetchKey{account: m.account, folder: msg.Folder, uid: msg.UID}]
		b.WriteString(styleMuted.Render("Failed to load body: " + reason))
		b.WriteString("\n" + styleDim.Render("Press r to retry."))
	default:
		b.WriteString(styleMuted.Render("Loading…"))
	}
	b.WriteString("\n\n" + m.footer("j/k: next/prev  d: trash  r: refresh  esc: back  q: quit"))
	return b.String()
}

func formatMessageRow(msg types.Message, width int) string {
	date := msg.Date.Format("Jan 02")
	flag := " "
	if msg.Unread {
		flag = "●"
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
