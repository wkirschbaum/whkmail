package tui

import (
	"strings"
	"testing"
	"time"

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
		account:    "a@b",
		folder:     "INBOX",
		prefetched: make(map[prefetchKey]bool),
		bodyErr:    make(map[prefetchKey]string),
	}
}

func TestHandleEvent_BodyFailure_StoresError(t *testing.T) {
	m := newTestModel()
	m.view = viewMessage
	m.message = &types.Message{UID: 1, Folder: "INBOX"}

	m, cmd := m.handleEvent(events.Event{
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

	m, cmd := m.handleEvent(events.Event{
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
	got := mm

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
	if got.modal != nil {
		t.Errorf("unexpected modal: %T", got.modal)
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
	got := mm

	cm, ok := got.modal.(confirmModal)
	if !ok {
		t.Fatalf("expected confirmModal, got %T", got.modal)
	}
	if cm.prompt == "" {
		t.Error("expected a non-empty prompt")
	}
	if cm.onConfirm == nil {
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
	m = mm

	// Press y to confirm.
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := mm

	if got.modal != nil {
		t.Errorf("modal should clear after confirm, got %T", got.modal)
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
	m = mm

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got := mm

	if got.modal != nil {
		t.Errorf("modal should clear on cancel, got %T", got.modal)
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
	if mm.cursor != 1 {
		t.Errorf("space: cursor = %d, want 1", mm.cursor)
	}
}

// stylePickerCursor is a small test helper that fishes the cursor out of
// whatever modal is currently open. Keeps popup-state assertions
// readable without spreading the type assertion through every test.
func stylePickerCursor(t *testing.T, m Model) int {
	t.Helper()
	p, ok := m.modal.(stylePickerModal)
	if !ok {
		t.Fatalf("expected stylePickerModal, got %T", m.modal)
	}
	return p.cursor
}

// setStylePickerCursor mutates the open picker's cursor. Necessary because
// the cursor lives on the modal value, not on Model — tests that want to
// simulate a specific selection need to replace the modal.
func setStylePickerCursor(t *testing.T, m Model, cursor int) Model {
	t.Helper()
	if _, ok := m.modal.(stylePickerModal); !ok {
		t.Fatalf("expected stylePickerModal, got %T", m.modal)
	}
	m.modal = stylePickerModal{cursor: cursor}
	return m
}

func TestHandleKey_CommaOpensStylePicker(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.style = StyleVim

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	got := mm

	if _, ok := got.modal.(stylePickerModal); !ok {
		t.Errorf("expected stylePickerModal after pressing ,, got %T", got.modal)
	}
	if c := stylePickerCursor(t, got); c != 0 {
		t.Errorf("cursor should start on the active style (vim=0), got %d", c)
	}
	if cmd != nil {
		t.Error("opening the picker must not dispatch a command")
	}
}

func TestHandleKey_CommaOpensStylePicker_StartsOnActiveStyle(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.style = StyleEmacs

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	if c := stylePickerCursor(t, mm); c != 1 {
		t.Errorf("cursor should start on emacs (=1), got %d", c)
	}
}

func TestHandleConfigKey_JKMovesCursor(t *testing.T) {
	m := newTestModel()
	m = m.openStylePicker()

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if got := stylePickerCursor(t, mm); got != 1 {
		t.Errorf("j: cursor = %d, want 1", got)
	}

	mm2, _ := mm.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if got := stylePickerCursor(t, mm2); got != 0 {
		t.Errorf("k: cursor = %d, want 0", got)
	}
}

func TestHandleConfigKey_EnterAppliesAndPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel()
	m.style = StyleVim
	m = m.openStylePicker()
	m = setStylePickerCursor(t, m, 1) // emacs

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm

	if got.modal != nil {
		t.Errorf("modal should be nil after enter, got %T", got.modal)
	}
	if got.style != StyleEmacs {
		t.Errorf("style = %q, want %q", got.style, StyleEmacs)
	}
	if cmd == nil {
		t.Fatal("expected a persist cmd on style change")
	}
	// Run the cmd and verify the file actually lands on disk.
	if msg := cmd(); msg != nil {
		t.Errorf("saveStyleCmd returned non-nil msg: %+v", msg)
	}
	if loaded := LoadInputStyle(); loaded != StyleEmacs {
		t.Errorf("persisted style = %q, want emacs", loaded)
	}
}

func TestHandleConfigKey_EnterNoChangeSkipsSave(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel()
	m.style = StyleVim
	m = m.openStylePicker() // cursor starts on vim

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm

	if got.modal != nil {
		t.Errorf("modal should be nil, got %T", got.modal)
	}
	if cmd != nil {
		t.Error("no-change enter should not dispatch a save cmd")
	}
}

func TestHandleConfigKey_EscCancelsWithoutPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel()
	m.style = StyleVim
	m = m.openStylePicker()
	m = setStylePickerCursor(t, m, 1) // about to pick emacs…

	// …but press esc instead.
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := mm

	if got.modal != nil {
		t.Errorf("esc should close the popup, got %T", got.modal)
	}
	if got.style != StyleVim {
		t.Errorf("style must stay vim after cancel, got %q", got.style)
	}
	if cmd != nil {
		t.Error("cancel must not dispatch a command")
	}
	if loaded := LoadInputStyle(); loaded != StyleVim {
		t.Errorf("no file should have been written; loaded = %q", loaded)
	}
}

func TestHandleConfigKey_SwallowsUnrelatedKeys(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	m.messages = []types.Message{{UID: 1, Folder: "INBOX"}}
	m = m.openStylePicker()

	// 'd' outside the popup would trigger trash. Inside, it should no-op.
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := mm

	if _, ok := got.modal.(stylePickerModal); !ok {
		t.Errorf("popup should remain open, got %T", got.modal)
	}
	if cmd != nil {
		t.Error("unrelated keys must not dispatch commands while popup is open")
	}
	if len(got.messages) != 1 {
		t.Errorf("the underlying trash handler must not fire, got %d messages", len(got.messages))
	}
}

func TestHandleKey_CtrlDQuits(t *testing.T) {
	m := newTestModel()
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("ctrl+d must dispatch tea.Quit")
	}
}

func TestHandleKey_ReplyAll_InViewMessage(t *testing.T) {
	m := newTestModel()
	m.view = viewMessage
	m.account = "me@example.com"
	m.message = &types.Message{
		UID: 1, Folder: "INBOX", Subject: "Hi",
		From: "alice@example.com", To: "me@example.com, bob@example.com",
		Date: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
	}
	m.height = 30
	m.width = 80

	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if mm.compose == nil {
		t.Fatal("expected compose pane to open")
	}
	if len(mm.compose.draft.To) != 1 || mm.compose.draft.To[0] != "alice@example.com" {
		t.Errorf("To wrong: %+v", mm.compose.draft.To)
	}
	if len(mm.compose.draft.Cc) != 1 || mm.compose.draft.Cc[0] != "bob@example.com" {
		t.Errorf("Cc wrong: %+v", mm.compose.draft.Cc)
	}
	if cmd != nil {
		t.Error("opening compose must not dispatch a command")
	}
}

func TestHandleKey_ReplySender_DropsOtherRecipients(t *testing.T) {
	m := newTestModel()
	m.view = viewMessage
	m.account = "me@example.com"
	m.message = &types.Message{
		UID: 1, Subject: "Hi",
		From: "alice@example.com", To: "me@example.com, bob@example.com",
		Date: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
	}
	m.height = 30
	m.width = 80

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if mm.compose == nil {
		t.Fatal("expected compose pane to open")
	}
	if len(mm.compose.draft.To) != 1 || mm.compose.draft.To[0] != "alice@example.com" {
		t.Errorf("To wrong: %+v", mm.compose.draft.To)
	}
	if len(mm.compose.draft.Cc) != 0 {
		t.Errorf("Cc should be empty on reply-sender, got %+v", mm.compose.draft.Cc)
	}
}

func TestHandleKey_Reply_IgnoredOutsideDetailView(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if mm.compose != nil {
		t.Error("reply must not open outside viewMessage")
	}
}

func TestComposeKey_EscCancels(t *testing.T) {
	m := newTestModel()
	m.view = viewMessage
	m.account = "me@example.com"
	m.message = &types.Message{Subject: "Hi", From: "alice@example.com"}
	m.height = 30
	m.width = 80

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if mm.compose == nil {
		t.Fatal("compose should be open")
	}
	mm2, cmd := mm.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if mm2.compose != nil {
		t.Error("esc should close compose")
	}
	if cmd != nil {
		t.Error("esc must not dispatch a command")
	}
}

func TestHandleKey_QDoesNotQuit(t *testing.T) {
	m := newTestModel()
	m.view = viewMessages
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Errorf("q should no longer quit, got cmd = %T", cmd)
	}
}

func TestHandleEvent_NewMessage_AccumulatesAndArmsTick(t *testing.T) {
	m := newTestModel()
	m.account = "a@b"

	m, cmd := m.handleEvent(events.Event{
		Kind: events.KindNewMessage, Account: "a@b",
		Folder: "INBOX", UID: 1, Subject: "Hi", From: "alice@example.com",
	})
	if cmd == nil {
		t.Fatal("KindNewMessage must return a debounce tick")
	}
	if len(m.flash.pending) != 1 {
		t.Errorf("flash.pending len = %d, want 1", len(m.flash.pending))
	}
	if m.flash.gen != 1 {
		t.Errorf("flash.gen = %d, want 1", m.flash.gen)
	}

	// Second burst bumps gen and appends.
	m, _ = m.handleEvent(events.Event{
		Kind: events.KindNewMessage, Account: "a@b",
		Subject: "Two", From: "bob@example.com",
	})
	if len(m.flash.pending) != 2 {
		t.Errorf("after second event, flash.pending len = %d, want 2", len(m.flash.pending))
	}
	if m.flash.gen != 2 {
		t.Errorf("after second event, flash.gen = %d, want 2", m.flash.gen)
	}
}

func TestHandleEvent_NewMessage_IgnoresOtherAccount(t *testing.T) {
	m := newTestModel()
	m.account = "a@b"
	m, cmd := m.handleEvent(events.Event{
		Kind: events.KindNewMessage, Account: "other@b",
		Subject: "Hi", From: "x",
	})
	if cmd != nil {
		t.Error("events for other accounts must not arm the debounce")
	}
	if len(m.flash.pending) != 0 {
		t.Errorf("flash.pending should stay empty, got %d", len(m.flash.pending))
	}
}

func TestHandleEvent_SyncStarted_SetsFlag(t *testing.T) {
	m := newTestModel()
	m, _ = m.handleEvent(events.Event{Kind: events.KindSyncStarted, Account: "a@b"})
	if !m.sync.active {
		t.Error("KindSyncStarted must flip sync.active=true")
	}
}

func TestHandleEvent_SyncProgress_PopulatesFolder(t *testing.T) {
	m := newTestModel()
	m, _ = m.handleEvent(events.Event{
		Kind: events.KindSyncProgress, Folder: "INBOX", Current: 3, Total: 12,
	})
	if !m.sync.active {
		t.Error("progress must imply active sync")
	}
	if m.sync.folder != "INBOX" || m.sync.current != 3 || m.sync.total != 12 {
		t.Errorf("sync detail wrong: %+v", m.sync)
	}
}

func TestHandleEvent_SyncDone_ClearsFlagAndFetches(t *testing.T) {
	m := newTestModel()
	m.sync.active = true
	m.view = viewMessages
	m.folder = "INBOX"

	m, cmd := m.handleEvent(events.Event{Kind: events.KindSyncDone, Account: "a@b"})
	if m.sync.active {
		t.Error("KindSyncDone must flip sync.active=false")
	}
	if cmd == nil {
		t.Fatal("KindSyncDone in viewMessages should batch status + messages refresh")
	}
}

func TestMsgFlash_LatestGenCommitsPending(t *testing.T) {
	m := newTestModel()
	m.flash.gen = 3
	m.flash.pending = []flashEntry{
		{subject: "Hello", from: "alice"},
	}
	mm, _ := m.Update(msgFlash{gen: 3})
	got := mm.(Model)
	if got.flash.text == "" {
		t.Error("expected flash to be committed")
	}
	if len(got.flash.pending) != 0 {
		t.Errorf("pending should be drained, got %d", len(got.flash.pending))
	}
}

func TestMsgFlash_StaleGenIgnored(t *testing.T) {
	m := newTestModel()
	m.flash.gen = 5
	m.flash.pending = []flashEntry{{subject: "x", from: "y"}}
	mm, _ := m.Update(msgFlash{gen: 3})
	got := mm.(Model)
	if got.flash.text != "" {
		t.Errorf("stale tick must not commit, flash = %q", got.flash.text)
	}
	if len(got.flash.pending) != 1 {
		t.Error("stale tick must not drain pending")
	}
}

func TestFormatFlash(t *testing.T) {
	if got := formatFlash(nil); got != "" {
		t.Errorf("empty = %q, want empty", got)
	}
	single := formatFlash([]flashEntry{{subject: "Hi", from: "alice"}})
	if single != "New: Hi — alice" {
		t.Errorf("single = %q", single)
	}
	many := formatFlash([]flashEntry{
		{subject: "a", from: "x"},
		{subject: "b", from: "y"},
		{subject: "c", from: "z"},
	})
	if !strings.Contains(many, "3 new") || !strings.Contains(many, "c") {
		t.Errorf("many = %q", many)
	}
}

func TestTotalUnread(t *testing.T) {
	m := Model{
		accounts: []types.AccountStatus{
			{Folders: []types.Folder{{Unread: 3}, {Unread: 5}}},
			{Folders: []types.Folder{{Unread: 2}}},
		},
	}
	if got := m.totalUnread(); got != 10 {
		t.Errorf("totalUnread = %d, want 10", got)
	}
	if empty := (Model{}).totalUnread(); empty != 0 {
		t.Errorf("empty totalUnread = %d, want 0", empty)
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
