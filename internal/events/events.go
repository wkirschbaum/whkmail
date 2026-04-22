package events

import "sync"

type Kind string

const (
	KindNewMessage  Kind = "new_message"
	KindSyncStarted Kind = "sync_started"
	KindSyncDone    Kind = "sync_done"
)

type Event struct {
	Kind    Kind   `json:"kind"`
	Folder  string `json:"folder,omitempty"`
	UID     uint32 `json:"uid,omitempty"`
	Subject string `json:"subject,omitempty"`
	From    string `json:"from,omitempty"`
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
