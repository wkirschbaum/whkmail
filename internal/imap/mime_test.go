package imap

import (
	"strings"
	"testing"
)

func TestExtractText_PlainText(t *testing.T) {
	raw := "From: alice@example.com\r\nContent-Type: text/plain\r\n\r\nHello, world!\r\n"
	got := extractText([]byte(raw))
	if got != "Hello, world!" {
		t.Errorf("got %q, want %q", got, "Hello, world!")
	}
}

func TestExtractText_HTMLOnly(t *testing.T) {
	raw := "From: alice@example.com\r\nContent-Type: text/html\r\n\r\n<p>Hello</p>\r\n"
	got := extractText([]byte(raw))
	if !strings.Contains(got, "Hello") {
		t.Errorf("got %q, want it to contain 'Hello'", got)
	}
}

func TestExtractText_MultipartAlternative(t *testing.T) {
	raw := strings.Join([]string{
		"From: alice@example.com",
		`Content-Type: multipart/alternative; boundary="bound"`,
		"",
		"--bound",
		"Content-Type: text/plain",
		"",
		"Plain text body",
		"--bound",
		"Content-Type: text/html",
		"",
		"<p>HTML body</p>",
		"--bound--",
		"",
	}, "\r\n")
	got := extractText([]byte(raw))
	if got != "Plain text body" {
		t.Errorf("got %q, want plain text preferred over html", got)
	}
}

func TestExtractText_Empty(t *testing.T) {
	got := extractText([]byte{})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
