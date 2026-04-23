package tui

import (
	"os"
	"testing"

	"github.com/wkirschbaum/whkmail/internal/types"
)

func TestDraft_RoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	want := types.SendRequest{
		To:           []string{"a@ex.com"},
		Cc:           []string{"b@ex.com"},
		Subject:      "Re: Hi",
		Body:         "draft body\nline two",
		InReplyTo:    "<parent@ex.com>",
		References:   []string{"<root@ex.com>"},
		SourceFolder: "INBOX",
	}

	if err := saveDraft("me@ex.com", "key-1", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadDraft("me@ex.com", "key-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil {
		t.Fatal("loadDraft returned nil for a draft that was just written")
	}
	if got.Body != want.Body || got.Subject != want.Subject {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
	if got.SourceFolder != "INBOX" {
		t.Errorf("SourceFolder lost in round-trip: %q", got.SourceFolder)
	}
}

func TestDraft_LoadMissing_IsNotAnError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	got, err := loadDraft("me@ex.com", "nope")
	if err != nil {
		t.Errorf("missing file should return (nil, nil), got err=%v", err)
	}
	if got != nil {
		t.Errorf("missing file should return nil draft, got %+v", got)
	}
}

func TestDraft_Delete(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := saveDraft("me@ex.com", "key-1", types.SendRequest{Subject: "Hi"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := deleteDraft("me@ex.com", "key-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Second delete must succeed (idempotent).
	if err := deleteDraft("me@ex.com", "key-1"); err != nil {
		t.Errorf("double-delete should be idempotent, got %v", err)
	}
	if _, err := os.Stat(draftPath("me@ex.com", "key-1")); !os.IsNotExist(err) {
		t.Errorf("file should be gone, stat err = %v", err)
	}
}

func TestDraftKey_StableAcrossCalls(t *testing.T) {
	orig := types.Message{MessageID: "<parent@ex.com>", Subject: "Hi", From: "a@ex"}
	if draftKey(orig) != draftKey(orig) {
		t.Error("draftKey should be deterministic for the same message")
	}
}

func TestDraftKey_DifferentByMessageID(t *testing.T) {
	a := types.Message{MessageID: "<one@ex.com>"}
	b := types.Message{MessageID: "<two@ex.com>"}
	if draftKey(a) == draftKey(b) {
		t.Error("distinct message IDs should produce distinct keys")
	}
}

func TestDraftKey_FallsBackWhenNoMessageID(t *testing.T) {
	a := types.Message{Subject: "Hi", From: "a@ex"}
	b := types.Message{Subject: "Bye", From: "a@ex"}
	if draftKey(a) == draftKey(b) {
		t.Error("fallback key should differ by subject")
	}
}
