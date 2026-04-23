package tui

import "github.com/wkirschbaum/whkmail/internal/types"

// Pure helpers. No I/O, no shared state. Each one is directly testable and
// is exercised by app_test.go + render_test.go.

// messageIndex returns the index of the message in msgs matching folder+uid,
// or -1 when not present.
func messageIndex(msgs []types.Message, folder string, uid uint32) int {
	for i := range msgs {
		if msgs[i].UID == uid && msgs[i].Folder == folder {
			return i
		}
	}
	return -1
}

// mergeMessages returns next with body text preserved from prev whenever prev
// still holds a cached body for the same folder+uid. Sync-refreshed headers
// otherwise blow away bodies we already downloaded.
func mergeMessages(prev, next []types.Message) []types.Message {
	if len(prev) == 0 {
		return next
	}
	type key struct {
		folder string
		uid    uint32
	}
	index := make(map[key]int, len(prev))
	for i := range prev {
		index[key{folder: prev[i].Folder, uid: prev[i].UID}] = i
	}
	for i := range next {
		if next[i].BodyText != "" {
			continue
		}
		if j, ok := index[key{folder: next[i].Folder, uid: next[i].UID}]; ok {
			next[i].BodyText = prev[j].BodyText
		}
	}
	return next
}

// visibleRows returns the number of message rows the terminal can show at
// once, accounting for the header and footer lines rendered around the list.
func (m Model) visibleRows() int {
	// 3 = account/folder line + header line + help line.
	const chrome = 3
	n := m.height - chrome
	if n < 1 {
		return 1
	}
	return n
}

// adjustViewport returns a new top offset so cursor stays inside the window
// of visible rows. total is the total number of rows in the list.
func adjustViewport(top, cursor, visible, total int) int {
	if total <= visible {
		return 0
	}
	maxTop := total - visible
	// Sanitize a stale top first so the scroll decisions below operate on
	// a valid window.
	switch {
	case top < 0:
		top = 0
	case top > maxTop:
		top = maxTop
	}
	if cursor < top {
		return cursor
	}
	if cursor >= top+visible {
		return cursor - visible + 1
	}
	return top
}

// clamp returns v clamped to [0, hi]. Returns 0 when hi < 0 (empty slice).
func clamp(v, hi int) int {
	if hi < 0 {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

// isTrashFolder matches the common names for an account's trash mailbox.
// Mirrors the daemon-side discoverTrashFolder fallback list so the two ends
// agree on which folders trigger the permanent-delete confirmation prompt.
func isTrashFolder(name string) bool {
	switch name {
	case "[Gmail]/Trash", "Trash", "Deleted Items", "Deleted Messages":
		return true
	}
	return false
}
