package imap

import (
	"context"
	"fmt"
	"sync"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wkirschbaum/whkmail/internal/events"
)

// sentFolderCache memoises the account's Sent mailbox name the same way
// trashFolderCache does for Trash. Uses the SPECIAL-USE attribute when
// the server advertises it, falling back to common literal names.
type sentFolderCache struct {
	once sync.Once
	name string
	err  error
}

// ResolveSentFolder returns the mailbox that archived-sent mail lives in
// for this account. Result is cached for the life of the Syncer — the
// mapping is a server-side convention that doesn't change. Exported so
// the daemon's send handler can trigger a post-send re-sync of exactly
// the right folder.
func (s *Syncer) ResolveSentFolder(ctx context.Context) (string, error) {
	err := s.withOpsConn(ctx, func(c *imapclient.Client) error {
		s.sentCache.once.Do(func() {
			s.sentCache.name, s.sentCache.err = discoverSentFolder(c)
		})
		return s.sentCache.err
	})
	if err != nil {
		return "", err
	}
	return s.sentCache.name, nil
}

// discoverSentFolder walks the mailbox list looking for \Sent via
// SPECIAL-USE (RFC 6154), then falls back to common literal names.
func discoverSentFolder(c *imapclient.Client) (string, error) {
	opts := &goimap.ListOptions{ReturnSpecialUse: true}
	data, err := c.List("", "*", opts).Collect()
	if err != nil {
		return "", fmt.Errorf("list mailboxes: %w", err)
	}
	for _, mb := range data {
		for _, attr := range mb.Attrs {
			if attr == goimap.MailboxAttrSent {
				return mb.Mailbox, nil
			}
		}
	}
	for _, candidate := range []string{"[Gmail]/Sent Mail", "Sent", "Sent Items", "Sent Mail"} {
		for _, mb := range data {
			if mb.Mailbox == candidate {
				return mb.Mailbox, nil
			}
		}
	}
	return "", fmt.Errorf("could not locate Sent mailbox — neither SPECIAL-USE \\Sent nor a known fallback name was found")
}

// isSentFolder reports whether folder is this account's Sent mailbox.
// Cheap after the first call; any discovery error is treated as "not
// Sent" so a sync against an account without a Sent folder still works.
func (s *Syncer) isSentFolder(ctx context.Context, folder string) bool {
	name, err := s.ResolveSentFolder(ctx)
	if err != nil {
		return false
	}
	return name == folder
}

// SyncFolder triggers a one-shot re-sync of folder on the shared ops
// connection. Used by the daemon's send handler to refresh the Sent
// folder immediately after a submission and to pick up the \Answered
// flag on the message we just replied to. Emits KindSyncStarted /
// KindSyncDone so the TUI status bar reflects the work.
func (s *Syncer) SyncFolder(ctx context.Context, folder string) error {
	s.bus.Publish(events.SyncStartedEvent(s.email))
	defer s.bus.Publish(events.SyncDoneEvent(s.email))
	return s.withOpsConn(ctx, func(c *imapclient.Client) error {
		return s.syncMailbox(ctx, c, folder)
	})
}
