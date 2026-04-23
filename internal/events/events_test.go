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

func TestNewMessageEvent(t *testing.T) {
	got := events.NewMessageEvent("me@ex", "INBOX", 42, "Hi", "alice@ex")
	want := events.Event{
		Kind: events.KindNewMessage, Account: "me@ex", Folder: "INBOX",
		UID: 42, Subject: "Hi", From: "alice@ex",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBodyReadyEvent_SuccessAndFailure(t *testing.T) {
	ok := events.BodyReadyEvent("me@ex", "INBOX", 5, "")
	if ok.Error != "" {
		t.Errorf("success should carry no error, got %q", ok.Error)
	}
	if ok.Kind != events.KindBodyReady {
		t.Errorf("kind = %v, want body_ready", ok.Kind)
	}

	fail := events.BodyReadyEvent("me@ex", "INBOX", 5, "imap dropped us")
	if fail.Error != "imap dropped us" {
		t.Errorf("error not propagated: %q", fail.Error)
	}
}

func TestSyncLifecycleEvents(t *testing.T) {
	start := events.SyncStartedEvent("me@ex")
	if start.Kind != events.KindSyncStarted || start.Account != "me@ex" {
		t.Errorf("start: %+v", start)
	}
	if start.Folder != "" || start.Current != 0 || start.Total != 0 {
		t.Errorf("start should not carry progress fields: %+v", start)
	}

	prog := events.SyncProgressEvent("me@ex", "Sent", 3, 12)
	if prog.Kind != events.KindSyncProgress || prog.Current != 3 || prog.Total != 12 {
		t.Errorf("progress: %+v", prog)
	}

	done := events.SyncDoneEvent("me@ex")
	if done.Kind != events.KindSyncDone {
		t.Errorf("done kind: %v", done.Kind)
	}
	if done.Folder != "" || done.Current != 0 {
		t.Errorf("done should not carry progress fields: %+v", done)
	}
}
