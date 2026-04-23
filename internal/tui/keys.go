package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleKey dispatches a single key event. The confirmation-state guard at
// the top intentionally swallows all unrelated keys so a stray keystroke
// can't act on a half-confirmed destructive operation.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmPrompt != "" {
		switch msg.String() {
		case "y", "Y":
			action := m.onConfirm
			m.confirmPrompt = ""
			m.onConfirm = nil
			if action != nil {
				return action(m)
			}
			return m, nil
		case "n", "N", "esc", "ctrl+c":
			m.confirmPrompt = ""
			m.onConfirm = nil
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "r", "R":
		switch m.view {
		case viewAccounts, viewFolders:
			m.loading = true
			return m, fetchStatus(m.client)
		case viewMessages:
			m.loading = true
			return m, fetchMessages(m.client, m.account, m.folder)
		case viewMessage:
			if m.message != nil {
				m.loading = true
				// Retry clears any previous body-fetch failure so the
				// placeholder goes back to "Loading…" while we try again.
				delete(m.bodyErr, prefetchKey{account: m.account, folder: m.folder, uid: m.message.UID})
				return m, fetchMessage(m.client, m.account, m.folder, m.message.UID)
			}
		}

	case "j", "down", " ":
		return m.moveCursor(+1)

	case "k", "up":
		return m.moveCursor(-1)

	case "ctrl+d", "pgdown":
		return m.scrollBy(+m.visibleRows() / 2)

	case "ctrl+u", "pgup":
		return m.scrollBy(-m.visibleRows() / 2)

	case "g":
		if m.view == viewMessages && len(m.messages) > 0 {
			m.cursor = 0
			m.msgTop = 0
		}

	case "G":
		if m.view == viewMessages && len(m.messages) > 0 {
			m.cursor = len(m.messages) - 1
			m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
		}

	case "enter":
		switch m.view {
		case viewAccounts:
			if len(m.accounts) == 0 {
				break
			}
			m.account = m.accounts[m.cursor].Account
			m.folders = m.accounts[m.cursor].Folders
			m.cursor = 0
			m.view = viewFolders
			// New account → old folder state is meaningless; drop it.
			m.folder = ""
			m.messages = nil
		case viewFolders:
			if len(m.folders) == 0 {
				break
			}
			m.folder = m.folders[m.cursor].Name
			m.view = viewMessages
			m.cursor = 0
			m.msgTop = 0
			m.messages = nil
			m.loading = true
			return m, fetchMessages(m.client, m.account, m.folder)
		case viewMessages:
			if len(m.messages) == 0 {
				break
			}
			return m.openMessage(m.messages[m.cursor].UID)
		case viewMessage:
		}

	case "d":
		uid, ok := m.currentMessageUID()
		if !ok {
			break
		}
		if isTrashFolder(m.folder) {
			folder, account := m.folder, m.account
			m.confirmPrompt = "Permanently delete this message? (y/N)"
			m.onConfirm = func(m Model) (Model, tea.Cmd) {
				return m.permanentDelete(account, folder, uid)
			}
			return m, nil
		}
		return m.trashMessage(uid)

	case "esc", "backspace":
		switch m.view {
		case viewAccounts:
		case viewFolders:
			if len(m.accounts) > 1 {
				m.view = viewAccounts
				m.cursor = 0
			}
		case viewMessages:
			m.view = viewFolders
			m.cursor = 0
		case viewMessage:
			m.view = viewMessages
			m.message = nil
			// Invalidate any in-flight mark-read tick for the closed message.
			m.markReadGen++
		}
	}
	return m, nil
}

// moveCursor shifts the selection by delta (-1 or +1) in whatever list the
// current view shows. In viewMessage j/k navigates to the next/previous
// message and re-opens it.
func (m Model) moveCursor(delta int) (Model, tea.Cmd) {
	switch m.view {
	case viewAccounts:
		m.cursor = clamp(m.cursor+delta, len(m.accounts)-1)
	case viewFolders:
		m.cursor = clamp(m.cursor+delta, len(m.folders)-1)
	case viewMessages:
		m.cursor = clamp(m.cursor+delta, len(m.messages)-1)
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
	case viewMessage:
		next := clamp(m.cursor+delta, len(m.messages)-1)
		if next == m.cursor {
			return m, nil
		}
		m.cursor = next
		// Keep the hidden list's viewport aligned so esc-back lands on a
		// visible cursor. openMessage also bumps the mark-read generation.
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
		mm, cmd := m.openMessage(m.messages[m.cursor].UID)
		return mm.(Model), cmd
	}
	return m, nil
}

// scrollBy jumps the cursor by delta rows — used for page-scroll keys. In
// viewMessage we fall back to single-step navigation because "half a page"
// of messages doesn't map cleanly to the detail view.
func (m Model) scrollBy(delta int) (Model, tea.Cmd) {
	if delta == 0 {
		return m, nil
	}
	if m.view == viewMessage {
		step := 1
		if delta < 0 {
			step = -1
		}
		return m.moveCursor(step)
	}
	m.cursor = clamp(m.cursor+delta, listLen(m)-1)
	if m.view == viewMessages {
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
	}
	return m, nil
}

// listLen returns the number of rows in whatever list the current view shows.
func listLen(m Model) int {
	switch m.view {
	case viewAccounts:
		return len(m.accounts)
	case viewFolders:
		return len(m.folders)
	case viewMessages, viewMessage:
		return len(m.messages)
	}
	return 0
}
