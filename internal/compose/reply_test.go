package compose_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wkirschbaum/whkmail/internal/compose"
	"github.com/wkirschbaum/whkmail/internal/smtp"
	"github.com/wkirschbaum/whkmail/internal/types"
)

func sampleThread() types.Message {
	return types.Message{
		UID:       42,
		Folder:    "INBOX",
		Subject:   "Lunch?",
		From:      "Alice <alice@example.com>",
		To:        "Me <me@example.com>, bob@example.com",
		Date:      time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		MessageID: "<thread-root@example.com>",
		BodyText:  "Want to grab lunch?\nAround 1?",
	}
}

func TestBuildReplyAll_PopulatesEveryoneButSelf(t *testing.T) {
	m := compose.BuildReplyAll(sampleThread(), "me@example.com")
	if len(m.To) != 1 || m.To[0] != "alice@example.com" {
		t.Errorf("To wrong: %+v", m.To)
	}
	// Cc should contain bob but not me.
	if len(m.Cc) != 1 || m.Cc[0] != "bob@example.com" {
		t.Errorf("Cc wrong: %+v", m.Cc)
	}
}

func TestBuildReplySender_OnlyToOriginalFrom(t *testing.T) {
	m := compose.BuildReplySender(sampleThread(), "me@example.com")
	if len(m.To) != 1 || m.To[0] != "alice@example.com" {
		t.Errorf("To wrong: %+v", m.To)
	}
	if len(m.Cc) != 0 {
		t.Errorf("Cc should be empty, got %+v", m.Cc)
	}
}

func TestReplyAll_DropsSelfFromTo(t *testing.T) {
	// Self might appear in the original To (the whole point — a reply-
	// all shouldn't send the reply back to us).
	orig := sampleThread()
	orig.To = "me@example.com, bob@example.com, carol@example.com"
	m := compose.BuildReplyAll(orig, "me@example.com")

	for _, addr := range m.Cc {
		if strings.EqualFold(addr, "me@example.com") {
			t.Errorf("reply-all looped the reply back to self: %+v", m.Cc)
		}
	}
	if len(m.Cc) != 2 {
		t.Errorf("expected 2 Cc entries (bob, carol), got %+v", m.Cc)
	}
}

func TestReplyAll_DropsDuplicateAddresses(t *testing.T) {
	// If the original From is also in the To list, we should see the
	// address once (as To) and not again in Cc.
	orig := sampleThread()
	orig.To = "alice@example.com, bob@example.com"
	m := compose.BuildReplyAll(orig, "me@example.com")

	for _, addr := range m.Cc {
		if strings.EqualFold(addr, "alice@example.com") {
			t.Errorf("alice appeared in both To and Cc: %+v", m)
		}
	}
}

func TestReplyAll_StripsSelfEvenIfSenderIsSelf(t *testing.T) {
	// Edge case: replying to a message we sent ourselves (e.g. from
	// Drafts). Shouldn't produce a To: self draft.
	orig := sampleThread()
	orig.From = "me@example.com"
	m := compose.BuildReplyAll(orig, "me@example.com")
	if len(m.To) != 0 {
		t.Errorf("To should be empty when original sender is self, got %+v", m.To)
	}
}

func TestPrefixSubject_IdempotentOnExistingRe(t *testing.T) {
	cases := map[string]string{
		"Lunch?":     "Re: Lunch?",
		"Re: Lunch?": "Re: Lunch?",
		"RE: Lunch?": "RE: Lunch?",
		"re: lunch":  "re: lunch",
		"  Lunch?  ": "Re: Lunch?", // trimmed
		"":           "Re:",
	}
	for in, want := range cases {
		m := types.Message{Subject: in, Date: time.Now(), From: "a@ex"}
		got := compose.BuildReplyAll(m, "me@ex").Subject
		if got != want {
			t.Errorf("subject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteBody_FormatsAttributionAndQuoting(t *testing.T) {
	m := compose.BuildReplyAll(sampleThread(), "me@example.com")
	// Attribution line present.
	if !strings.Contains(m.Body, "Alice wrote:") {
		t.Errorf("attribution missing: %q", m.Body)
	}
	// Both body lines quoted.
	if !strings.Contains(m.Body, "> Want to grab lunch?") || !strings.Contains(m.Body, "> Around 1?") {
		t.Errorf("body not quoted properly: %q", m.Body)
	}
}

func TestBuildReply_ThreadingHeaders(t *testing.T) {
	orig := sampleThread()
	orig.InReplyTo = "<older-parent@example.com>"
	m := compose.BuildReplyAll(orig, "me@example.com")

	if m.InReplyTo != "<thread-root@example.com>" {
		t.Errorf("InReplyTo = %q, want <thread-root@example.com>", m.InReplyTo)
	}
	if len(m.References) != 2 {
		t.Fatalf("References len = %d, want 2: %+v", len(m.References), m.References)
	}
	// Order matters: grandparent first, parent last.
	if m.References[0] != "<older-parent@example.com>" {
		t.Errorf("References[0] = %q", m.References[0])
	}
	if m.References[1] != "<thread-root@example.com>" {
		t.Errorf("References[1] = %q", m.References[1])
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		msg     smtp.Message
		wantErr bool
	}{
		{"ok", smtp.Message{To: []string{"a@ex"}, Subject: "Hi"}, false},
		{"only cc", smtp.Message{Cc: []string{"a@ex"}, Subject: "Hi"}, false},
		{"no recipients", smtp.Message{Subject: "Hi"}, true},
		{"no subject", smtp.Message{To: []string{"a@ex"}}, true},
		{"whitespace subject", smtp.Message{To: []string{"a@ex"}, Subject: "   "}, true},
	}
	for _, c := range cases {
		err := compose.Validate(c.msg)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: got err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}
