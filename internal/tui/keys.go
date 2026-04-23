package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleKey dispatches one keystroke. Modal popups short-circuit the
// normal flow; otherwise we table-lookup the key and invoke its handler.
// Context-sensitivity lives inside each handler (e.g. doMarkUnread is a
// no-op outside the message views).
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.modal != nil {
		return m.modal.handleKey(m, msg)
	}
	h, ok := keyHandlers[msg.String()]
	if !ok {
		return m, nil
	}
	return h(m)
}

// keyHandlers maps a bubbletea key string to the action invoked when the
// user presses it. Multiple keys can target the same action (e.g. `j`
// and `down` both doDown). A future per-user-rebinding feature can mutate
// this table at startup from the config file.
var keyHandlers = map[string]func(Model) (Model, tea.Cmd){
	"ctrl+c":    doQuit,
	"ctrl+d":    doQuit,
	"r":         doRefresh,
	"R":         doRefresh,
	"j":         doDown,
	"down":      doDown,
	" ":         doDown,
	"k":         doUp,
	"up":        doUp,
	"pgdown":    doHalfPageDown,
	"ctrl+u":    doHalfPageUp,
	"pgup":      doHalfPageUp,
	"n":         doJumpNext,
	"J":         doJumpNext,
	"p":         doJumpPrev,
	"K":         doJumpPrev,
	"g":         doTop,
	"G":         doBottom,
	"enter":     doOpen,
	"d":         doTrash,
	"N":         doMarkUnread,
	"s":         doMarkRead,
	"!":         doMarkRead,
	",":         doOpenStylePicker,
	"?":         doOpenHelp,
	"esc":       doBack,
	"backspace": doBack,
}

func doQuit(m Model) (Model, tea.Cmd) { return m, tea.Quit }

func doRefresh(m Model) (Model, tea.Cmd) {
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
	return m, nil
}

func doDown(m Model) (Model, tea.Cmd) {
	if m.view == viewMessage {
		return m.scrollBody(+1)
	}
	return m.moveCursor(+1)
}

func doUp(m Model) (Model, tea.Cmd) {
	if m.view == viewMessage {
		return m.scrollBody(-1)
	}
	return m.moveCursor(-1)
}

func doHalfPageDown(m Model) (Model, tea.Cmd) {
	if m.view == viewMessage {
		return m.scrollBody(+m.visibleBodyRows() / 2)
	}
	return m.scrollBy(+m.visibleRows() / 2)
}

func doHalfPageUp(m Model) (Model, tea.Cmd) {
	if m.view == viewMessage {
		return m.scrollBody(-m.visibleBodyRows() / 2)
	}
	return m.scrollBy(-m.visibleRows() / 2)
}

func doJumpNext(m Model) (Model, tea.Cmd) {
	if m.view == viewMessage {
		return m.jumpMessage(+1)
	}
	return m, nil
}

func doJumpPrev(m Model) (Model, tea.Cmd) {
	if m.view == viewMessage {
		return m.jumpMessage(-1)
	}
	return m, nil
}

func doTop(m Model) (Model, tea.Cmd) {
	if m.view == viewMessages && len(m.messages) > 0 {
		m.cursor = 0
		m.msgTop = 0
	}
	return m, nil
}

func doBottom(m Model) (Model, tea.Cmd) {
	if m.view == viewMessages && len(m.messages) > 0 {
		m.cursor = len(m.messages) - 1
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
	}
	return m, nil
}

func doOpen(m Model) (Model, tea.Cmd) {
	switch m.view {
	case viewAccounts:
		if len(m.accounts) == 0 {
			return m, nil
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
			return m, nil
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
			return m, nil
		}
		return m.openMessage(m.messages[m.cursor].UID)
	}
	return m, nil
}

func doTrash(m Model) (Model, tea.Cmd) {
	uid, ok := m.currentMessageUID()
	if !ok {
		return m, nil
	}
	if isTrashFolder(m.folder) {
		folder, account := m.folder, m.account
		m.modal = confirmModal{
			prompt: "Permanently delete this message? (y/N)",
			onConfirm: func(m Model) (Model, tea.Cmd) {
				return m.permanentDelete(account, folder, uid)
			},
		}
		return m, nil
	}
	return m.trashMessage(uid)
}

// doMarkUnread marks the current message unread. From the detail view it
// also pops back to the list — the "I want to deal with this later"
// intent. The ack from msgMarkedUnread handles the local flag flip.
func doMarkUnread(m Model) (Model, tea.Cmd) {
	if m.view != viewMessages && m.view != viewMessage {
		return m, nil
	}
	uid, ok := m.currentMessageUID()
	if !ok {
		return m, nil
	}
	account, folder := m.account, m.folder
	if m.view == viewMessage {
		m.view = viewMessages
		m.message = nil
		// Invalidate any in-flight auto-mark-read tick so it can't flip
		// the message back to read after we've just unmarked it.
		m.mark.gen++
	}
	return m, markUnreadCmd(m.client, account, folder, uid)
}

func doMarkRead(m Model) (Model, tea.Cmd) {
	if m.view != viewMessages {
		return m, nil
	}
	uid, ok := m.currentMessageUID()
	if !ok {
		return m, nil
	}
	return m, markReadCmd(m.client, m.account, m.folder, uid)
}

func doOpenStylePicker(m Model) (Model, tea.Cmd) {
	return m.openStylePicker(), nil
}

func doOpenHelp(m Model) (Model, tea.Cmd) {
	m.modal = helpModal{}
	return m, nil
}

func doBack(m Model) (Model, tea.Cmd) {
	switch m.view {
	case viewAccounts:
		// Already at the top of the nav stack — nothing to do.
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
		m.mark.gen++
	}
	return m, nil
}

// scrollBody scrolls the message body by delta lines, clamping at the ends.
func (m Model) scrollBody(delta int) (Model, tea.Cmd) {
	lines := m.bodyLines()
	visible := m.visibleBodyRows()
	maxTop := len(lines) - visible
	if maxTop < 0 {
		maxTop = 0
	}
	m.bodyTop = clamp(m.bodyTop+delta, maxTop)
	return m, nil
}

// jumpMessage navigates directly to the adjacent message without going
// through the body-scroll edge mechanic.
func (m Model) jumpMessage(delta int) (Model, tea.Cmd) {
	next := clamp(m.cursor+delta, len(m.messages)-1)
	if next == m.cursor {
		return m, nil
	}
	m.cursor = next
	m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
	return m.openMessage(m.messages[m.cursor].UID)
}

// moveCursor shifts the selection by delta (-1 or +1) in whatever list
// the current view shows.
func (m Model) moveCursor(delta int) (Model, tea.Cmd) {
	switch m.view {
	case viewAccounts:
		m.cursor = clamp(m.cursor+delta, len(m.accounts)-1)
	case viewFolders:
		m.cursor = clamp(m.cursor+delta, len(m.folders)-1)
	case viewMessages:
		m.cursor = clamp(m.cursor+delta, len(m.messages)-1)
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
	}
	return m, nil
}

// scrollBy jumps the cursor by delta rows — used for page-scroll keys.
func (m Model) scrollBy(delta int) (Model, tea.Cmd) {
	if delta == 0 {
		return m, nil
	}
	m.cursor = clamp(m.cursor+delta, listLen(m)-1)
	if m.view == viewMessages {
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
	}
	return m, nil
}

// listLen returns the number of rows in whatever list the current view
// shows.
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
