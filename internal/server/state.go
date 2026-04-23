package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// MailStore is the persistence interface required by the HTTP handlers.
type MailStore interface {
	ListFolders(ctx context.Context) ([]types.Folder, error)
	ListMessages(ctx context.Context, folder string, limit int) ([]types.Message, error)
	GetMessage(ctx context.Context, folder string, uid uint32) (*types.Message, error)
}

// MailProvider is the protocol interface for fetching and updating mail on the
// remote server. It is intentionally protocol-agnostic so that Exchange, JMAP,
// etc. can implement it alongside the IMAP Syncer.
type MailProvider interface {
	FetchBody(ctx context.Context, folder string, uid uint32) (string, error)
	MarkRead(ctx context.Context, folder string, uid uint32) error
	MarkUnread(ctx context.Context, folder string, uid uint32) error

	// Trash moves a message from folder into the account's designated
	// trash/bin mailbox. For Gmail this is the [Gmail]/Trash mailbox — it
	// differs from \Deleted+EXPUNGE semantics in classic IMAP.
	Trash(ctx context.Context, folder string, uid uint32) error

	// PermanentDelete removes a message with no recycling. For IMAP this
	// is STORE +FLAGS.SILENT (\Deleted) followed by UID EXPUNGE. Expected
	// use: invoked from inside the Trash folder after confirmation.
	PermanentDelete(ctx context.Context, folder string, uid uint32) error
}

// job is a unit of work for the body-fetch worker.
type job struct {
	account string
	folder  string
	uid     uint32
}

// accountState holds the per-account runtime state owned by the server.
type accountState struct {
	email    string
	store    MailStore
	provider MailProvider
	syncing  atomic.Bool
	// cancel stops the per-account syncer goroutine when the account is
	// removed at runtime. nil in tests and for accounts whose owner doesn't
	// need runtime removal.
	cancel context.CancelFunc
}

// State is shared between all HTTP handlers.
type State struct {
	mu       sync.RWMutex
	accounts map[string]*accountState
	bus      *events.Bus
	jobs     chan job
}

// AccountOption customises an account registration. See WithCancel.
type AccountOption func(*accountState)

// WithCancel attaches a cancel function to the account — RemoveAccount will
// invoke it to stop the account's syncer goroutine before deregistration.
func WithCancel(cancel context.CancelFunc) AccountOption {
	return func(s *accountState) { s.cancel = cancel }
}

// NewState creates a new server State backed by the given event bus.
func NewState(bus *events.Bus) *State {
	return &State{
		accounts: make(map[string]*accountState),
		bus:      bus,
		jobs:     make(chan job, 64),
	}
}

// AddAccount registers a mail account with the server.
func (st *State) AddAccount(email string, store MailStore, provider MailProvider, opts ...AccountOption) {
	ac := &accountState{email: email, store: store, provider: provider}
	for _, opt := range opts {
		opt(ac)
	}
	st.mu.Lock()
	st.accounts[email] = ac
	st.mu.Unlock()
}

// RemoveAccount cancels the account's syncer (if any), removes it from the
// registry, and closes its store. Returns an error when the account is not
// registered so the caller can distinguish "already gone" from "found and
// removed". Safe to call concurrently with handlers reading the accounts map.
func (st *State) RemoveAccount(email string) error {
	st.mu.Lock()
	ac, ok := st.accounts[email]
	if ok {
		delete(st.accounts, email)
	}
	st.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown account: %s", email)
	}
	if ac.cancel != nil {
		ac.cancel()
	}
	if closer, ok := ac.store.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			slog.Warn("close account store", "account", email, "err", err)
		}
	}
	// Providers may hold long-lived network resources (e.g. the IMAP ops
	// connection); close them too if the type opts in via Closer.
	if closer, ok := ac.provider.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			slog.Warn("close account provider", "account", email, "err", err)
		}
	}
	return nil
}

// lookupAccount is the internal concurrent-safe read of the accounts map.
func (st *State) lookupAccount(email string) *accountState {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.accounts[email]
}

// snapshotAccounts returns a copy of the current accounts slice. Used by
// HandleStatus to iterate without holding the map lock during I/O.
func (st *State) snapshotAccounts() []*accountState {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]*accountState, 0, len(st.accounts))
	for _, ac := range st.accounts {
		out = append(out, ac)
	}
	return out
}
