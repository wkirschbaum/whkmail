// Package events is a fan-out event bus plus the wire-level Event type
// used by the daemon to tell the TUI about sync progress and new mail.
// Event intentionally carries every optional field in one struct so it
// JSON-encodes cleanly over SSE; use the typed constructors in this
// package (NewMessageEvent, SyncProgressEvent, ...) to guarantee only
// the fields that belong to each Kind are set.
package events

import "sync"

type Kind string

const (
	KindNewMessage   Kind = "new_message"
	KindBodyReady    Kind = "body_ready"
	KindSyncStarted  Kind = "sync_started"
	KindSyncProgress Kind = "sync_progress"
	KindSyncDone     Kind = "sync_done"
)

// Event is the wire-level envelope broadcast on the bus. Most fields are
// optional at the protocol level; which ones are actually populated is
// dictated by Kind. Constructors below encode those invariants — prefer
// them over hand-rolled literals so a typo can't produce, e.g., a
// "sync_done" event that carries a subject.
type Event struct {
	Kind    Kind   `json:"kind"`
	Account string `json:"account,omitempty"`
	Folder  string `json:"folder,omitempty"`
	UID     uint32 `json:"uid,omitempty"`
	Subject string `json:"subject,omitempty"`
	From    string `json:"from,omitempty"`
	// Error carries a human-readable failure reason for terminal events
	// (e.g. KindBodyReady when the background fetch failed). Empty on success.
	Error string `json:"error,omitempty"`
	// Current and Total carry step progress for KindSyncProgress. Zero
	// values mean "not applicable" — other kinds leave them unset.
	Current int `json:"current,omitempty"`
	Total   int `json:"total,omitempty"`
}

// NewMessageEvent announces one freshly-arrived unread message.
func NewMessageEvent(account, folder string, uid uint32, subject, from string) Event {
	return Event{
		Kind:    KindNewMessage,
		Account: account,
		Folder:  folder,
		UID:     uid,
		Subject: subject,
		From:    from,
	}
}

// BodyReadyEvent reports the outcome of a background body fetch. reason
// is empty on success; non-empty populates Error so the TUI can show why
// the body won't render instead of hanging on "Loading…".
func BodyReadyEvent(account, folder string, uid uint32, reason string) Event {
	return Event{
		Kind:    KindBodyReady,
		Account: account,
		Folder:  folder,
		UID:     uid,
		Error:   reason,
	}
}

// SyncStartedEvent marks the beginning of a sync pass (initial, IDLE-
// triggered, or polled). Consumers flip a spinner on.
func SyncStartedEvent(account string) Event {
	return Event{Kind: KindSyncStarted, Account: account}
}

// SyncProgressEvent reports "currently syncing folder N of M" during the
// initial multi-folder pass. IDLE and poll refreshes touch only INBOX so
// they don't emit progress.
func SyncProgressEvent(account, folder string, current, total int) Event {
	return Event{
		Kind:    KindSyncProgress,
		Account: account,
		Folder:  folder,
		Current: current,
		Total:   total,
	}
}

// SyncDoneEvent marks the end of a sync pass. Consumers flip the spinner
// off and refresh their cached view.
func SyncDoneEvent(account string) Event {
	return Event{Kind: KindSyncDone, Account: account}
}

// Bus is a simple fan-out broadcaster. Subscribers receive all events
// published after they subscribed. Slow consumers drop events (non-blocking send).
type Bus struct {
	mu   sync.Mutex
	subs []chan Event
}

func NewBus() *Bus { return &Bus{} }

func (b *Bus) Subscribe(buf int) <-chan Event {
	ch := make(chan Event, buf)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func (b *Bus) Unsubscribe(ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s == ch {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			close(s)
			return
		}
	}
}

func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		select {
		case s <- e:
		default:
		}
	}
}
