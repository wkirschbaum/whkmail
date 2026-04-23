package imap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// The long-running side of the Syncer: the initial-sync pass, IDLE / poll
// fallback, and the per-folder delta sync. Uses one dedicated IMAP
// connection for the life of Run; does *not* take opMu — that mutex
// guards the one-shot methods (FetchBody, MarkRead, Trash, PermanentDelete)
// which each dial a fresh connection.

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
	c, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	s.bus.Publish(events.Event{Kind: events.KindSyncStarted, Account: s.email})

	if err := s.syncFolders(ctx, c); err != nil {
		return fmt.Errorf("sync folders: %w", err)
	}

	s.bus.Publish(events.Event{Kind: events.KindSyncDone, Account: s.email})

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
	mbox, err := c.Select(name, &goimap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return err
	}

	storedValidity, storedUIDNext, err := s.store.GetFolderSync(ctx, name)
	if err != nil {
		return fmt.Errorf("get folder sync: %w", err)
	}

	var msgs []*imapclient.FetchMessageBuffer

	switch {
	case mbox.NumMessages == 0:
		// Empty mailbox — nothing to fetch.

	case storedValidity != 0 && storedValidity != mbox.UIDValidity:
		// UIDVALIDITY changed: the server reassigned all UIDs.
		// Wipe the cache and do a fresh initial sync.
		slog.Warn("UIDVALIDITY changed, wiping folder cache",
			"folder", name, "old", storedValidity, "new", mbox.UIDValidity)
		if err := s.store.DeleteFolderMessages(ctx, name); err != nil {
			return fmt.Errorf("wipe folder: %w", err)
		}
		msgs, err = s.fetchRecent(c, mbox.NumMessages)

	case storedValidity == mbox.UIDValidity && goimap.UID(storedUIDNext) >= mbox.UIDNext:
		// Same UIDVALIDITY, no new UIDs since last sync.

	case storedValidity == mbox.UIDValidity:
		// Same UIDVALIDITY, delta: only fetch UIDs we haven't seen yet.
		var uidSet goimap.UIDSet
		uidSet.AddRange(goimap.UID(storedUIDNext), 0) // storedUIDNext:*
		msgs, err = c.Fetch(uidSet, &goimap.FetchOptions{
			Envelope: true, Flags: true, UID: true,
		}).Collect()

	default:
		// First sync for this folder (storedValidity == 0).
		msgs, err = s.fetchRecent(c, mbox.NumMessages)
	}

	if err != nil {
		return err
	}

	// Build the batch first so the whole folder commits in one transaction.
	batch := make([]types.Message, 0, len(msgs))
	for _, buf := range msgs {
		batch = append(batch, messageFromBuffer(name, buf))
	}
	inserted, err := s.store.UpsertMessages(ctx, batch)
	if err != nil {
		return fmt.Errorf("upsert batch: %w", err)
	}
	for i, m := range batch {
		if inserted[i] && m.Unread {
			s.bus.Publish(events.Event{
				Kind:    events.KindNewMessage,
				Account: s.email,
				Folder:  name,
				UID:     m.UID,
				Subject: m.Subject,
				From:    m.From,
			})
		}
	}

	if err := s.store.UpdateFolderSync(ctx, name, mbox.UIDValidity, uint32(mbox.UIDNext)); err != nil {
		slog.Warn("update folder sync", "folder", name, "err", err)
	}
	return nil
}

// fetchRecent fetches the most recent (up to 200) messages by sequence number.
// Used for initial syncs and post-UIDVALIDITY-change re-syncs.
func (s *Syncer) fetchRecent(c *imapclient.Client, total uint32) ([]*imapclient.FetchMessageBuffer, error) {
	start := uint32(1)
	if total > 200 {
		start = total - 199
	}
	var seqSet goimap.SeqSet
	seqSet.AddRange(start, total)
	return c.Fetch(seqSet, &goimap.FetchOptions{
		Envelope: true, Flags: true, UID: true,
	}).Collect()
}

func (s *Syncer) idle(ctx context.Context, c *imapclient.Client) error {
	if _, err := c.Select("INBOX", &goimap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return fmt.Errorf("select inbox for idle: %w", err)
	}

	if !c.Caps().Has(goimap.CapIdle) {
		slog.Info("IDLE not supported, falling back to polling")
		return s.poll(ctx, c)
	}

	for {
		idle, err := c.Idle()
		if err != nil {
			return fmt.Errorf("idle: %w", err)
		}

		done := make(chan error, 1)
		go func() { done <- idle.Wait() }()

		var idleErr error
		select {
		case <-ctx.Done():
			_ = idle.Close()
			<-done
			return nil
		case idleErr = <-done:
			// Server sent a notification; IDLE was terminated by the library.
		case <-time.After(20 * time.Minute):
			_ = idle.Close()
			idleErr = <-done
		}
		if idleErr != nil {
			return idleErr
		}

		if err := s.syncMailbox(ctx, c, "INBOX"); err != nil {
			slog.Warn("re-sync after idle", "err", err)
		}
		s.bus.Publish(events.Event{Kind: events.KindSyncDone, Account: s.email})
	}
}

// poll is an IDLE fallback that re-syncs INBOX on a fixed interval.
func (s *Syncer) poll(ctx context.Context, c *imapclient.Client) error {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.syncMailbox(ctx, c, "INBOX"); err != nil {
				slog.Warn("poll re-sync", "err", err)
			}
			s.bus.Publish(events.Event{Kind: events.KindSyncDone, Account: s.email})
		}
	}
}

// messageFromBuffer converts the library's fetch result into our domain type.
func messageFromBuffer(folder string, buf *imapclient.FetchMessageBuffer) types.Message {
	m := types.Message{
		UID:     uint32(buf.UID),
		Folder:  folder,
		Unread:  !containsFlag(buf.Flags, goimap.FlagSeen),
		Flagged: containsFlag(buf.Flags, goimap.FlagFlagged),
		Draft:   containsFlag(buf.Flags, goimap.FlagDraft),
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

func addressString(a goimap.Address) string {
	name := strings.TrimSpace(a.Name)
	addr := fmt.Sprintf("%s@%s", a.Mailbox, a.Host)
	if name != "" {
		return fmt.Sprintf("%s <%s>", name, addr)
	}
	return addr
}

func containsFlag(flags []goimap.Flag, target goimap.Flag) bool {
	for _, f := range flags {
		if f == target {
			return true
		}
	}
	return false
}
