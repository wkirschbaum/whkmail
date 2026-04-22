package sync

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/store"
	"github.com/wkirschbaum/whkmail/internal/types"
)

type Syncer struct {
	host  string
	port  int
	email string
	token func(ctx context.Context) (string, error)
	store *store.Store
	bus   *events.Bus
}

func New(host string, port int, email string, token func(context.Context) (string, error), st *store.Store, bus *events.Bus) *Syncer {
	return &Syncer{host: host, port: port, email: email, token: token, store: st, bus: bus}
}

// Run performs an initial sync then idles, re-syncing on IDLE notifications.
// Blocks until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) {
	for {
		if err := s.run(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("sync error, retrying in 30s", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}
}

func (s *Syncer) run(ctx context.Context) error {
	token, err := s.token(ctx)
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	c, err := imapclient.DialTLS(addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: s.host},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Authenticate(&xoauth2{email: s.email, token: token}); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	s.bus.Publish(events.Event{Kind: events.KindSyncStarted})

	if err := s.syncFolders(ctx, c); err != nil {
		return fmt.Errorf("sync folders: %w", err)
	}

	s.bus.Publish(events.Event{Kind: events.KindSyncDone})

	return s.idle(ctx, c)
}

func (s *Syncer) syncFolders(ctx context.Context, c *imapclient.Client) error {
	data, err := c.List("", "*", nil).Collect()
	if err != nil {
		return err
	}
	for _, mb := range data {
		f := types.Folder{
			Name:      mb.Mailbox,
			Delimiter: string(mb.Delim),
		}
		if err := s.store.UpsertFolder(ctx, f); err != nil {
			slog.Warn("upsert folder", "name", f.Name, "err", err)
		}
		if err := s.syncMailbox(ctx, c, mb.Mailbox); err != nil {
			slog.Warn("sync mailbox", "name", mb.Mailbox, "err", err)
		}
	}
	return nil
}

func (s *Syncer) syncMailbox(ctx context.Context, c *imapclient.Client, name string) error {
	mbox, err := c.Select(name, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return err
	}
	if mbox.NumMessages == 0 {
		return nil
	}

	// Fetch the 50 most recent messages by sequence number.
	start := uint32(1)
	if mbox.NumMessages > 50 {
		start = mbox.NumMessages - 49
	}
	var seqSet imap.SeqSet
	seqSet.AddRange(start, mbox.NumMessages)

	msgs, err := c.Fetch(seqSet, &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
	}).Collect()
	if err != nil {
		return err
	}

	for _, buf := range msgs {
		m := messageFromBuffer(name, buf)
		if err := s.store.UpsertMessage(ctx, m); err != nil {
			slog.Warn("upsert message", "uid", m.UID, "err", err)
			continue
		}
		if m.Unread {
			s.bus.Publish(events.Event{
				Kind:    events.KindNewMessage,
				Folder:  name,
				UID:     m.UID,
				Subject: m.Subject,
				From:    m.From,
			})
		}
	}
	return nil
}

func (s *Syncer) idle(ctx context.Context, c *imapclient.Client) error {
	if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return fmt.Errorf("select inbox for idle: %w", err)
	}

	for {
		idle, err := c.Idle()
		if err != nil {
			return fmt.Errorf("idle: %w", err)
		}

		// RFC 2177: clients should re-issue IDLE at least every 29 minutes.
		timer := time.NewTimer(20 * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = idle.Close()
			return nil
		case <-timer.C:
		}
		timer.Stop()

		if err := idle.Close(); err != nil {
			return err
		}
		if err := s.syncMailbox(ctx, c, "INBOX"); err != nil {
			slog.Warn("re-sync after idle", "err", err)
		}
	}
}

func messageFromBuffer(folder string, buf *imapclient.FetchMessageBuffer) types.Message {
	m := types.Message{
		UID:    uint32(buf.UID),
		Folder: folder,
		Unread: !containsFlag(buf.Flags, imap.FlagSeen),
		Flagged: containsFlag(buf.Flags, imap.FlagFlagged),
	}
	if buf.Envelope != nil {
		m.Subject = buf.Envelope.Subject
		m.Date = buf.Envelope.Date
		if len(buf.Envelope.From) > 0 {
			m.From = addressString(buf.Envelope.From[0])
		}
		if len(buf.Envelope.To) > 0 {
			m.To = addressString(buf.Envelope.To[0])
		}
	}
	return m
}

func addressString(a imap.Address) string {
	name := strings.TrimSpace(a.Name)
	addr := fmt.Sprintf("%s@%s", a.Mailbox, a.Host)
	if name != "" {
		return fmt.Sprintf("%s <%s>", name, addr)
	}
	return addr
}

func containsFlag(flags []imap.Flag, target imap.Flag) bool {
	for _, f := range flags {
		if f == target {
			return true
		}
	}
	return false
}
