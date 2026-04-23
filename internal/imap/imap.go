// Package imap implements the IMAP + XOAUTH2 transport for whkmail. The
// Syncer is the concrete implementation of server.MailProvider plus a
// long-running sync loop (Run) that keeps the local SQLite cache in step
// with the server.
//
// Files in this package:
//
//	imap.go  — Syncer struct, constructor, shared connect helper
//	sync.go  — Run / initial sync / syncMailbox / IDLE / poll
//	body.go  — FetchBody / MarkRead / MIME text extraction
//	trash.go — Trash / PermanentDelete / Trash-folder discovery
//	oauth.go — XOAUTH2 SASL mechanism
package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/storage"
)

// Syncer owns the IMAP connection(s) and storage for one account.
type Syncer struct {
	host  string
	port  int
	email string
	token func(ctx context.Context) (string, error)
	store storage.Store
	bus   *events.Bus

	// opMu serialises the one-shot RPC-style methods (FetchBody, MarkRead,
	// Trash, PermanentDelete) — holding it also protects opsConn and the
	// backoff counter. Each of those methods would otherwise dial +
	// XOAUTH2-authenticate a fresh connection per call; firing them in
	// parallel (e.g. when the user holds `d` to trash a run of messages)
	// slams Gmail with concurrent sessions and gets 5xx back. The long-
	// running sync loop (Run/idle/poll) has its own dedicated connection
	// and does not take this lock.
	opMu sync.Mutex
	// opsConn is a long-lived IMAP session reused across one-shot ops so
	// the TCP+TLS+auth round-trip isn't paid on every trash/mark-read.
	// Reset to nil on any operation error; the next call reconnects.
	opsConn *imapclient.Client
	// opFailures counts consecutive errors on opsConn. withOpsConn sleeps
	// an exponential (capped) delay before the next op when this is > 0
	// so a genuine rate-limit / network outage doesn't turn the TUI into
	// a retry storm.
	opFailures int

	trashCache trashFolderCache
}

// New constructs a Syncer. The token closure is expected to yield a fresh
// OAuth2 access token on each call; see internal/oauth.TokenFn.
func New(host string, port int, email string, token func(context.Context) (string, error), st storage.Store, bus *events.Bus) *Syncer {
	return &Syncer{host: host, port: port, email: email, token: token, store: st, bus: bus}
}

// connect dials the IMAP server and authenticates with XOAUTH2. Each call
// returns a fresh connection; callers are responsible for Close(). The long-
// lived sync loop calls this directly; one-shot ops go through withOpsConn
// which caches and reuses the returned client.
func (s *Syncer) connect(ctx context.Context) (*imapclient.Client, error) {
	token, err := s.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	c, err := imapclient.DialTLS(addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: s.host},
	})
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	if err := c.Authenticate(&xoauth2{email: s.email, token: token}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("auth: %w", err)
	}
	return c, nil
}

// withOpsConn runs fn against a cached IMAP connection, serialised across
// callers. Responsibilities:
//
//   - Lazily dial + auth on first use; reuse thereafter so a burst of ops
//     pays one handshake rather than N.
//   - If fn fails on a reused connection, close it and retry once on a
//     fresh connection (covers the "server dropped us while idle" case).
//   - After a sustained failure streak, sleep a capped exponential backoff
//     before the *next* caller's attempt so a genuine outage or rate-limit
//     response doesn't turn into a tight retry loop.
//
// Does not take ctx into its backoff wait — a pending op with a 30s timeout
// will unblock correctly when ctx is cancelled.
func (s *Syncer) withOpsConn(ctx context.Context, fn func(c *imapclient.Client) error) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	if delay := s.backoffDuration(); delay > 0 {
		slog.Warn("imap: backing off before next op", "account", s.email, "delay", delay, "failures", s.opFailures)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	// Up to two attempts: the first may hit a stale cached connection, the
	// second uses a guaranteed-fresh one. A fresh-connect failure is
	// reported immediately — no point retrying the same dial.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		hadCachedConn := s.opsConn != nil
		if s.opsConn == nil {
			c, err := s.connect(ctx)
			if err != nil {
				s.opFailures++
				return err
			}
			s.opsConn = c
		}
		err := fn(s.opsConn)
		if err == nil {
			s.opFailures = 0
			return nil
		}
		lastErr = err
		_ = s.opsConn.Close()
		s.opsConn = nil
		if !hadCachedConn {
			// A fresh connection failed — don't retry, report.
			s.opFailures++
			return err
		}
		// Retry once with a fresh connection.
	}
	s.opFailures++
	return lastErr
}

// backoffDuration returns how long to sleep before the next op. Starts at
// 0s for the first two failures (transient noise), then grows exponentially
// up to ~30s with jitter so successive clients don't thunder together.
func (s *Syncer) backoffDuration() time.Duration {
	if s.opFailures < 2 {
		return 0
	}
	const (
		base = 500 * time.Millisecond
		cap  = 30 * time.Second
	)
	shift := s.opFailures - 2
	if shift > 6 {
		shift = 6
	}
	d := base << shift
	if d > cap {
		d = cap
	}
	// Jitter: ±25%.
	jitter := time.Duration(rand.Int64N(int64(d/2))) - d/4
	return d + jitter
}

// Close releases the cached ops connection. Called from server.RemoveAccount
// via the optional Closer interface; safe to call on a Syncer that never
// opened an ops connection.
func (s *Syncer) Close() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.opsConn == nil {
		return nil
	}
	err := s.opsConn.Close()
	s.opsConn = nil
	return err
}
