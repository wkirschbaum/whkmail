// Package compose contains the pure logic for turning a received
// message into a reply draft: recipient selection (reply-to-sender vs
// reply-all), subject prefixing, body quoting, and threading headers.
// No I/O, no bubbletea, no SMTP — those live at the edges. The TUI
// calls BuildReply/BuildReplyAll at key-press time; the resulting
// smtp.Message flows to the daemon for transport.
package compose

import (
	"fmt"
	"strings"
	"time"

	"github.com/wkirschbaum/whkmail/internal/smtp"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// Mode is the reply flavour. ReplyAll is the default — most user
// responses go to every participant, and a narrower sender-only reply
// is the explicit escape hatch.
type Mode int

const (
	ReplyAll Mode = iota
	ReplySender
)

// Build turns an original message + our own address into a draft reply.
// Addresses matching self (case-insensitive, envelope-stripped) are
// filtered out of To / Cc so we don't reply to ourselves.
//
// For Mode=ReplySender, To is just the original From.
// For Mode=ReplyAll, To = {original From}, Cc = {original Tos + Ccs}.
// In both cases duplicates and self-addresses are removed.
func Build(mode Mode, orig types.Message, self string) smtp.Message {
	fromAddr := extractAddress(orig.From)

	var to, cc []string
	switch mode {
	case ReplySender:
		if fromAddr != "" && !sameAddress(fromAddr, self) {
			to = []string{fromAddr}
		}
	default: // ReplyAll
		if fromAddr != "" && !sameAddress(fromAddr, self) {
			to = append(to, fromAddr)
		}
		// The original To/Cc may contain multiple addresses in a single
		// field — RFC 5322 joins them with commas. splitAddresses
		// handles both "a@ex" and "a@ex, b@ex".
		for _, raw := range splitAddresses(orig.To) {
			addr := extractAddress(raw)
			if addr == "" || sameAddress(addr, self) {
				continue
			}
			if containsAddress(to, addr) {
				continue
			}
			cc = append(cc, addr)
		}
	}

	subject := prefixSubject(orig.Subject)
	body := quoteBody(orig)
	references := buildReferences(orig)

	return smtp.Message{
		From:       self,
		To:         to,
		Cc:         cc,
		Subject:    subject,
		Body:       body,
		InReplyTo:  orig.MessageID,
		References: references,
	}
}

// BuildReplyAll is a convenience wrapper matching the default UX.
func BuildReplyAll(orig types.Message, self string) smtp.Message {
	return Build(ReplyAll, orig, self)
}

// BuildReplySender replies only to the original sender.
func BuildReplySender(orig types.Message, self string) smtp.Message {
	return Build(ReplySender, orig, self)
}

// prefixSubject adds "Re: " if the subject doesn't already start with
// one (case-insensitive). Handles "Re:", "RE:", "re: " uniformly so a
// chain doesn't accumulate "Re: Re: Re: ".
func prefixSubject(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return "Re:"
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "re:") {
		return s
	}
	return "Re: " + s
}

// quoteBody renders the classic "On <date>, <who> wrote:" attribution
// line followed by the original body prefixed with "> ". Handles empty
// bodies (we still emit the attribution — the user can delete it if
// they want a clean reply).
func quoteBody(orig types.Message) string {
	attribution := "On " + orig.Date.Format("Mon, 02 Jan 2006 at 15:04") + ", " +
		displayName(orig.From) + " wrote:"

	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(attribution)
	b.WriteString("\n")
	for _, line := range strings.Split(strings.TrimRight(orig.BodyText, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// buildReferences extends the original References chain with the
// original Message-ID so the new reply sits at the bottom of the thread
// graph. Per RFC 5322, the References header should contain the whole
// ancestor chain in order, terminating with the parent's Message-ID.
func buildReferences(orig types.Message) []string {
	var refs []string
	if orig.InReplyTo != "" {
		// We don't have the full References chain in types.Message today;
		// approximate it by concatenating InReplyTo (thread grandparent)
		// and the orig Message-ID (immediate parent). Good enough for
		// Gmail to stitch threads correctly.
		refs = append(refs, orig.InReplyTo)
	}
	if orig.MessageID != "" {
		refs = append(refs, orig.MessageID)
	}
	return refs
}

// displayName returns the "Name" portion of "Name <addr@ex>" if present,
// otherwise the address itself. Used for the attribution line.
func displayName(from string) string {
	if i := strings.LastIndex(from, "<"); i > 0 {
		name := strings.TrimSpace(from[:i])
		if name != "" {
			return strings.Trim(name, `"`)
		}
	}
	return extractAddress(from)
}

// extractAddress pulls the bare address out of "Name <addr@ex>" or
// returns the input trimmed. Defensive — empty strings and malformed
// inputs become "".
func extractAddress(s string) string {
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.LastIndex(s, ">"); j > i {
			return strings.TrimSpace(s[i+1 : j])
		}
	}
	return strings.TrimSpace(s)
}

// splitAddresses breaks a comma-separated header value into individual
// entries. Respects the "," inside angle brackets is impossible in
// practice (addr-spec disallows comma in local or domain) so a plain
// split is safe for our input set.
func splitAddresses(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// sameAddress compares two email addresses case-insensitively after
// extracting the bare address from each. Used to keep self off the
// reply's To/Cc list.
func sameAddress(a, b string) bool {
	return strings.EqualFold(extractAddress(a), extractAddress(b))
}

// containsAddress reports whether list already contains addr, comparing
// case-insensitively on the bare address.
func containsAddress(list []string, addr string) bool {
	for _, entry := range list {
		if sameAddress(entry, addr) {
			return true
		}
	}
	return false
}

// ErrNoRecipients is returned by Validate when a draft has nothing to
// send to. Exported so the TUI can detect it and surface a specific
// "you haven't picked a recipient" message instead of a generic error.
var ErrNoRecipients = fmt.Errorf("no recipients")

// Validate enforces the minimal invariants a draft must satisfy before
// the daemon accepts it for submission. Pure function so the TUI can
// call it client-side before bothering the daemon.
func Validate(m smtp.Message) error {
	if len(m.To) == 0 && len(m.Cc) == 0 {
		return ErrNoRecipients
	}
	if strings.TrimSpace(m.Subject) == "" {
		return fmt.Errorf("subject is required")
	}
	return nil
}

// ForceDate lets tests pin the Date on a draft so quoteBody's
// attribution is deterministic. Production callers leave the orig.Date
// as whatever the IMAP server reported.
func ForceDate(orig *types.Message, t time.Time) {
	orig.Date = t
}
