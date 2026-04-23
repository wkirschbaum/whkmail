package notify_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/notify"
)

type recording struct {
	mu    sync.Mutex
	calls []call
}

type call struct{ subject, from string }

func (r *recording) Send(subject, from string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{subject: subject, from: from})
	return nil
}

func (r *recording) snapshot() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]call, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestSummarise(t *testing.T) {
	if s, f := notify.Summarise(nil); s != "" || f != "" {
		t.Errorf("empty: got (%q,%q)", s, f)
	}

	single := []events.Event{{Subject: "Hi", From: "alice@example.com"}}
	if s, f := notify.Summarise(single); s != "Hi" || f != "alice@example.com" {
		t.Errorf("single: got (%q,%q)", s, f)
	}

	many := []events.Event{
		{Subject: "a", From: "x"},
		{Subject: "b", From: "y"},
		{Subject: "c", From: "z"},
	}
	s, f := notify.Summarise(many)
	if !strings.HasPrefix(s, "3 new") {
		t.Errorf("many subject: got %q", s)
	}
	if !strings.Contains(f, "c") || !strings.Contains(f, "z") {
		t.Errorf("many body: got %q", f)
	}
}

func TestRun_Debounces_BurstIntoSingleNotification(t *testing.T) {
	bus := events.NewBus()
	rec := &recording{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		notify.Run(ctx, bus, rec)
		close(done)
	}()
	// Wait for Subscribe to register.
	time.Sleep(30 * time.Millisecond)

	// Three arrivals in rapid succession — should coalesce.
	for i, subj := range []string{"a", "b", "c"} {
		bus.Publish(events.Event{
			Kind: events.KindNewMessage, Subject: subj, From: subj + "@ex",
		})
		if i < 2 {
			// Stay well under the debounce window.
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Wait long enough for the debounce to fire plus notifier goroutine.
	time.Sleep(notify.Debounce + 200*time.Millisecond)

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 summary notification, got %d: %+v", len(calls), calls)
	}
	if !strings.HasPrefix(calls[0].subject, "3 new") {
		t.Errorf("burst subject: got %q", calls[0].subject)
	}
}

func TestRun_SpacedEvents_FireSeparately(t *testing.T) {
	bus := events.NewBus()
	rec := &recording{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go notify.Run(ctx, bus, rec)
	// Give Run's goroutine time to subscribe; otherwise early Publish
	// calls land before the channel exists and get silently dropped.
	time.Sleep(30 * time.Millisecond)

	bus.Publish(events.Event{Kind: events.KindNewMessage, Subject: "first", From: "a@ex"})
	time.Sleep(notify.Debounce + 200*time.Millisecond)
	bus.Publish(events.Event{Kind: events.KindNewMessage, Subject: "second", From: "b@ex"})
	time.Sleep(notify.Debounce + 200*time.Millisecond)

	calls := rec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("expected 2 separate notifications, got %d: %+v", len(calls), calls)
	}
	if calls[0].subject != "first" || calls[1].subject != "second" {
		t.Errorf("order / subjects wrong: %+v", calls)
	}
}

func TestRun_IgnoresNonNewMessageEvents(t *testing.T) {
	bus := events.NewBus()
	rec := &recording{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go notify.Run(ctx, bus, rec)
	// Give Run's goroutine time to subscribe; otherwise early Publish
	// calls land before the channel exists and get silently dropped.
	time.Sleep(30 * time.Millisecond)

	bus.Publish(events.Event{Kind: events.KindSyncStarted})
	bus.Publish(events.Event{Kind: events.KindSyncDone})
	bus.Publish(events.Event{Kind: events.KindBodyReady})
	time.Sleep(notify.Debounce + 200*time.Millisecond)

	if n := len(rec.snapshot()); n != 0 {
		t.Errorf("expected 0 notifications, got %d", n)
	}
}
