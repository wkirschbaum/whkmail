package tui

import (
	"fmt"
	"strings"
)

// helpEntry is one row in the help popup: a display key plus its
// human-readable action. Built from Model.style.Key(Action) so rebinding
// one action doesn't require touching the popup layout.
type helpEntry struct {
	key  string
	desc string
}

// helpSection is a labelled group of keybindings — global, or the
// commands that apply to one specific view.
type helpSection struct {
	title   string
	entries []helpEntry
	active  bool // flagged when this section matches the current view
}

// helpSections returns the full keybinding reference, organised into
// global + per-view groups. The active section for the current view is
// flagged so renderHelpBody can emphasise it.
func (m Model) helpSections() []helpSection {
	k := m.style.Key
	return []helpSection{
		{
			title: "Global",
			entries: []helpEntry{
				{k(ActHelp), "toggle this help"},
				{k(ActConfig), "config"},
				{k(ActRefresh), "refresh"},
				{k(ActBack), "back / cancel"},
				{k(ActQuit), "quit (also C-c)"},
			},
		},
		{
			title:  "Accounts",
			active: m.view == viewAccounts,
			entries: []helpEntry{
				{k(ActMove), "move up/down"},
				{k(ActOpen), "select account"},
			},
		},
		{
			title:  "Folders",
			active: m.view == viewFolders,
			entries: []helpEntry{
				{k(ActMove), "move up/down"},
				{k(ActOpen), "open folder"},
			},
		},
		{
			title:  "Messages (list)",
			active: m.view == viewMessages,
			entries: []helpEntry{
				{k(ActMove), "move up/down"},
				{k(ActTopBottom), "top / bottom"},
				{k(ActHalfPage), "half page up/down"},
				{k(ActOpen), "open message"},
				{k(ActMarkRead), "mark read"},
				{k(ActMarkUnread), "mark unread"},
				{k(ActTrash), "trash"},
			},
		},
		{
			title:  "Message (detail)",
			active: m.view == viewMessage,
			entries: []helpEntry{
				{k(ActScrollBody), "scroll body"},
				{k(ActHalfPage), "half page up/down"},
				{k(ActJumpMessage), "next / prev message"},
				{k(ActMarkUnread), "mark unread (back to list)"},
				{k(ActTrash), "trash"},
			},
		},
	}
}

// renderHelpBody draws the full keybinding reference. Sections are
// ordered global → per-view; the section matching the current view is
// emphasised so the user's eye lands on the right block. Called by
// helpModal.render — never directly by View.
func renderHelpBody(m Model) string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("whkmail — Help") + "\n")
	b.WriteString(styleDim.Render("Input style: "+string(m.style)) + "\n\n")
	sections := m.helpSections()
	for i, sec := range sections {
		title := sec.title
		if sec.active {
			title += "  (current view)"
			b.WriteString(styleHeader.Render(title) + "\n")
		} else {
			b.WriteString(styleUnread.Render(title) + "\n")
		}
		for _, e := range sec.entries {
			line := fmt.Sprintf("  %-12s  %s", e.key, e.desc)
			if sec.active {
				b.WriteString(line + "\n")
			} else {
				b.WriteString(styleDim.Render(line) + "\n")
			}
		}
		if i < len(sections)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n" + styleDim.Render("Press any key to close"))
	return b.String()
}

// renderStylePickerBody draws the input-style chooser with the given
// cursor highlighted. Pure function — the modal holds the cursor, not
// Model, so this helper doesn't need the full state.
func renderStylePickerBody(cursor int) string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Input style") + "\n")
	for i, st := range configStyles {
		line := string(st)
		if i == cursor {
			b.WriteString(styleSelected.Render("> "+line) + "\n")
		} else {
			b.WriteString(styleDim.Render("  "+line) + "\n")
		}
	}
	b.WriteString("\n" + styleDim.Render("enter: apply  esc: cancel"))
	return b.String()
}
