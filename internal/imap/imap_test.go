package imap

import (
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func TestAddressString_WithName(t *testing.T) {
	a := goimap.Address{Name: "Alice Smith", Mailbox: "alice", Host: "example.com"}
	got := addressString(a)
	want := "Alice Smith <alice@example.com>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAddressString_WithoutName(t *testing.T) {
	a := goimap.Address{Mailbox: "bob", Host: "example.com"}
	got := addressString(a)
	want := "bob@example.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAddressString_WhitespaceName(t *testing.T) {
	a := goimap.Address{Name: "  ", Mailbox: "carol", Host: "example.com"}
	got := addressString(a)
	want := "carol@example.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestContainsFlag(t *testing.T) {
	flags := []goimap.Flag{goimap.FlagSeen, goimap.FlagFlagged}

	if !containsFlag(flags, goimap.FlagSeen) {
		t.Error("expected FlagSeen to be found")
	}
	if containsFlag(flags, goimap.FlagDeleted) {
		t.Error("expected FlagDeleted not to be found")
	}
	if containsFlag(nil, goimap.FlagSeen) {
		t.Error("expected no match on nil slice")
	}
}

func TestMessageFromBuffer_WithEnvelope(t *testing.T) {
	date := time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC)
	buf := &imapclient.FetchMessageBuffer{
		UID:   goimap.UID(7),
		Flags: []goimap.Flag{goimap.FlagFlagged},
		Envelope: &goimap.Envelope{
			Subject: "Test subject",
			Date:    date,
			From:    []goimap.Address{{Name: "Alice", Mailbox: "alice", Host: "x.com"}},
			To:      []goimap.Address{{Mailbox: "bob", Host: "y.com"}},
		},
	}
	m := messageFromBuffer("INBOX", buf)

	if m.UID != 7 {
		t.Errorf("UID: got %d, want 7", m.UID)
	}
	if m.Folder != "INBOX" {
		t.Errorf("Folder: got %q, want INBOX", m.Folder)
	}
	if m.Subject != "Test subject" {
		t.Errorf("Subject: got %q", m.Subject)
	}
	if m.From != "Alice <alice@x.com>" {
		t.Errorf("From: got %q", m.From)
	}
	if m.To != "bob@y.com" {
		t.Errorf("To: got %q", m.To)
	}
	if !m.Date.Equal(date) {
		t.Errorf("Date: got %v, want %v", m.Date, date)
	}
	if !m.Unread {
		t.Error("expected Unread=true (FlagSeen absent → message is unread)")
	}
	if !m.Flagged {
		t.Error("expected Flagged=true")
	}
}

func TestMessageFromBuffer_Seen(t *testing.T) {
	buf := &imapclient.FetchMessageBuffer{
		Flags: []goimap.Flag{goimap.FlagSeen},
	}
	m := messageFromBuffer("INBOX", buf)
	if m.Unread {
		t.Error("expected Unread=false when FlagSeen is set")
	}
}

func TestMessageFromBuffer_NoEnvelope(t *testing.T) {
	buf := &imapclient.FetchMessageBuffer{UID: goimap.UID(1)}
	m := messageFromBuffer("Sent", buf)
	if m.Folder != "Sent" {
		t.Errorf("Folder: got %q", m.Folder)
	}
	if m.Subject != "" || m.From != "" {
		t.Error("expected empty subject and from with no envelope")
	}
}
