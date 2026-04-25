package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/wkirschbaum/whkmail/internal/types"
)

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

// mergeMessages returns next with body text and body-fetched flag preserved
// from prev whenever prev holds a cached body for the same folder+uid.
// Sync-refreshed headers otherwise blow away bodies we already downloaded.
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
		if j, ok := index[key{folder: next[i].Folder, uid: next[i].UID}]; ok {
			if next[i].BodyText == "" && !next[i].BodyFetched {
				next[i].BodyText = prev[j].BodyText
				next[i].BodyFetched = prev[j].BodyFetched
			}
			if next[i].MessageID == "" {
				next[i].MessageID = prev[j].MessageID
			}
			if next[i].InReplyTo == "" {
				next[i].InReplyTo = prev[j].InReplyTo
			}
		}
	}
	return next
}

// threadMessages takes a flat message list (any order) and returns the same
// messages re-ordered by thread: root messages newest-thread-first, with
// replies following their parent in chronological order. The parallel depths
// slice gives the nesting level of each row (0 = root, 1 = direct reply, …).
// Messages whose InReplyTo doesn't match any local MessageID become roots.
func threadMessages(msgs []types.Message) ([]types.Message, []int) {
	if len(msgs) == 0 {
		return nil, nil
	}

	// messageID → index in msgs
	byID := make(map[string]int, len(msgs))
	for i, m := range msgs {
		if m.MessageID != "" {
			byID[m.MessageID] = i
		}
	}

	// parent index → child indices
	children := make(map[int][]int, len(msgs))
	roots := make([]int, 0, len(msgs))
	for i, m := range msgs {
		if m.InReplyTo != "" {
			if pi, ok := byID[m.InReplyTo]; ok {
				children[pi] = append(children[pi], i)
				continue
			}
		}
		roots = append(roots, i)
	}

	// Sort children chronologically (oldest first within a thread).
	for pi := range children {
		cs := children[pi]
		sort.Slice(cs, func(a, b int) bool {
			return msgs[cs[a]].Date.Before(msgs[cs[b]].Date)
		})
	}

	// Compute the latest date in each subtree so roots can be sorted by it.
	latest := make([]time.Time, len(msgs))
	var subtreeLatest func(i int) time.Time
	subtreeLatest = func(i int) time.Time {
		t := msgs[i].Date
		for _, ci := range children[i] {
			if ct := subtreeLatest(ci); ct.After(t) {
				t = ct
			}
		}
		latest[i] = t
		return t
	}
	for _, ri := range roots {
		subtreeLatest(ri)
	}

	// Sort roots newest-thread-first.
	sort.Slice(roots, func(a, b int) bool {
		return latest[roots[a]].After(latest[roots[b]])
	})

	// DFS to produce the flat output in display order.
	out := make([]types.Message, 0, len(msgs))
	depths := make([]int, 0, len(msgs))
	visited := make(map[int]bool, len(msgs))

	var visit func(i, depth int)
	visit = func(i, depth int) {
		if visited[i] {
			return
		}
		visited[i] = true
		out = append(out, msgs[i])
		depths = append(depths, depth)
		for _, ci := range children[i] {
			visit(ci, depth+1)
		}
	}
	for _, ri := range roots {
		visit(ri, 0)
	}

	return out, depths
}

// visibleBodyRows returns the number of body lines that fit on screen in
// the message detail view. Chrome = up to 7 header lines + 1 blank
// separator + status bar. When a compose pane is open the body is
// constrained to roughly a third of the screen so the reply editor gets
// the rest — the user still needs enough of the original visible to
// reference while typing.
func (m Model) visibleBodyRows() int {
	if m.compose != nil {
		if m.height <= 0 {
			return 6
		}
		// Leave two-thirds for the compose pane.
		n := m.height / 3
		if n < 3 {
			n = 3
		}
		return n
	}
	const chrome = 8
	n := m.height - chrome
	if n < 1 {
		return 1
	}
	return n
}

// bodyLines wraps and splits the current message body into display lines.
// Returns nil when there is no body.
func (m Model) bodyLines() []string {
	if m.message == nil || m.message.BodyText == "" {
		return nil
	}
	body := strings.ReplaceAll(m.message.BodyText, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	width := m.width - 2
	if width < 40 {
		width = 80
	}
	return strings.Split(wrapBody(body, width), "\n")
}

// visibleRows returns the number of message rows the terminal can show at
// once, accounting for the tab bar (3 lines) and the status bar.
func (m Model) visibleRows() int {
	const chrome = 4 // tab bar top + tab bar content + separator + status bar
	n := m.height - chrome
	if n < 1 {
		return 1
	}
	return n
}

// visibleFolders returns the folders that are not hidden, in server order.
// Used by the folder list view and cursor clamping.
func (m Model) visibleFolders() []types.Folder {
	out := make([]types.Folder, 0, len(m.folders))
	for _, f := range m.folders {
		if folderStateFor(f.Name, m.folderStates) != FolderStateHidden {
			out = append(out, f)
		}
	}
	return out
}

// combinedFolderNames returns the names of folders whose state is
// FolderStateCombined, used to build the combined-tab fetch list.
func (m Model) combinedFolderNames() []string {
	var out []string
	for _, f := range m.folders {
		if folderStateFor(f.Name, m.folderStates) == FolderStateCombined {
			out = append(out, f.Name)
		}
	}
	return out
}

// visibleTabFolders returns the indices into m.folders (1-based) for tabs
// that should appear in the tab bar: Combined (index 0) plus one per
// non-hidden folder.
func (m Model) visibleTabIndices() []int {
	tabs := []int{0}
	for i, f := range m.folders {
		if folderStateFor(f.Name, m.folderStates) != FolderStateHidden {
			tabs = append(tabs, i+1)
		}
	}
	return tabs
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

// isTrashFolder matches the common names for an account's trash mailbox
// so the TUI knows when to show the permanent-delete confirmation prompt.
// Uses types.TrashFolderNames so this stays in sync with the IMAP syncer.
func isTrashFolder(name string) bool {
	for _, candidate := range types.TrashFolderNames {
		if name == candidate {
			return true
		}
	}
	return false
}
