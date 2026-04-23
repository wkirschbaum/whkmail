package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/events"
)

// Update is bubbletea's event dispatch. One case per message type; delegate
// heavy lifting to small helpers so each case stays one screen's worth.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))

	case msgStatus:
		m.err = nil
		m.accounts = msg.Accounts
		m.loading = false
		switch len(m.accounts) {
		case 0:
		case 1:
			m.account = m.accounts[0].Account
			m.folders = m.accounts[0].Folders
			switch m.view {
			case viewAccounts:
				m.cursor = clamp(m.cursor, len(m.folders)-1)
				m.view = viewFolders
			case viewFolders:
				m.cursor = clamp(m.cursor, len(m.folders)-1)
			}
		default:
			for _, ac := range m.accounts {
				if ac.Account == m.account {
					m.folders = ac.Folders
					break
				}
			}
			if m.view == viewAccounts {
				m.cursor = clamp(m.cursor, len(m.accounts)-1)
			}
		}

	case msgMessages:
		m.err = nil
		// Remember which UID the cursor is on so we can restore it after
		// re-threading (thread order may differ from date order).
		var cursorUID uint32
		if m.cursor < len(m.messages) {
			cursorUID = m.messages[m.cursor].UID
		}
		merged := mergeMessages(m.messages, msg.Messages)
		m.messages, m.msgDepths = threadMessages(merged)
		// Restore cursor to the same UID; fall back to clamping.
		m.cursor = 0
		for i, msg := range m.messages {
			if msg.UID == cursorUID {
				m.cursor = i
				break
			}
		}
		m.cursor = clamp(m.cursor, len(m.messages)-1)
		m.msgTop = adjustViewport(m.msgTop, m.cursor, m.visibleRows(), len(m.messages))
		m.loading = false
		return m, tea.Batch(m.prefetchOnFolderOpen()...)

	case msgMessage:
		m.err = nil
		m.loading = false
		m.mergeFetched(&msg.Message)

	case msgPrefetched:
		if msg.message == nil {
			return m, nil
		}
		m.mergeFetched(msg.message)

	case msgMarkedRead:
		// Only touch local state when the ack is for the account we're viewing
		// — otherwise a stale ack after an account switch would flip the wrong
		// message.
		if msg.account != m.account {
			return m, nil
		}
		for i := range m.messages {
			if m.messages[i].UID == msg.uid && m.messages[i].Folder == msg.folder {
				m.messages[i].Unread = false
			}
		}
		if m.message != nil && m.message.UID == msg.uid && m.message.Folder == msg.folder {
			m.message.Unread = false
		}

	case msgMarkedUnread:
		if msg.account != m.account {
			return m, nil
		}
		for i := range m.messages {
			if m.messages[i].UID == msg.uid && m.messages[i].Folder == msg.folder {
				m.messages[i].Unread = true
			}
		}
		if m.message != nil && m.message.UID == msg.uid && m.message.Folder == msg.folder {
			m.message.Unread = true
		}

	case msgTrashed, msgDeleted:
		// Server confirmed the mutation — nothing more to do, the optimistic
		// local update already happened at key-press time. A typed nil msg
		// would do the same but typed acks are handy for tests.

	case tickMarkRead:
		// Only honour the most recently scheduled tick and only while the
		// message is still open.
		if msg.gen != m.markReadGen || m.view != viewMessage {
			return m, nil
		}
		if m.message == nil || m.message.UID != msg.uid || m.message.Folder != msg.folder {
			return m, nil
		}
		if !m.message.Unread {
			return m, nil
		}
		return m, markReadCmd(m.client, msg.account, msg.folder, msg.uid)

	case msgEvent:
		cmd := m.handleEvent(events.Event(msg))
		return m, tea.Batch(cmd, waitEvent(m.eventCh))

	case msgErr:
		m.err = msg.err
		m.loading = false

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleEvent reacts to a daemon-side push. Only events for the active
// account are acted on so a multi-account session stays sane.
func (m Model) handleEvent(e events.Event) tea.Cmd {
	if e.Account != m.account {
		return nil
	}
	switch e.Kind {
	case events.KindBodyReady:
		key := prefetchKey{account: m.account, folder: e.Folder, uid: e.UID}
		// A failed background fetch unsticks the view by replacing the
		// "Loading…" placeholder with the error; a successful one clears any
		// prior error and pulls the fresh body into the cache.
		if e.Error != "" {
			m.bodyErr[key] = e.Error
			return nil
		}
		delete(m.bodyErr, key)
		viewing := m.view == viewMessage && m.message != nil &&
			m.message.UID == e.UID && m.folder == e.Folder
		if viewing {
			return fetchMessage(m.client, m.account, m.folder, e.UID)
		}
		if m.prefetched[key] {
			return prefetchMessage(m.client, m.account, e.Folder, e.UID)
		}
	case events.KindSyncDone:
		if m.view == viewFolders {
			return fetchStatus(m.client)
		}
	}
	return nil
}

// waitEvent returns a command that blocks until one event arrives on ch. The
// command restarts itself from Update so the TUI keeps consuming the SSE
// stream for the life of the process.
func waitEvent(ch <-chan events.Event) tea.Cmd {
	return func() tea.Msg {
		return msgEvent(<-ch)
	}
}
