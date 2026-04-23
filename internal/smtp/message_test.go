package smtp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wkirschbaum/whkmail/internal/smtp"
)

func TestMessage_Recipients_MergesToAndCc(t *testing.T) {
	m := smtp.Message{
		To: []string{"a@ex", "b@ex"},
		Cc: []string{"c@ex"},
	}
	got := m.Recipients()
	if len(got) != 3 || got[0] != "a@ex" || got[2] != "c@ex" {
		t.Errorf("recipients: %+v", got)
	}
}

func TestMessage_RFC5322_IncludesAllHeaders(t *testing.T) {
	m := smtp.Message{
		From:       "me@ex.com",
		To:         []string{"a@ex.com"},
		Cc:         []string{"b@ex.com"},
		Subject:    "Re: Hi",
		Body:       "Hello there\nSecond line",
		InReplyTo:  "<parent@ex.com>",
		References: []string{"<root@ex.com>", "<parent@ex.com>"},
		Date:       time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		MessageID:  "<fixed@ex.com>",
	}
	out := m.RFC5322()

	for _, want := range []string{
		"From: me@ex.com\r\n",
		"To: a@ex.com\r\n",
		"Cc: b@ex.com\r\n",
		"Subject: Re: Hi\r\n",
		"Message-ID: <fixed@ex.com>\r\n",
		"In-Reply-To: <parent@ex.com>\r\n",
		"References: <root@ex.com> <parent@ex.com>\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
		"\r\nHello there\r\nSecond line", // body with CRLF normalised
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMessage_RFC5322_NoCcWhenEmpty(t *testing.T) {
	m := smtp.Message{
		From:      "me@ex.com",
		To:        []string{"a@ex.com"},
		Subject:   "Hi",
		Date:      time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		MessageID: "<x@ex.com>",
	}
	out := m.RFC5322()
	if strings.Contains(out, "Cc:") {
		t.Errorf("unexpected Cc header:\n%s", out)
	}
}

func TestMessage_RFC5322_GeneratesMessageID(t *testing.T) {
	m := smtp.Message{
		From:    "me@ex.com",
		To:      []string{"a@ex.com"},
		Subject: "Hi",
		Date:    time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
	}
	out := m.RFC5322()
	if !strings.Contains(out, "Message-ID: <") {
		t.Errorf("Message-ID header missing:\n%s", out)
	}
	if !strings.Contains(out, "@ex.com>") {
		t.Errorf("Message-ID should use sender domain:\n%s", out)
	}
}
