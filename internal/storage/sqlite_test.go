package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/wkirschbaum/whkmail/internal/storage"
	"github.com/wkirschbaum/whkmail/internal/types"
)

func openMemory(t *testing.T) *storage.SQLite {
	t.Helper()
	s, err := storage.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSQLite_FolderRoundTrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	if err := s.UpsertFolder(ctx, types.Folder{Name: "INBOX", Delimiter: "/"}); err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}

	folders, err := s.ListFolders(ctx)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("got %d folders, want 1", len(folders))
	}
	if folders[0].Name != "INBOX" || folders[0].Delimiter != "/" {
		t.Errorf("got %+v", folders[0])
	}
}

func TestSQLite_FolderUpsertUpdates(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX", Delimiter: "/"})
	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX", Delimiter: "."})

	folders, _ := s.ListFolders(ctx)
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder after upsert, got %d", len(folders))
	}
	if folders[0].Delimiter != "." {
		t.Errorf("Delimiter: got %q, want .", folders[0].Delimiter)
	}
}

func TestSQLite_FolderCounts(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX"})
	_, _ = s.UpsertMessage(ctx, types.Message{UID: 1, Folder: "INBOX", Unread: true})
	_, _ = s.UpsertMessage(ctx, types.Message{UID: 2, Folder: "INBOX", Unread: false})
	_, _ = s.UpsertMessage(ctx, types.Message{UID: 3, Folder: "INBOX", Unread: true})

	folders, _ := s.ListFolders(ctx)
	if len(folders) != 1 {
		t.Fatalf("got %d folders, want 1", len(folders))
	}
	if folders[0].MessageCount != 3 {
		t.Errorf("MessageCount: got %d, want 3", folders[0].MessageCount)
	}
	if folders[0].Unread != 2 {
		t.Errorf("Unread: got %d, want 2", folders[0].Unread)
	}
}

func TestSQLite_MessageRoundTrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	m := types.Message{
		UID:     42,
		Folder:  "INBOX",
		Subject: "Hello",
		From:    "alice@example.com",
		To:      "bob@example.com",
		Date:    time.Unix(1700000000, 0).UTC(),
		Unread:  true,
	}
	if _, err := s.UpsertMessage(ctx, m); err != nil {
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

func TestSQLite_UpsertMessage_ReturnsInsertedFlag(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	m := types.Message{UID: 1, Folder: "INBOX", Subject: "first", Unread: true}

	inserted, err := s.UpsertMessage(ctx, m)
	if err != nil {
		t.Fatalf("first UpsertMessage: %v", err)
	}
	if !inserted {
		t.Error("expected inserted=true on first upsert")
	}

	m.Subject = "second"
	inserted, err = s.UpsertMessage(ctx, m)
	if err != nil {
		t.Fatalf("second UpsertMessage: %v", err)
	}
	if inserted {
		t.Error("expected inserted=false on repeat upsert")
	}
}

func TestSQLite_ListMessages_OrderedNewestFirst(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	base := time.Unix(1700000000, 0)
	for i, offset := range []int{0, 100, 50} {
		_, _ = s.UpsertMessage(ctx, types.Message{
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

func TestSQLite_GetMessage_Found(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_, _ = s.UpsertMessage(ctx, types.Message{UID: 7, Folder: "INBOX", Subject: "Hi"})

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

func TestSQLite_GetMessage_NotFound(t *testing.T) {
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

func TestSQLite_ListMessages_EmptyFolder(t *testing.T) {
	s := openMemory(t)
	msgs, err := s.ListMessages(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestSQLite_FolderSync_DefaultsToZero(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX"})

	validity, next, err := s.GetFolderSync(ctx, "INBOX")
	if err != nil {
		t.Fatalf("GetFolderSync: %v", err)
	}
	if validity != 0 || next != 1 {
		t.Errorf("got validity=%d next=%d, want 0/1", validity, next)
	}
}

func TestSQLite_FolderSync_RoundTrip(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX"})
	if err := s.UpdateFolderSync(ctx, "INBOX", 12345, 99); err != nil {
		t.Fatalf("UpdateFolderSync: %v", err)
	}

	validity, next, err := s.GetFolderSync(ctx, "INBOX")
	if err != nil {
		t.Fatalf("GetFolderSync: %v", err)
	}
	if validity != 12345 || next != 99 {
		t.Errorf("got validity=%d next=%d, want 12345/99", validity, next)
	}
}

func TestSQLite_FolderSync_MissingFolder(t *testing.T) {
	s := openMemory(t)
	// GetFolderSync on a folder not in the table returns 0/1 (not an error).
	validity, next, err := s.GetFolderSync(context.Background(), "INBOX")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if validity != 0 || next != 1 {
		t.Errorf("got validity=%d next=%d, want 0/1", validity, next)
	}
}

func TestSQLite_UpsertMessages_BatchInsertsAndUpdates(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX"})

	batch := []types.Message{
		{UID: 1, Folder: "INBOX", Subject: "one", Unread: true},
		{UID: 2, Folder: "INBOX", Subject: "two", Unread: false},
	}
	inserted, err := s.UpsertMessages(ctx, batch)
	if err != nil {
		t.Fatalf("first UpsertMessages: %v", err)
	}
	if len(inserted) != 2 || !inserted[0] || !inserted[1] {
		t.Errorf("expected all inserted, got %v", inserted)
	}

	// Re-run with a mix of existing + new.
	batch2 := []types.Message{
		{UID: 1, Folder: "INBOX", Subject: "one-updated", Unread: false},
		{UID: 3, Folder: "INBOX", Subject: "three", Unread: true},
	}
	inserted, err = s.UpsertMessages(ctx, batch2)
	if err != nil {
		t.Fatalf("second UpsertMessages: %v", err)
	}
	if inserted[0] || !inserted[1] {
		t.Errorf("expected [false, true], got %v", inserted)
	}

	// UID 1 subject + unread should have been updated.
	m, _ := s.GetMessage(ctx, "INBOX", 1)
	if m == nil || m.Subject != "one-updated" || m.Unread {
		t.Errorf("UID 1 not updated: %+v", m)
	}

	// UID 3 should now be there.
	m, _ = s.GetMessage(ctx, "INBOX", 3)
	if m == nil || m.Subject != "three" {
		t.Errorf("UID 3 not inserted: %+v", m)
	}
}

func TestSQLite_UpsertMessages_PreservesBody(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX"})

	// Pre-seed with a body via SetBodyText.
	_, _ = s.UpsertMessages(ctx, []types.Message{{UID: 1, Folder: "INBOX", Subject: "first"}})
	_ = s.SetBodyText(ctx, "INBOX", 1, "cached body")

	// A fresh sync upserts the same UID with empty body — body must survive.
	_, _ = s.UpsertMessages(ctx, []types.Message{{UID: 1, Folder: "INBOX", Subject: "first", Unread: false}})
	m, _ := s.GetMessage(ctx, "INBOX", 1)
	if m == nil || m.BodyText != "cached body" {
		t.Errorf("body lost on re-sync: %+v", m)
	}
}

func TestSQLite_DeleteMessage(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	_ = s.UpsertFolder(ctx, types.Folder{Name: "INBOX"})
	_, _ = s.UpsertMessage(ctx, types.Message{UID: 1, Folder: "INBOX"})
	_, _ = s.UpsertMessage(ctx, types.Message{UID: 2, Folder: "INBOX"})

	if err := s.DeleteMessage(ctx, "INBOX", 1); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	if m, _ := s.GetMessage(ctx, "INBOX", 1); m != nil {
		t.Errorf("UID 1 still present after delete: %+v", m)
	}
	if m, _ := s.GetMessage(ctx, "INBOX", 2); m == nil {
		t.Error("UID 2 was unexpectedly deleted")
	}
}

func TestSQLite_DeleteFolderMessages(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_, _ = s.UpsertMessage(ctx, types.Message{UID: 1, Folder: "INBOX"})
	_, _ = s.UpsertMessage(ctx, types.Message{UID: 2, Folder: "INBOX"})
	_, _ = s.UpsertMessage(ctx, types.Message{UID: 3, Folder: "Sent"})

	if err := s.DeleteFolderMessages(ctx, "INBOX"); err != nil {
		t.Fatalf("DeleteFolderMessages: %v", err)
	}

	inbox, _ := s.ListMessages(ctx, "INBOX", 10)
	if len(inbox) != 0 {
		t.Errorf("expected INBOX empty after delete, got %d messages", len(inbox))
	}
	sent, _ := s.ListMessages(ctx, "Sent", 10)
	if len(sent) != 1 {
		t.Errorf("expected Sent unaffected (1 message), got %d", len(sent))
	}
}
