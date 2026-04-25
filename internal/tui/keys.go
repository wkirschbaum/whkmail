package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleKey dispatches one keystroke. Modal popups and the compose pane
// short-circuit the normal flow; otherwise we table-lookup the key and
// invoke its handler. Context-sensitivity lives inside each handler
// (e.g. doMarkUnread is a no-op outside the message views).
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.modal != nil {
		return m.modal.handleKey(m, msg)
	}
	if m.compose != nil {
		return m.handleComposeKey(msg)
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
//
// `r` is reply-all (the default action), `R` is reply-sender. This
// inverts the traditional mutt convention (r=sender, R=all) because the
// user's preference was for reply-all to be the primary action. There's
// no refresh keybinding — the status bar spinner + automatic re-fetch on
// KindSyncDone obsolete it.
var keyHandlers = map[string]func(Model) (Model, tea.Cmd){
	"ctrl+c":    doQuit,
	"ctrl+d":    doQuit,
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
	"S":         doMarkSpam,
	"$":         doMarkSpam,
	"N":         doMarkUnread,
	"s":         doMarkRead,
	"!":         doMarkRead,
	"r":         doReplyAll,
	"R":         doReplySender,
	",":         doOpenStylePicker,
	"?":         doOpenHelp,
	"esc":       doBack,
	"backspace": doBack,
	"[":         doTabLeft,
	"]":         doTabRight,
	"tab":       doTabRight,
	"shift+tab": doTabLeft,
	"m":         doFolderManager,
}

func doQuit(m Model) (Model, tea.Cmd) { return m, tea.Quit }

// doReplyAll opens a compose pane pre-populated with reply-to-all
// recipients. The original body is quoted; the textarea cursor starts
// above the quote so the user can type their reply directly.
func doReplyAll(m Model) (Model, tea.Cmd) {
	return m.openReply(true)
}

// doReplySender is the narrower reply — only the original sender goes on
// the To line.
func doReplySender(m Model) (Model, tea.Cmd) {
	return m.openReply(false)
}

// openReply is the shared entry point for the two reply modes. Rejects
// anything that isn't a viewable message (no open mail, wrong view) so
// the user gets silence on a stray keypress instead of a half-populated
// compose. The source folder is captured here so the daemon can re-sync
// it after the reply is sent (picks up the \Answered flag).
func (m Model) openReply(all bool) (Model, tea.Cmd) {
	if m.view != viewMessage || m.message == nil {
		return m, nil
	}
	cs := newComposeState(*m.message, m.account, all, m.message.Folder)
	cs.resize(m.width-4, composePaneRows(m.height))
	m.compose = &cs
	return m, nil
}

// composePaneRows returns how many rows the textarea gets. We reserve
// about a third of the screen for the open message and give the rest to
// the compose pane. Floors at 4 so the textarea is always usable.
func composePaneRows(height int) int {
	if height <= 0 {
		return 10
	}
	rows := height * 2 / 3
	if rows < 4 {
		return 4
	}
	return rows
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
		m.folder = ""
		m.messages = nil
	case viewFolders:
		visible := m.visibleFolders()
		if len(visible) == 0 {
			return m, nil
		}
		chosen := visible[m.cursor]
		m.folder = chosen.Name
		// Set activeTab to the chosen folder's index in m.folders (+1 for Combined offset).
		for i, f := range m.folders {
			if f.Name == chosen.Name {
				m.activeTab = i + 1
				break
			}
		}
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
		return m.openMessage(m.messages[m.cursor])
	}
	return m, nil
}

func doTabLeft(m Model) (Model, tea.Cmd) {
	if m.view != viewMessages {
		return m, nil
	}
	tabs := m.visibleTabIndices()
	if len(tabs) == 0 {
		return m, nil
	}
	pos := 0
	for i, t := range tabs {
		if t == m.activeTab {
			pos = i
			break
		}
	}
	pos = (pos - 1 + len(tabs)) % len(tabs)
	m.activeTab = tabs[pos]
	return m.switchTab()
}

func doTabRight(m Model) (Model, tea.Cmd) {
	if m.view != viewMessages {
		return m, nil
	}
	tabs := m.visibleTabIndices()
	if len(tabs) == 0 {
		return m, nil
	}
	pos := 0
	for i, t := range tabs {
		if t == m.activeTab {
			pos = i
			break
		}
	}
	pos = (pos + 1) % len(tabs)
	m.activeTab = tabs[pos]
	return m.switchTab()
}

// switchTab resets message state and fetches content for the newly active tab.
func (m Model) switchTab() (Model, tea.Cmd) {
	m.cursor = 0
	m.msgTop = 0
	m.messages = nil
	m.loading = true
	if m.activeTab == 0 {
		m.folder = ""
		combined := m.combinedFolderNames()
		if len(combined) == 0 {
			m.loading = false
			return m, nil
		}
		return m, fetchCombinedMessages(m.client, m.account, combined)
	}
	m.folder = m.folders[m.activeTab-1].Name
	return m, fetchMessages(m.client, m.account, m.folder)
}

func doFolderManager(m Model) (Model, tea.Cmd) {
	if m.view != viewFolders && m.view != viewMessages {
		return m, nil
	}
	m.modal = folderManagerModal{}
	return m, nil
}

// doMarkSpam moves the current message to the spam/junk folder. Works in both
// the message list and the detail view; no-ops outside the message views or
// when the message is already in the spam folder.
func doMarkSpam(m Model) (Model, tea.Cmd) {
	if m.view != viewMessages && m.view != viewMessage {
		return m, nil
	}
	uid, ok := m.currentMessageUID()
	if !ok {
		return m, nil
	}
	folder := m.currentMessageFolder()
	if isSpamFolder(folder) {
		return m, nil
	}
	return m.spamMessage(folder, uid)
}

func doTrash(m Model) (Model, tea.Cmd) {
	uid, ok := m.currentMessageUID()
	if !ok {
		return m, nil
	}
	folder := m.currentMessageFolder()
	if isTrashFolder(folder) {
		account := m.account
		m.modal = confirmModal{
			prompt: "Permanently delete this message? (y/N)",
			onConfirm: func(m Model) (Model, tea.Cmd) {
				return m.permanentDelete(account, folder, uid)
			},
		}
		return m, nil
	}
	return m.trashMessage(folder, uid)
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
	account := m.account
	folder := m.currentMessageFolder()
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
	return m, markReadCmd(m.client, m.account, m.currentMessageFolder(), uid)
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
		// Return to the combined messages view rather than a dead end.
		m.view = viewMessages
	case viewFolders:
		if len(m.accounts) > 1 {
			m.view = viewAccounts
			m.cursor = 0
		} else {
			// Single-account: fold list is a sidebar, not the home screen.
			m.view = viewMessages
		}
	case viewMessages:
		if len(m.accounts) > 1 {
			m.view = viewAccounts
			m.cursor = 0
		} else {
			m.view = viewFolders
			m.cursor = 0
		}
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
	return m.openMessage(m.messages[m.cursor])
}

// moveCursor shifts the selection by delta (-1 or +1) in whatever list
// the current view shows.
func (m Model) moveCursor(delta int) (Model, tea.Cmd) {
	switch m.view {
	case viewAccounts:
		m.cursor = clamp(m.cursor+delta, len(m.accounts)-1)
	case viewFolders:
		m.cursor = clamp(m.cursor+delta, len(m.visibleFolders())-1)
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
		return len(m.visibleFolders())
	case viewMessages, viewMessage:
		return len(m.messages)
	}
	return 0
}
