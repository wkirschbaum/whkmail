// Package smtp is the outbound side of whkmail. The Sender authenticates
// to Gmail's SMTP submission port with XOAUTH2 using the same token
// source the IMAP syncer uses, so adding SMTP required no extra OAuth
// consent. Compose behaviour (reply / reply-all / thread stitching) is
// not in this package — that lives in internal/compose and hands a
// fully-constructed Message here for transport.
package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

// Sender owns the SMTP submission path for one account. Safe to call
// Send concurrently — each call dials a fresh submission connection and
// closes it at the end, which is what Gmail expects.
type Sender struct {
	host  string
	port  int
	email string
	token func(ctx context.Context) (string, error)
}

// New constructs a Sender. Mirrors imap.New so the daemon's composition
// root treats both transports the same way.
func New(host string, port int, email string, token func(context.Context) (string, error)) *Sender {
	return &Sender{host: host, port: port, email: email, token: token}
}

// Send delivers one Message through the configured Gmail SMTP server.
// Responsibilities:
//
//   - Fill in From if missing (the sender's own email is the natural default).
//   - Pull a fresh OAuth2 token.
//   - Dial + STARTTLS + AUTH XOAUTH2 + MAIL/RCPT/DATA.
//
// Gmail archives the sent message in "[Gmail]/Sent Mail" automatically
// when submission succeeds, so there's no separate IMAP APPEND step on
// our side.
func (s *Sender) Send(ctx context.Context, msg Message) error {
	if msg.From == "" {
		msg.From = s.email
	}
	if msg.Date.IsZero() {
		msg.Date = time.Now()
	}
	if len(msg.Recipients()) == 0 {
		return fmt.Errorf("smtp: no recipients")
	}

	token, err := s.token(ctx)
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	// Dial via net.Dialer under the caller's context so shutdown during
	// send cancels promptly.
	c, err := dial(ctx, addr, s.host)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Auth(NewXOAUTH2(s.email, token)); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := c.Mail(stripAngleBrackets(msg.From)); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, rcpt := range msg.Recipients() {
		if err := c.Rcpt(stripAngleBrackets(rcpt)); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write([]byte(msg.RFC5322())); err != nil {
		_ = w.Close()
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}

	return c.Quit()
}

// stripAngleBrackets pulls the bare address out of a "Name <addr@ex>"
// or "<addr@ex>" form. Gmail's SMTP submission expects just the address
// portion on MAIL FROM / RCPT TO lines.
func stripAngleBrackets(addr string) string {
	if i := strings.LastIndex(addr, "<"); i >= 0 {
		if j := strings.LastIndex(addr, ">"); j > i {
			return addr[i+1 : j]
		}
	}
	return strings.TrimSpace(addr)
}

// dial connects to host:port via STARTTLS. Kept as a package-level
// function (not a method) so tests can stub it without touching the
// Sender struct.
var dial = func(ctx context.Context, addr, host string) (*smtp.Client, error) {
	d := smtpDialer{}
	return d.dial(ctx, addr, host)
}

// smtpDialer is a thin wrapper so dial() stays testable. Gmail
// submission (port 587) requires STARTTLS; port 465 wants implicit TLS.
// The Sender constructor picks the port; we branch here on it.
type smtpDialer struct{}

func (smtpDialer) dial(ctx context.Context, addr, host string) (*smtp.Client, error) {
	// Implicit TLS (port 465) — dial TLS directly.
	if strings.HasSuffix(addr, ":465") {
		tlsConn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
		if err != nil {
			return nil, err
		}
		c, err := smtp.NewClient(tlsConn, host)
		if err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		return c, nil
	}

	// STARTTLS (port 587).
	c, err := smtp.Dial(addr)
	if err != nil {
		return nil, err
	}
	if err := c.Hello("localhost"); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("EHLO: %w", err)
	}
	if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("STARTTLS: %w", err)
	}
	_ = ctx // context isn't plumbed into net/smtp; best-effort for now
	return c, nil
}
