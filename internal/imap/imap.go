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

	// opMu serialises short interactive ops (MarkRead, MarkUnread,
	// MoveToFolder) so they share one cached IMAP session. The long-running
	// sync loop has its own dedicated connection and never takes this lock.
	opMu sync.Mutex
	// opsConn is a long-lived IMAP session reused across interactive ops.
	// Reset to nil on any operation error; the next call reconnects.
	opsConn    *imapclient.Client
	opFailures int

	// bulkMu serialises background/batch operations (FetchBody, TrashBatch,
	// PermanentDeleteBatch) on a dedicated connection so slow bulk work
	// cannot block interactive ops waiting on opMu.
	bulkMu       sync.Mutex
	bulkConn     *imapclient.Client
	bulkFailures int

	trashCache trashFolderCache
	sentCache  sentFolderCache
	spamCache  spamFolderCache
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

// withOpsConn runs fn against the interactive ops connection, serialised across
// callers. Lazily dials on first use; retries once on a stale connection;
// applies exponential backoff after sustained failures.
func (s *Syncer) withOpsConn(ctx context.Context, fn func(c *imapclient.Client) error) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return withConn(ctx, s, &s.opsConn, &s.opFailures, fn)
}

// withBulkConn runs fn against a dedicated bulk-ops connection so background
// batch work (TrashBatch, PermanentDeleteBatch) cannot starve interactive ops
// waiting on opMu. Same retry + backoff semantics as withOpsConn.
func (s *Syncer) withBulkConn(ctx context.Context, fn func(c *imapclient.Client) error) error {
	s.bulkMu.Lock()
	defer s.bulkMu.Unlock()
	return withConn(ctx, s, &s.bulkConn, &s.bulkFailures, fn)
}

// withConn is the shared implementation behind withOpsConn and withBulkConn.
// conn and failures are pointers into the caller's Syncer fields; the caller
// holds the appropriate mutex for the duration of this call.
func withConn(ctx context.Context, s *Syncer, conn **imapclient.Client, failures *int, fn func(*imapclient.Client) error) error {
	if delay := backoffDelay(*failures); delay > 0 {
		slog.Warn("imap: backing off before next op", "account", s.email, "delay", delay, "failures", *failures)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		hadCachedConn := *conn != nil
		if *conn == nil {
			c, err := s.connect(ctx)
			if err != nil {
				*failures++
				return err
			}
			*conn = c
		}
		err := fn(*conn)
		if err == nil {
			*failures = 0
			return nil
		}
		lastErr = err
		_ = (*conn).Close()
		*conn = nil
		if !hadCachedConn {
			*failures++
			return err
		}
	}
	*failures++
	return lastErr
}

// backoffDelay returns how long to sleep before the next op attempt. Zero for
// the first two failures (transient noise), then grows exponentially up to
// ~30 s with ±25% jitter so concurrent callers don't retry in lockstep.
func backoffDelay(failures int) time.Duration {
	if failures < 2 {
		return 0
	}
	const (
		base    = 500 * time.Millisecond
		ceiling = 20 * time.Second // must stay below requestTimeout so the op gets headroom
	)
	shift := failures - 2
	if shift > 6 {
		shift = 6
	}
	d := base << shift
	if d > ceiling {
		d = ceiling
	}
	jitter := time.Duration(rand.Int64N(int64(d/2))) - d/4
	return d + jitter
}

// Close releases both cached connections. Called from server.RemoveAccount
// via the optional Closer interface.
func (s *Syncer) Close() error {
	s.opMu.Lock()
	var opErr error
	if s.opsConn != nil {
		opErr = s.opsConn.Close()
		s.opsConn = nil
	}
	s.opMu.Unlock()

	s.bulkMu.Lock()
	var bulkErr error
	if s.bulkConn != nil {
		bulkErr = s.bulkConn.Close()
		s.bulkConn = nil
	}
	s.bulkMu.Unlock()

	if opErr != nil {
		return opErr
	}
	return bulkErr
}
