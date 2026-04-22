package events_test

import (
	"testing"

	"github.com/wkirschbaum/whkmail/internal/events"
)

func TestBus_PublishReceive(t *testing.T) {
	b := events.NewBus()
	ch := b.Subscribe(1)
	defer b.Unsubscribe(ch)

	want := events.Event{Kind: events.KindNewMessage, Folder: "INBOX", UID: 42}
	b.Publish(want)

	got := <-ch
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBus_Unsubscribe_ClosesChannel(t *testing.T) {
	b := events.NewBus()
	ch := b.Subscribe(1)
	b.Unsubscribe(ch)

	_, open := <-ch
	if open {
		t.Error("channel should be closed after Unsubscribe")
	}
}

func TestBus_NonBlockingOnFullChannel(t *testing.T) {
	b := events.NewBus()
	ch := b.Subscribe(0) // unbuffered — always full
	defer b.Unsubscribe(ch)

	// Must not block.
	b.Publish(events.Event{Kind: events.KindSyncStarted})
}

func TestBus_MultipleSubscribers(t *testing.T) {
	b := events.NewBus()
	ch1 := b.Subscribe(1)
	ch2 := b.Subscribe(1)
	defer b.Unsubscribe(ch1)
	defer b.Unsubscribe(ch2)

	e := events.Event{Kind: events.KindSyncDone}
	b.Publish(e)

	if got := <-ch1; got != e {
		t.Errorf("ch1: got %+v, want %+v", got, e)
	}
	if got := <-ch2; got != e {
		t.Errorf("ch2: got %+v, want %+v", got, e)
	}
}

func TestBus_PublishAfterUnsubscribe(t *testing.T) {
	b := events.NewBus()
	ch := b.Subscribe(1)
	b.Unsubscribe(ch)

	// Publish to an empty bus must not panic.
	b.Publish(events.Event{Kind: events.KindSyncStarted})
}
