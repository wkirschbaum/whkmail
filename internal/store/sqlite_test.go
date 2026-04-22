package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/wkirschbaum/whkmail/internal/store"
	"github.com/wkirschbaum/whkmail/internal/types"
)

func openMemory(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_FolderRoundTrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	f := types.Folder{Name: "INBOX", Delimiter: "/", MessageCount: 10, Unread: 3}
	if err := s.UpsertFolder(ctx, f); err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}

	folders, err := s.ListFolders(ctx)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("got %d folders, want 1", len(folders))
	}
	if folders[0] != f {
		t.Errorf("got %+v, want %+v", folders[0], f)
	}
}

func TestStore_FolderUpsertUpdates(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX", Unread: 1})
	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX", Unread: 5})

	folders, _ := s.ListFolders(ctx)
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder after upsert, got %d", len(folders))
	}
	if folders[0].Unread != 5 {
		t.Errorf("Unread: got %d, want 5", folders[0].Unread)
	}
}

func TestStore_MessageRoundTrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	m := types.Message{
		UID:    42,
		Folder: "INBOX",
		Subject: "Hello",
		From:   "alice@example.com",
		To:     "bob@example.com",
		Date:   time.Unix(1700000000, 0).UTC(),
		Unread: true,
	}
	if err := s.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	msgs, err := s.ListMessages(ctx, "INBOX", 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	got := msgs[0]
	if got.UID != m.UID || got.Subject != m.Subject || got.From != m.From || got.Unread != m.Unread {
		t.Errorf("got %+v, want %+v", got, m)
	}
}

func TestStore_ListMessages_OrderedNewestFirst(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	base := time.Unix(1700000000, 0)
	for i, offset := range []int{0, 100, 50} {
		_ = s.UpsertMessage(ctx, types.Message{
			UID:    uint32(i + 1),
			Folder: "INBOX",
			Date:   base.Add(time.Duration(offset) * time.Second),
		})
	}

	msgs, _ := s.ListMessages(ctx, "INBOX", 10)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	if msgs[0].UID != 2 { // offset 100 is newest
		t.Errorf("expected UID 2 first (newest), got %d", msgs[0].UID)
	}
}

func TestStore_GetMessage_Found(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_ = s.UpsertMessage(ctx, types.Message{UID: 7, Folder: "INBOX", Subject: "Hi"})

	msg, err := s.GetMessage(ctx, "INBOX", 7)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message, got nil")
	}
	if msg.Subject != "Hi" {
		t.Errorf("Subject: got %q, want Hi", msg.Subject)
	}
}

func TestStore_GetMessage_NotFound(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	msg, err := s.GetMessage(ctx, "INBOX", 999)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg != nil {
		t.Errorf("expected nil for missing message, got %+v", msg)
	}
}

func TestStore_ListMessages_EmptyFolder(t *testing.T) {
	s := openMemory(t)
	msgs, err := s.ListMessages(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}
