package imap

import (
	"context"
	"fmt"
	"sync"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// trashFolderCache memoises the account's Trash mailbox name. Discovery uses
// SPECIAL-USE (RFC 6154) when the server advertises it and falls back to the
// Gmail / common naming convention otherwise. The result is cached on the
// Syncer for the lifetime of the process — mailboxes don't change names
// often and the cost of re-listing on every trash action is not worth it.
type trashFolderCache struct {
	once sync.Once
	name string
	err  error
}

// resolveTrashFolder returns the mailbox to move trashed messages into,
// using the same logged-in IMAP session as the caller.
func (s *Syncer) resolveTrashFolder(c *imapclient.Client) (string, error) {
	s.trashCache.once.Do(func() {
		name, err := discoverTrashFolder(c)
		s.trashCache.name = name
		s.trashCache.err = err
	})
	return s.trashCache.name, s.trashCache.err
}

// discoverTrashFolder walks the mailbox list looking for \Trash via
// SPECIAL-USE, then falls back to common literal names.
func discoverTrashFolder(c *imapclient.Client) (string, error) {
	opts := &goimap.ListOptions{ReturnSpecialUse: true}
	data, err := c.List("", "*", opts).Collect()
	if err != nil {
		return "", fmt.Errorf("list mailboxes: %w", err)
	}
	// First pass: SPECIAL-USE attribute wins.
	for _, mb := range data {
		for _, attr := range mb.Attrs {
			if attr == goimap.MailboxAttrTrash {
				return mb.Mailbox, nil
			}
		}
	}
	// Fallback: literal names we've seen in the wild.
	for _, candidate := range []string{"[Gmail]/Trash", "Trash", "Deleted Items", "Deleted Messages"} {
		for _, mb := range data {
			if mb.Mailbox == candidate {
				return mb.Mailbox, nil
			}
		}
	}
	return "", fmt.Errorf("could not locate Trash mailbox — neither SPECIAL-USE \\Trash nor a known fallback name was found")
}

// Trash moves a message from folder into the account's Trash mailbox. Uses
// IMAP UID MOVE when the server supports it (Gmail does); the client library
// silently falls back to COPY + \Deleted + EXPUNGE when MOVE is not
// advertised. Local cache is updated to remove the source row immediately so
// the TUI reflects the move without waiting for the next sync pass.
func (s *Syncer) Trash(ctx context.Context, folder string, uid uint32) error {
	err := s.withOpsConn(ctx, func(c *imapclient.Client) error {
		trash, err := s.resolveTrashFolder(c)
		if err != nil {
			return err
		}
		if trash == folder {
			// Trash-from-inside-Trash is a permanent delete, not a move. The
			// caller should have dispatched to PermanentDelete; refuse here to
			// avoid a silent no-op server round trip.
			return fmt.Errorf("message already in Trash; use PermanentDelete instead")
		}
		if _, err := c.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select %s: %w", folder, err)
		}
		if _, err := c.Move(goimap.UIDSetNum(goimap.UID(uid)), trash).Wait(); err != nil {
			return fmt.Errorf("move to %s: %w", trash, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Local: drop from source. The Trash folder will pick up the moved
	// message on the next sync pass (with its new UID assigned by the server).
	if err := s.store.DeleteMessage(ctx, folder, uid); err != nil {
		return fmt.Errorf("delete from cache: %w", err)
	}
	return nil
}

// PermanentDelete flags the message \Deleted and expunges it — the irrecoverable
// path. UID EXPUNGE is used when UIDPLUS is advertised so only the targeted
// UID is purged; otherwise plain EXPUNGE is issued (Gmail's Trash only holds
// already-trashed items so a folder-wide expunge is safe there).
func (s *Syncer) PermanentDelete(ctx context.Context, folder string, uid uint32) error {
	err := s.withOpsConn(ctx, func(c *imapclient.Client) error {
		if _, err := c.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select %s: %w", folder, err)
		}
		uidSet := goimap.UIDSetNum(goimap.UID(uid))
		if err := c.Store(uidSet, &goimap.StoreFlags{
			Op:     goimap.StoreFlagsAdd,
			Flags:  []goimap.Flag{goimap.FlagDeleted},
			Silent: true,
		}, nil).Close(); err != nil {
			return fmt.Errorf("store \\Deleted: %w", err)
		}
		var expunge *imapclient.ExpungeCommand
		if c.Caps().Has(goimap.CapUIDPlus) {
			expunge = c.UIDExpunge(uidSet)
		} else {
			expunge = c.Expunge()
		}
		if err := expunge.Close(); err != nil {
			return fmt.Errorf("expunge: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.store.DeleteMessage(ctx, folder, uid); err != nil {
		return fmt.Errorf("delete from cache: %w", err)
	}
	return nil
}
