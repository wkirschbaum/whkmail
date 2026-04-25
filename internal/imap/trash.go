package imap

import (
	"context"
	"fmt"
	"sync"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// trashFolderCache memoises the account's Trash mailbox name. Discovery uses
// SPECIAL-USE (RFC 6154) when the server advertises it and falls back to the
// Gmail / common naming convention otherwise. Only a successful resolution is
// cached so a transient listing failure is retried on the next call.
type trashFolderCache struct {
	mu   sync.Mutex
	name string // empty until successfully resolved
}

// resolveTrashFolder returns the mailbox to move trashed messages into,
// using the same logged-in IMAP session as the caller.
func (s *Syncer) resolveTrashFolder(c *imapclient.Client) (string, error) {
	s.trashCache.mu.Lock()
	defer s.trashCache.mu.Unlock()
	if s.trashCache.name != "" {
		return s.trashCache.name, nil
	}
	name, err := discoverTrashFolder(c)
	if err != nil {
		return "", err
	}
	s.trashCache.name = name
	return name, nil
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
	for _, candidate := range types.TrashFolderNames {
		for _, mb := range data {
			if mb.Mailbox == candidate {
				return mb.Mailbox, nil
			}
		}
	}
	return "", fmt.Errorf("could not locate Trash mailbox — neither SPECIAL-USE \\Trash nor a known fallback name was found")
}

// TrashBatch moves one or more messages from folder into the account's Trash
// mailbox in a single IMAP UID MOVE command. Uses the dedicated bulk
// connection so it cannot block interactive ops (FetchBody, MarkRead). The
// caller is responsible for local cache removal.
func (s *Syncer) TrashBatch(ctx context.Context, folder string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	return s.withBulkConn(ctx, func(c *imapclient.Client) error {
		trash, err := s.resolveTrashFolder(c)
		if err != nil {
			return err
		}
		if trash == folder {
			return fmt.Errorf("messages already in Trash; use PermanentDelete instead")
		}
		if _, err := c.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select %s: %w", folder, err)
		}
		uidList := make([]goimap.UID, len(uids))
		for i, uid := range uids {
			uidList[i] = goimap.UID(uid)
		}
		if _, err := c.Move(goimap.UIDSetNum(uidList...), trash).Wait(); err != nil {
			return fmt.Errorf("move to %s: %w", trash, err)
		}
		return nil
	})
}

// Trash moves a single message from folder into the account's Trash mailbox
// and removes it from the local cache. Used for single-message interactive
// deletes; bulk deletes go through the daemon's TrashWorker + TrashBatch.
func (s *Syncer) Trash(ctx context.Context, folder string, uid uint32) error {
	if err := s.TrashBatch(ctx, folder, []uint32{uid}); err != nil {
		return err
	}
	if err := s.store.DeleteMessage(ctx, folder, uid); err != nil {
		return fmt.Errorf("delete from cache: %w", err)
	}
	return nil
}

// PermanentDeleteBatch flags all uids \Deleted and expunges them in a single
// IMAP transaction. Uses the dedicated bulk connection. The caller is
// responsible for local cache removal.
func (s *Syncer) PermanentDeleteBatch(ctx context.Context, folder string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	return s.withBulkConn(ctx, func(c *imapclient.Client) error {
		if _, err := c.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select %s: %w", folder, err)
		}
		uidList := make([]goimap.UID, len(uids))
		for i, uid := range uids {
			uidList[i] = goimap.UID(uid)
		}
		uidSet := goimap.UIDSetNum(uidList...)
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
}

// PermanentDelete flags the message \Deleted and expunges it. Used for
// single-message interactive expunge; bulk expunge goes through
// PermanentDeleteWorker + PermanentDeleteBatch.
func (s *Syncer) PermanentDelete(ctx context.Context, folder string, uid uint32) error {
	if err := s.PermanentDeleteBatch(ctx, folder, []uint32{uid}); err != nil {
		return err
	}
	if err := s.store.DeleteMessage(ctx, folder, uid); err != nil {
		return fmt.Errorf("delete from cache: %w", err)
	}
	return nil
}
