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

// Tunings baselined for Gmail. Gmail IMAP documents / enforces:
//
//   - IDLE timeout: ~29 minutes before the server drops an idle client.
//   - Concurrent IMAP connections: 15 per account.
//   - Commands are rate-limited per session; bandwidth ~2.5 GB/day per account.
//
// The Syncer opens 2 connections per account (one for the sync loop, one
// cached for one-shot ops), well under the 15 cap. The values below pick
// safe margins against the other limits.
const (
	// errorRetryDelay is how long Run waits before restarting after a
	// sync error. Short enough that transient network blips resolve
	// quickly; long enough that we don't hot-loop on a persistent failure.
	errorRetryDelay = 30 * time.Second

	// idleKeepalive is the ceiling on one IDLE round-trip. Gmail drops
	// idle connections around 29 minutes; closing at 20 gives us a 9-min
	// buffer and forces a fresh re-sync if no push has arrived.
	idleKeepalive = 20 * time.Minute

	// pollInterval is the IDLE-fallback cadence. Only used if the server
	// doesn't advertise CAP IDLE (essentially never for Gmail). 5 minutes
	// keeps the daemon responsive without being a polling fire hose.
	pollInterval = 5 * time.Minute

	// initialFetchLimit bounds how many messages the initial (or
	// UIDVALIDITY-rewind) sync pulls per folder. Newer messages come
	// through incremental deltas; older messages are fetched on demand
	// when the user scrolls back.
	initialFetchLimit = 200
)

// Run performs an initial sync then idles, re-syncing on IDLE notifications.
// Blocks until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) {
	for {
		if err := s.run(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("sync error, retrying", "err", err, "delay", errorRetryDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(errorRetryDelay):
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

	s.bus.Publish(events.SyncStartedEvent(s.email))

	if err := s.syncFolders(ctx, c); err != nil {
		return fmt.Errorf("sync folders: %w", err)
	}

	s.bus.Publish(events.SyncDoneEvent(s.email))

	return s.idle(ctx, c)
}

func (s *Syncer) syncFolders(ctx context.Context, c *imapclient.Client) error {
	// Request SPECIAL-USE attributes so we can skip virtual aggregate folders
	// like [Gmail]/All Mail (\All) that contain only duplicates of messages
	// already synced from their canonical folders.
	data, err := c.List("", "*", &goimap.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		return err
	}
	total := len(data)
	for i, mb := range data {
		s.bus.Publish(events.SyncProgressEvent(s.email, mb.Mailbox, i+1, total))
		f := types.Folder{
			Name:      mb.Mailbox,
			Delimiter: string(mb.Delim),
		}
		if err := s.store.UpsertFolder(ctx, f); err != nil {
			slog.Warn("upsert folder", "name", f.Name, "err", err)
		}
		if skipMailboxSync(*mb) {
			continue
		}
		if err := s.syncMailbox(ctx, c, mb.Mailbox); err != nil {
			slog.Warn("sync mailbox", "name", mb.Mailbox, "err", err)
		}
	}
	return nil
}

// skipMailboxSync returns true for virtual aggregate mailboxes that contain
// only copies of messages already present in other folders. Syncing them
// wastes IMAP round-trips and inflates the local cache with duplicates.
// The folder record is still upserted so discovery (spam, trash, sent) works.
func skipMailboxSync(mb goimap.ListData) bool {
	for _, attr := range mb.Attrs {
		if attr == goimap.MailboxAttrAll {
			return true
		}
	}
	return false
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
	// Mail in the Sent folder is, by definition, read — the user wrote
	// it. Force Unread=false regardless of the server flag so a sent
	// message never shows up as "unread" in the TUI, even on servers
	// (or moments) where \Seen isn't set on the submitted copy.
	if s.isSentFolder(ctx, name) {
		for i := range batch {
			batch[i].Unread = false
		}
	}
	inserted, err := s.store.UpsertMessages(ctx, batch)
	if err != nil {
		return fmt.Errorf("upsert batch: %w", err)
	}
	for i, m := range batch {
		if inserted[i] && m.Unread {
			s.bus.Publish(events.NewMessageEvent(s.email, name, m.UID, m.Subject, m.From))
		}
	}

	if err := s.store.UpdateFolderSync(ctx, name, mbox.UIDValidity, uint32(mbox.UIDNext)); err != nil {
		slog.Warn("update folder sync", "folder", name, "err", err)
	}
	return nil
}

// fetchRecent fetches the most recent (up to initialFetchLimit) messages
// by sequence number. Used for initial syncs and post-UIDVALIDITY-change
// re-syncs. Older messages page in on demand once the user scrolls.
func (s *Syncer) fetchRecent(c *imapclient.Client, total uint32) ([]*imapclient.FetchMessageBuffer, error) {
	start := uint32(1)
	if total > initialFetchLimit {
		start = total - (initialFetchLimit - 1)
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
		case <-time.After(idleKeepalive):
			_ = idle.Close()
			idleErr = <-done
		}
		if idleErr != nil {
			return idleErr
		}

		s.bus.Publish(events.SyncStartedEvent(s.email))
		if err := s.syncMailbox(ctx, c, "INBOX"); err != nil {
			slog.Warn("re-sync after idle", "err", err)
		}
		s.bus.Publish(events.SyncDoneEvent(s.email))
	}
}

// poll is an IDLE fallback that re-syncs INBOX on a fixed interval.
func (s *Syncer) poll(ctx context.Context, c *imapclient.Client) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.bus.Publish(events.SyncStartedEvent(s.email))
			if err := s.syncMailbox(ctx, c, "INBOX"); err != nil {
				slog.Warn("poll re-sync", "err", err)
			}
			s.bus.Publish(events.SyncDoneEvent(s.email))
		}
	}
}

// messageFromBuffer converts the library's fetch result into our domain type.
func messageFromBuffer(folder string, buf *imapclient.FetchMessageBuffer) types.Message {
	m := types.Message{
		UID:      uint32(buf.UID),
		Folder:   folder,
		Unread:   !containsFlag(buf.Flags, goimap.FlagSeen),
		Flagged:  containsFlag(buf.Flags, goimap.FlagFlagged),
		Answered: containsFlag(buf.Flags, goimap.FlagAnswered),
		Draft:    containsFlag(buf.Flags, goimap.FlagDraft),
	}
	if buf.Envelope != nil {
		m.Subject = buf.Envelope.Subject
		m.Date = buf.Envelope.Date
		m.MessageID = buf.Envelope.MessageID
		if len(buf.Envelope.InReplyTo) > 0 {
			m.InReplyTo = buf.Envelope.InReplyTo[0]
		}
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
