package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/events"
)

// Update is bubbletea's event dispatch. One case per message type;
// delegate heavy lifting to small helpers so each case stays one screen's
// worth.
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
		// Only touch local state when the ack is for the account we're
		// viewing — otherwise a stale ack after an account switch would
		// flip the wrong message.
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
		// Server confirmed the mutation — nothing more to do, the
		// optimistic local update already happened at key-press time. A
		// typed nil msg would do the same but typed acks are handy for
		// tests.

	case msgFlash:
		// Stale tick: a more recent KindNewMessage has already scheduled
		// a later fire, so this one carries out-of-date pending state.
		if msg.gen != m.flash.gen {
			return m, nil
		}
		if n := len(m.flash.pending); n > 0 {
			m.flash.text = formatFlash(m.flash.pending)
			m.flash.pending = nil
		}

	case tickMarkRead:
		// Only honour the most recently scheduled tick and only while the
		// message is still open.
		if msg.gen != m.mark.gen || m.view != viewMessage {
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
		next, cmd := m.handleEvent(events.Event(msg))
		return next, tea.Batch(cmd, waitEvent(next.eventCh))

	case msgErr:
		m.err = msg.err
		m.loading = false

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleEvent reacts to a daemon-side push. Account-scoped events
// (KindNewMessage, KindBodyReady) are ignored for other accounts so a
// multi-account session stays sane; sync-lifecycle events update the
// global status indicator regardless. Returns the updated Model so the
// caller can thread the mutation back through Update without worrying
// about pointer aliasing.
func (m Model) handleEvent(e events.Event) (Model, tea.Cmd) {
	switch e.Kind {
	case events.KindSyncStarted:
		m.sync = syncState{active: true}
		return m, nil

	case events.KindSyncProgress:
		m.sync = syncState{
			active:  true,
			folder:  e.Folder,
			current: e.Current,
			total:   e.Total,
		}
		return m, nil

	case events.KindSyncDone:
		m.sync = syncState{}
		// Status always refreshes so the total-unread counter stays in
		// sync across views. Re-fetch messages too if we're looking at a
		// list, otherwise new arrivals would stay invisible until the
		// user pressed r.
		cmds := []tea.Cmd{fetchStatus(m.client)}
		if m.view == viewMessages && m.folder != "" {
			cmds = append(cmds, fetchMessages(m.client, m.account, m.folder))
		}
		return m, tea.Batch(cmds...)

	case events.KindNewMessage:
		if e.Account != m.account {
			return m, nil
		}
		// Queue the arrival and (re)arm the debounce timer. A burst of
		// events bumps flash.gen each time, so any earlier msgFlash tick
		// that fires mid-burst sees the older gen and no-ops.
		m.flash.pending = append(m.flash.pending, flashEntry{subject: e.Subject, from: e.From})
		m.flash.gen++
		gen := m.flash.gen
		return m, tea.Tick(flashDebounce, func(time.Time) tea.Msg {
			return msgFlash{gen: gen}
		})

	case events.KindBodyReady:
		if e.Account != m.account {
			return m, nil
		}
		key := prefetchKey{account: m.account, folder: e.Folder, uid: e.UID}
		// A failed background fetch unsticks the view by replacing the
		// "Loading…" placeholder with the error; a successful one clears
		// any prior error and pulls the fresh body into the cache.
		if e.Error != "" {
			m.bodyErr[key] = e.Error
			return m, nil
		}
		delete(m.bodyErr, key)
		viewing := m.view == viewMessage && m.message != nil &&
			m.message.UID == e.UID && m.folder == e.Folder
		if viewing {
			return m, fetchMessage(m.client, m.account, m.folder, e.UID)
		}
		if m.prefetched[key] {
			return m, prefetchMessage(m.client, m.account, e.Folder, e.UID)
		}
	}
	return m, nil
}

// formatFlash renders the debounced-arrival summary. One message →
// "New: subject — from"; a burst → "N new · latest: subject — from". Pure
// function so the test suite can assert the format without touching the
// bubbletea runtime.
func formatFlash(pending []flashEntry) string {
	n := len(pending)
	if n == 0 {
		return ""
	}
	latest := pending[n-1]
	if n == 1 {
		return fmt.Sprintf("New: %s — %s", latest.subject, latest.from)
	}
	return fmt.Sprintf("%d new · latest: %s — %s", n, latest.subject, latest.from)
}

// waitEvent returns a command that blocks until one event arrives on ch.
// The command restarts itself from Update so the TUI keeps consuming the
// SSE stream for the life of the process.
func waitEvent(ch <-chan events.Event) tea.Cmd {
	return func() tea.Msg {
		return msgEvent(<-ch)
	}
}
