package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/types"
)

func TestMessageIndex(t *testing.T) {
	msgs := []types.Message{
		{UID: 1, Folder: "INBOX"},
		{UID: 2, Folder: "INBOX"},
		{UID: 1, Folder: "Sent"},
	}
	cases := []struct {
		folder string
		uid    uint32
		want   int
	}{
		{"INBOX", 1, 0},
		{"INBOX", 2, 1},
		{"Sent", 1, 2},
		{"INBOX", 99, -1}, // unknown uid
		{"Drafts", 1, -1}, // unknown folder
	}
	for _, c := range cases {
		got := messageIndex(msgs, c.folder, c.uid)
		if got != c.want {
			t.Errorf("messageIndex(_, %q, %d) = %d, want %d", c.folder, c.uid, got, c.want)
		}
	}

	if got := messageIndex(nil, "INBOX", 1); got != -1 {
		t.Errorf("messageIndex(nil, …) = %d, want -1", got)
	}
}

func TestMergeMessages_EmptyPrev(t *testing.T) {
	next := []types.Message{{UID: 1, Folder: "INBOX"}}
	got := mergeMessages(nil, next)
	if len(got) != 1 || got[0].UID != 1 {
		t.Errorf("expected next returned unchanged, got %+v", got)
	}
}

func TestMergeMessages_PreservesBody(t *testing.T) {
	prev := []types.Message{
		{UID: 1, Folder: "INBOX", BodyText: "cached body"},
		{UID: 2, Folder: "INBOX", BodyText: "also cached"},
	}
	// Fresh sync: headers only, no bodies.
	next := []types.Message{
		{UID: 1, Folder: "INBOX"},
		{UID: 2, Folder: "INBOX"},
		{UID: 3, Folder: "INBOX"}, // new arrival
	}
	got := mergeMessages(prev, next)
	if got[0].BodyText != "cached body" {
		t.Errorf("uid 1 body lost: %q", got[0].BodyText)
	}
	if got[1].BodyText != "also cached" {
		t.Errorf("uid 2 body lost: %q", got[1].BodyText)
	}
	if got[2].BodyText != "" {
		t.Errorf("uid 3 should have no body, got %q", got[2].BodyText)
	}
}

func TestMergeMessages_DoesNotOverwriteFreshBody(t *testing.T) {
	prev := []types.Message{{UID: 1, Folder: "INBOX", BodyText: "stale"}}
	next := []types.Message{{UID: 1, Folder: "INBOX", BodyText: "fresh"}}
	got := mergeMessages(prev, next)
	if got[0].BodyText != "fresh" {
		t.Errorf("expected fresh body, got %q", got[0].BodyText)
	}
}

func TestMergeMessages_FolderScoped(t *testing.T) {
	// Same UID in a different folder must not bleed bodies across folders.
	prev := []types.Message{{UID: 1, Folder: "INBOX", BodyText: "inbox-body"}}
	next := []types.Message{{UID: 1, Folder: "Sent"}}
	got := mergeMessages(prev, next)
	if got[0].BodyText != "" {
		t.Errorf("body leaked across folders: %q", got[0].BodyText)
	}
}

func newTestModel() Model {
	return Model{
		account:       "a@b",
		folder:        "INBOX",
		markReadDelay: 0,
		prefetched:    make(map[prefetchKey]bool),
		bodyErr:       make(map[prefetchKey]string),
	}
}

func TestHandleEvent_BodyFailure_StoresError(t *testing.T) {
	m := newTestModel()
	m.view = viewMessage
	m.message = &types.Message{UID: 1, Folder: "INBOX"}

	cmd := m.handleEvent(events.Event{
		Kind:    events.KindBodyReady,
		Account: "a@b",
		Folder:  "INBOX",
		UID:     1,
		Error:   "imap down",
	})
	if cmd != nil {
		t.Errorf("failure event should not emit a re-fetch command")
	}
	key := prefetchKey{account: "a@b", folder: "INBOX", uid: 1}
	if got := m.bodyErr[key]; got != "imap down" {
		t.Errorf("bodyErr[%v]=%q, want %q", key, got, "imap down")
	}
}

func TestHandleEvent_BodySuccess_ClearsError(t *testing.T) {
	m := newTestModel()
	m.view = viewMessage
	m.message = &types.Message{UID: 1, Folder: "INBOX"}
	key := prefetchKey{account: "a@b", folder: "INBOX", uid: 1}
	m.bodyErr[key] = "stale"

	cmd := m.handleEvent(events.Event{
		Kind:    events.KindBodyReady,
		Account: "a@b",
		Folder:  "INBOX",
		UID:     1,
	})
	if cmd == nil {
		t.Error("success while viewing should trigger a re-fetch cmd")
	}
	if _, ok := m.bodyErr[key]; ok {
		t.Errorf("bodyErr was not cleared on success: %q", m.bodyErr[key])
	}
}

func TestHandleKey_TrashOutsideTrashFolder(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.height = 10
	m.folder = "INBOX"
	m.messages = []types.Message{
		{UID: 1, Folder: "INBOX", Subject: "keep"},
		{UID: 2, Folder: "INBOX", Subject: "trash me"},
		{UID: 3, Folder: "INBOX", Subject: "also keep"},
	}
	m.cursor = 1

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := mm.(Model)

	// Optimistic removal.
	if len(got.messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(got.messages))
	}
	for _, msg := range got.messages {
		if msg.UID == 2 {
			t.Error("trashed message was not removed from the list")
		}
	}
	// No pending confirmation outside of the Trash folder.
	if got.confirmPrompt != "" {
		t.Errorf("unexpected confirm prompt: %q", got.confirmPrompt)
	}
	// A command should have been returned to actually hit the daemon.
	if cmd == nil {
		t.Error("expected a trashCmd")
	}
}

func TestHandleKey_TrashInTrashPromptsConfirm(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.height = 10
	m.folder = "[Gmail]/Trash"
	m.messages = []types.Message{{UID: 5, Folder: "[Gmail]/Trash", Subject: "already trashed"}}

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := mm.(Model)

	if got.confirmPrompt == "" {
		t.Error("expected a confirmation prompt in the Trash folder")
	}
	if got.onConfirm == nil {
		t.Error("expected an onConfirm handler")
	}
	if len(got.messages) != 1 {
		t.Errorf("message must not be removed before confirmation, got %d rows", len(got.messages))
	}
	if cmd != nil {
		t.Error("confirm state must not dispatch a command yet")
	}
}

func TestHandleKey_ConfirmYExecutes(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.height = 10
	m.folder = "[Gmail]/Trash"
	m.messages = []types.Message{{UID: 5, Folder: "[Gmail]/Trash"}}

	// Put into confirmation state.
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = mm.(Model)

	// Press y to confirm.
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := mm.(Model)

	if got.confirmPrompt != "" {
		t.Errorf("confirmPrompt should clear after confirm, got %q", got.confirmPrompt)
	}
	if len(got.messages) != 0 {
		t.Errorf("message should have been removed after confirm, got %d", len(got.messages))
	}
	if cmd == nil {
		t.Error("expected a permanentDeleteCmd after confirm")
	}
}

func TestHandleKey_ConfirmNCancels(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.height = 10
	m.folder = "[Gmail]/Trash"
	m.messages = []types.Message{{UID: 5, Folder: "[Gmail]/Trash"}}

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = mm.(Model)

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got := mm.(Model)

	if got.confirmPrompt != "" {
		t.Errorf("confirmPrompt should clear on cancel, got %q", got.confirmPrompt)
	}
	if len(got.messages) != 1 {
		t.Errorf("message must be preserved on cancel, got %d", len(got.messages))
	}
	if cmd != nil {
		t.Error("cancel must not dispatch a command")
	}
}

func TestIsTrashFolder(t *testing.T) {
	cases := map[string]bool{
		"[Gmail]/Trash":    true,
		"Trash":            true,
		"Deleted Items":    true,
		"Deleted Messages": true,
		"INBOX":            false,
		"Archive":          false,
		"":                 false,
	}
	for name, want := range cases {
		if got := isTrashFolder(name); got != want {
			t.Errorf("isTrashFolder(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestHandleKey_SpaceMovesDown(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.height = 10
	m.messages = []types.Message{
		{UID: 1, Folder: "INBOX"},
		{UID: 2, Folder: "INBOX"},
		{UID: 3, Folder: "INBOX"},
	}

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if mm.(Model).cursor != 1 {
		t.Errorf("space: cursor = %d, want 1", mm.(Model).cursor)
	}
}

func TestAdjustViewport(t *testing.T) {
	cases := []struct {
		name                        string
		top, cursor, visible, total int
		want                        int
	}{
		{"all fits in window", 0, 2, 10, 5, 0},
		{"cursor above top scrolls up", 5, 2, 3, 20, 2},
		{"cursor below bottom scrolls down", 0, 5, 3, 20, 3},
		{"cursor in range keeps top", 2, 3, 3, 20, 2},
		{"top beyond max clamps down", 50, 18, 3, 20, 17},
		{"negative top clamps to 0, cursor visible", -5, 1, 3, 20, 0},
		{"negative top, cursor far down scrolls", -5, 10, 3, 20, 8},
		{"last cursor puts it at bottom of window", 0, 19, 3, 20, 17},
	}
	for _, c := range cases {
		got := adjustViewport(c.top, c.cursor, c.visible, c.total)
		if got != c.want {
			t.Errorf("%s: adjustViewport(%d,%d,%d,%d)=%d, want %d",
				c.name, c.top, c.cursor, c.visible, c.total, got, c.want)
		}
	}
}
