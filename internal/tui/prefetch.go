package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// prefetch returns a prefetch command for msg, or nil if already prefetched or
// already cached with a body. Uses msg.Folder so it works correctly in the
// Combined tab where m.folder is empty.
func (m Model) prefetch(msg types.Message) tea.Cmd {
	key := prefetchKey{account: m.account, folder: msg.Folder, uid: msg.UID}
	if m.prefetched[key] {
		return nil
	}
	if msg.BodyText != "" {
		m.prefetched[key] = true
		return nil
	}
	m.prefetched[key] = true
	return prefetchMessage(m.client, m.account, msg.Folder, msg.UID)
}

// prefetchAfter warms up to n messages starting at startIdx.
func (m Model) prefetchAfter(startIdx, n int) []tea.Cmd {
	var cmds []tea.Cmd
	end := startIdx + n
	if end > len(m.messages) {
		end = len(m.messages)
	}
	for i := startIdx; i < end; i++ {
		if cmd := m.prefetch(m.messages[i]); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// prefetchFirstNUnread warms the first n unread messages in the list.
func (m Model) prefetchFirstNUnread(n int) []tea.Cmd {
	var cmds []tea.Cmd
	found := 0
	for _, msg := range m.messages {
		if found >= n {
			break
		}
		if !msg.Unread {
			continue
		}
		found++
		if cmd := m.prefetch(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// prefetchOnFolderOpen warms the two newest messages and the first two unread
// so common entry points (latest arrivals + unread backlog) feel instant.
func (m Model) prefetchOnFolderOpen() []tea.Cmd {
	cmds := m.prefetchAfter(0, 2)
	cmds = append(cmds, m.prefetchFirstNUnread(2)...)
	return cmds
}
