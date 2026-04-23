package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// openMessage transitions to viewMessage for the given UID and schedules the
// mark-as-read tick. It reads the body from the local cache when available so
// re-opening a previously read message is instant. Prefetches the next two
// messages after the cursor to warm the cache for likely-next navigation.
func (m Model) openMessage(uid uint32) (tea.Model, tea.Cmd) {
	m.view = viewMessage
	m.bodyTop = 0
	m.markReadGen++
	gen := m.markReadGen

	// Seed m.message from the list so the header area renders immediately.
	// The body will either be present (cached) or filled in when the fetch
	// returns.
	idx := messageIndex(m.messages, m.folder, uid)
	var cached types.Message
	if idx >= 0 {
		cached = m.messages[idx]
	} else {
		cached = types.Message{UID: uid, Folder: m.folder}
	}
	cp := cached
	m.message = &cp

	cmds := []tea.Cmd{
		tea.Tick(m.markReadDelay, func(time.Time) tea.Msg {
			return tickMarkRead{gen: gen, account: m.account, folder: m.folder, uid: uid}
		}),
	}
	// Only fetch the body across the wire when the daemon hasn't fetched it yet.
	if !cached.BodyFetched {
		cmds = append(cmds, fetchMessage(m.client, m.account, m.folder, uid))
	}
	cmds = append(cmds, m.prefetchAfter(m.cursor+1, 2)...)
	return m, tea.Batch(cmds...)
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
// doesn't stare at a now-removed message.
func (m Model) trashMessage(uid uint32) (Model, tea.Cmd) {
	account, folder := m.account, m.folder
	m.popIfViewing(folder, uid)
	m.removeLocalMessage(folder, uid)
	return m, trashCmd(m.client, account, folder, uid)
}

// permanentDelete is the trash-folder variant — expunges on the server and
// removes the row locally after confirmation.
func (m Model) permanentDelete(account, folder string, uid uint32) (Model, tea.Cmd) {
	m.popIfViewing(folder, uid)
	m.removeLocalMessage(folder, uid)
	return m, permanentDeleteCmd(m.client, account, folder, uid)
}

// popIfViewing returns to viewMessages when the detail view is showing
// (folder, uid). Cancels any in-flight mark-read tick for that message.
func (m *Model) popIfViewing(folder string, uid uint32) {
	if m.view == viewMessage && m.message != nil &&
		m.message.UID == uid && m.message.Folder == folder {
		m.view = viewMessages
		m.message = nil
		m.markReadGen++
	}
}

// removeLocalMessage drops a row from m.messages and keeps the cursor +
// viewport in a sensible place.
func (m *Model) removeLocalMessage(folder string, uid uint32) {
	idx := messageIndex(m.messages, folder, uid)
	if idx < 0 {
		return
	}
	m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
	if m.cursor >= len(m.messages) {
		m.cursor = len(m.messages) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
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
