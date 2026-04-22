package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/wkirschbaum/whkmail/internal/types"
)

var (
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleUnread   = lipgloss.NewStyle().Bold(true)
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleHeader   = lipgloss.NewStyle().Bold(true).Underline(true)
)

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v\n\nPress q to quit.", m.err)
	}

	switch m.view {
	case viewFolders:
		return m.renderFolders()
	case viewMessages:
		return m.renderMessages()
	case viewMessage:
		return m.renderMessage()
	}
	return ""
}

func (m Model) renderFolders() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Folders") + "\n\n")
	for i, f := range m.folders {
		line := fmt.Sprintf("%-30s %d", f.Name, f.Unread)
		if i == m.cursor {
			b.WriteString(styleSelected.Render("> "+line) + "\n")
		} else if f.Unread > 0 {
			b.WriteString(styleUnread.Render("  "+line) + "\n")
		} else {
			b.WriteString(styleDim.Render("  "+line) + "\n")
		}
	}
	if len(m.folders) == 0 {
		b.WriteString(styleDim.Render("  No folders yet. Daemon may still be syncing.") + "\n")
	}
	b.WriteString("\n" + styleDim.Render("enter: open  q: quit"))
	return b.String()
}

func (m Model) renderMessages() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(m.folder) + "\n\n")
	for i, msg := range m.messages {
		line := formatMessageRow(msg, m.width)
		if i == m.cursor {
			b.WriteString(styleSelected.Render("> "+line) + "\n")
		} else if msg.Unread {
			b.WriteString(styleUnread.Render("  "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	if len(m.messages) == 0 {
		b.WriteString(styleDim.Render("  No messages.") + "\n")
	}
	b.WriteString("\n" + styleDim.Render("enter: read  esc: back  q: quit"))
	return b.String()
}

func (m Model) renderMessage() string {
	if m.message == nil {
		return "Loading…"
	}
	msg := m.message
	var b strings.Builder
	b.WriteString(styleHeader.Render(msg.Subject) + "\n")
	b.WriteString(styleDim.Render("From: "+msg.From) + "\n")
	b.WriteString(styleDim.Render("Date: "+msg.Date.Format("Mon, 02 Jan 2006 15:04")) + "\n\n")
	if msg.BodyText != "" {
		b.WriteString(msg.BodyText)
	} else {
		b.WriteString(styleDim.Render("(no body)"))
	}
	b.WriteString("\n\n" + styleDim.Render("esc: back  q: quit"))
	return b.String()
}

func formatMessageRow(msg types.Message, width int) string {
	date := msg.Date.Format("Jan 02")
	from := truncate(msg.From, 24)
	subjectWidth := width - 24 - 8 - 4
	if subjectWidth < 10 {
		subjectWidth = 40
	}
	subject := truncate(msg.Subject, subjectWidth)
	return fmt.Sprintf("%-24s  %-*s  %s", from, subjectWidth, subject, date)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}
