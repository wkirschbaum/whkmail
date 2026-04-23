// Package storage defines the persistence contract for whkmail's message and
// folder cache, plus concrete adapters that implement it.
//
// The Store interface is expressed in terms of whkmail's domain (messages,
// folders, sync state), not in terms of any particular storage engine, so
// non-SQL backends (e.g. Maildir) can implement it alongside the SQLite
// adapter. The daemon depends on this interface; concrete types are only
// named at construction time.
package storage

import (
	"context"

	"github.com/wkirschbaum/whkmail/internal/types"
)

// Store is the persistence contract for whkmail's message and folder cache.
// Implementations must be safe for concurrent use by one reader (SSE/HTTP)
// and one writer (the sync loop).
type Store interface {
	// UpsertMessage inserts or updates a message's header fields.
	// Returns inserted=true only when a new row was created — callers use
	// this to distinguish genuinely new messages from re-syncs of ones
	// already cached (e.g. to decide whether to fire a notification).
	UpsertMessage(ctx context.Context, m types.Message) (inserted bool, err error)

	// UpsertMessages is the batch form of UpsertMessage. Implementations
	// commit the whole slice in a single transaction so a sync pass that
	// receives 200 messages pays one commit, not 200. The returned slice
	// is parallel to msgs: out[i]=true iff msgs[i] was a new row.
	UpsertMessages(ctx context.Context, msgs []types.Message) (inserted []bool, err error)

	// ListMessages returns the most recent limit messages from folder,
	// newest first. Bodies are not included.
	ListMessages(ctx context.Context, folder string, limit int) ([]types.Message, error)

	// GetMessage returns a single message including its body, or nil if
	// not found.
	GetMessage(ctx context.Context, folder string, uid uint32) (*types.Message, error)

	// SetBodyText caches the fetched body text for a message. A sync pass
	// must not overwrite this — it is managed separately from header
	// upserts so cached bodies survive re-syncs.
	SetBodyText(ctx context.Context, folder string, uid uint32, body string) error

	// MarkSeen marks a message as read in the cache.
	MarkSeen(ctx context.Context, folder string, uid uint32) error

	// DeleteMessage removes a single message from the cache. Used when a
	// message is moved (e.g. trashed) or expunged on the remote; the caller
	// is responsible for any follow-up insertion in the destination folder.
	DeleteMessage(ctx context.Context, folder string, uid uint32) error

	// UpsertFolder inserts or updates a folder record. Message and unread
	// counts are not stored — ListFolders computes them from the message
	// set.
	UpsertFolder(ctx context.Context, f types.Folder) error

	// ListFolders returns all known folders ordered by name, with live
	// message and unread counts.
	ListFolders(ctx context.Context) ([]types.Folder, error)

	// GetFolderSync returns the stored UIDVALIDITY and UIDNEXT for a
	// folder. Returns (0, 1, nil) if the folder has never been synced.
	GetFolderSync(ctx context.Context, folder string) (uidValidity, uidNext uint32, err error)

	// UpdateFolderSync persists the UIDVALIDITY and UIDNEXT observed at
	// the end of a sync pass.
	UpdateFolderSync(ctx context.Context, folder string, uidValidity, uidNext uint32) error

	// DeleteFolderMessages removes all cached messages for a folder.
	// Called when UIDVALIDITY changes and the entire UID space must be
	// re-synced from scratch.
	DeleteFolderMessages(ctx context.Context, folder string) error

	// Close releases any resources held by the adapter.
	Close() error
}
