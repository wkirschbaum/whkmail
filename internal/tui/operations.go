package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// openMessage transitions to viewMessage for the given message and schedules
// the mark-as-read tick. It reads the body from the local cache when available
// so re-opening a previously read message is instant. Prefetches the next two
// messages after the cursor to warm the cache for likely-next navigation.
// Takes a full Message so the folder is always correct, including in the
// Combined tab where m.folder is empty.
func (m Model) openMessage(msg types.Message) (Model, tea.Cmd) {
	m.view = viewMessage
	m.bodyTop = 0
	m.mark.gen++
	gen := m.mark.gen

	cp := msg
	m.message = &cp

	cmds := []tea.Cmd{
		tea.Tick(m.mark.delay, func(time.Time) tea.Msg {
			return tickMarkRead{gen: gen, account: m.account, folder: msg.Folder, uid: msg.UID}
		}),
	}
	if !msg.BodyFetched {
		cmds = append(cmds, fetchMessage(m.client, m.account, msg.Folder, msg.UID))
	}
	cmds = append(cmds, m.prefetchAfter(m.cursor+1, 2)...)
	return m, tea.Batch(cmds...)
}

// currentMessageFolder returns the folder of the currently focused message.
// In the Combined tab (m.folder = "") the folder is read from the message
// itself, so operations always target the right mailbox.
func (m Model) currentMessageFolder() string {
	switch m.view {
	case viewMessages:
		if m.cursor >= 0 && m.cursor < len(m.messages) {
			return m.messages[m.cursor].Folder
		}
	case viewMessage:
		if m.message != nil {
			return m.message.Folder
		}
	}
	return m.folder
}

// currentMessageUID returns the UID of the message the user is currently
// focused on — the cursor row in viewMessages or the open message in
// viewMessage. Returns ok=false in every other view.
func (m Model) currentMessageUID() (uint32, bool) {
	switch m.view {
	case viewMessages:
		if len(m.messages) == 0 {
			return 0, false
		}
		return m.messages[m.cursor].UID, true
	case viewMessage:
		if m.message == nil {
			return 0, false
		}
		return m.message.UID, true
	}
	return 0, false
}

// trashMessage runs the optimistic local delete + daemon-side trash. If the
// message was open in the detail view, we pop back to the list so the user
// doesn't stare at a now-removed message. folder must be the message's own
// folder (not m.folder, which is empty in the Combined tab).
func (m Model) trashMessage(folder string, uid uint32) (Model, tea.Cmd) {
	account := m.account
	m.popIfViewing(folder, uid)
	m.removeLocalMessage(folder, uid)
	return m, tea.Batch(trashCmd(m.client, account, folder, uid), tea.SetWindowTitle(m.windowTitle()))
}

// permanentDelete is the trash-folder variant — expunges on the server and
// removes the row locally after confirmation.
func (m Model) permanentDelete(account, folder string, uid uint32) (Model, tea.Cmd) {
	m.popIfViewing(folder, uid)
	m.removeLocalMessage(folder, uid)
	return m, tea.Batch(permanentDeleteCmd(m.client, account, folder, uid), tea.SetWindowTitle(m.windowTitle()))
}

// popIfViewing returns to viewMessages when the detail view is showing
// (folder, uid). Cancels any in-flight mark-read tick for that message.
func (m *Model) popIfViewing(folder string, uid uint32) {
	if m.view == viewMessage && m.message != nil &&
		m.message.UID == uid && m.message.Folder == folder {
		m.view = viewMessages
		m.message = nil
		m.mark.gen++
	}
}

// removeLocalMessage drops a row from m.messages, keeps the cursor +
// viewport in a sensible place, and decrements the matching folder's counts
// so the folder sidebar reflects the deletion without waiting for a sync.
func (m *Model) removeLocalMessage(folder string, uid uint32) {
	idx := messageIndex(m.messages, folder, uid)
	if idx < 0 {
		return
	}
	msg := m.messages[idx]
	m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
	if idx < len(m.msgDepths) {
		m.msgDepths = append(m.msgDepths[:idx], m.msgDepths[idx+1:]...)
	}
	if m.cursor >= len(m.messages) {
		m.cursor = len(m.messages) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))

	for i := range m.folders {
		if m.folders[i].Name == folder {
			if m.folders[i].MessageCount > 0 {
				m.folders[i].MessageCount--
			}
			if msg.Unread && m.folders[i].Unread > 0 {
				m.folders[i].Unread--
			}
			break
		}
	}
}

// mergeFetched writes a freshly-fetched message into the local cache and
// refreshes the open detail view when it's the one on screen.
func (m *Model) mergeFetched(fetched *types.Message) {
	if idx := messageIndex(m.messages, fetched.Folder, fetched.UID); idx >= 0 {
		m.messages[idx] = *fetched
	}
	if m.view == viewMessage && m.message != nil &&
		m.message.UID == fetched.UID && m.message.Folder == fetched.Folder {
		cp := *fetched
		m.message = &cp
	}
}
