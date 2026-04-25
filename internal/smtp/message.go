package smtp

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
)

// Message is the domain type the TUI / daemon hand to Send. Headers are
// typed fields (not a raw RFC 5322 block) so the sender can validate
// recipients, normalise threading headers, and generate a Message-ID
// consistently.
type Message struct {
	From    string
	To      []string
	Cc      []string
	Subject string
	Body    string

	// Threading headers. Set InReplyTo to the original Message-ID when
	// replying; References is the chain from the thread root to the
	// immediate parent. Both are optional for fresh-start messages.
	InReplyTo  string
	References []string

	// Date defaults to time.Now() when zero.
	Date time.Time

	// MessageID defaults to a generated value when empty. Exposed so
	// tests can pin the output.
	MessageID string
}

// Recipients returns every address the SMTP envelope needs (RCPT TO
// lines). Order: To first, then Cc. Bcc isn't modelled here — when it's
// added it'll extend this slice but must not appear in the rendered
// headers.
func (m Message) Recipients() []string {
	out := make([]string, 0, len(m.To)+len(m.Cc))
	out = append(out, m.To...)
	out = append(out, m.Cc...)
	return out
}

// RFC5322 encodes the message into a wire-format RFC 5322 block ready
// for DATA. Pure function — no clock reads, no network, no globals —
// so tests can round-trip an exact expected output.
func (m Message) RFC5322() string {
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	msgID := m.MessageID
	if msgID == "" {
		msgID = generateMessageID(m.From, date)
	}

	var b strings.Builder
	writeHeader(&b, "From", m.From)
	if len(m.To) > 0 {
		writeHeader(&b, "To", strings.Join(m.To, ", "))
	}
	if len(m.Cc) > 0 {
		writeHeader(&b, "Cc", strings.Join(m.Cc, ", "))
	}
	writeHeader(&b, "Subject", m.Subject)
	writeHeader(&b, "Date", date.Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", msgID)
	if m.InReplyTo != "" {
		writeHeader(&b, "In-Reply-To", m.InReplyTo)
	}
	if len(m.References) > 0 {
		writeHeader(&b, "References", strings.Join(m.References, " "))
	}
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", "text/plain; charset=UTF-8")
	writeHeader(&b, "Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	// Normalise body line endings to CRLF — SMTP DATA requires it and
	// some relays reject bare LF.
	b.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n"))
	return b.String()
}

func writeHeader(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\r\n")
}

// generateMessageID returns a new RFC 5322 Message-ID. It combines the
// Unix timestamp with 8 random bytes so IDs are globally unique even
// when two messages are sent within the same nanosecond.
func generateMessageID(from string, t time.Time) string {
	domain := "local"
	if at := strings.LastIndex(from, "@"); at >= 0 && at < len(from)-1 {
		domain = strings.TrimSuffix(from[at+1:], ">")
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		// /dev/random failure is extremely unlikely; fall back to nanoseconds.
		return fmt.Sprintf("<%d.%d@%s>", t.Unix(), t.UnixNano(), domain)
	}
	return fmt.Sprintf("<%d.%x@%s>", t.Unix(), rnd, domain)
}
