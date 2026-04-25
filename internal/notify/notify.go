package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/wkirschbaum/whkmail/internal/events"
)

type Notifier interface {
	Send(subject, from string) error
}

// Debounce is the quiet window we wait for after the last KindNewMessage
// before firing a single desktop notification. Mirrors the TUI's status
// bar debounce so a burst of arrivals collapses into one popup instead
// of flooding the notification centre.
const Debounce = 300 * time.Millisecond

// Run listens on the event bus and sends one desktop notification per
// debounced burst of new-mail arrivals. A burst of N events within the
// debounce window collapses into a single "N new messages" summary.
// At most one Send runs at a time — if the previous notification is still
// in-flight (e.g. a slow D-Bus call) the burst is dropped rather than
// stacking up goroutines.
func Run(ctx context.Context, bus *events.Bus, n Notifier) {
	ch := bus.Subscribe(16)
	defer bus.Unsubscribe(ch)

	var pending []events.Event
	var timer *time.Timer
	var timerC <-chan time.Time
	var inflight atomic.Bool

	arm := func() {
		if timer == nil {
			timer = time.NewTimer(Debounce)
			timerC = timer.C
			return
		}
		if !timer.Stop() {
			// Drain a value that may already be sitting in the channel
			// so Reset starts from a clean state.
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(Debounce)
		// timerC was cleared by the last fire — re-enable the select.
		timerC = timer.C
	}

	fire := func() {
		if len(pending) == 0 {
			return
		}
		// If a previous Send is still running, drop this burst rather than
		// spawning another goroutine. The next event will re-arm the debounce.
		if !inflight.CompareAndSwap(false, true) {
			pending = nil
			timerC = nil
			return
		}
		subject, from := Summarise(pending)
		pending = nil
		timerC = nil
		go func() {
			defer inflight.Store(false)
			if err := n.Send(subject, from); err != nil {
				slog.Warn("notification failed", "err", err)
			}
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Kind != events.KindNewMessage {
				continue
			}
			pending = append(pending, e)
			arm()
		case <-timerC:
			fire()
		}
	}
}

// Summarise renders the notification title and body for a burst. One
// arrival uses the message's own subject/from; multiple collapse into a
// count with the latest as the body. Pure function so tests can assert
// the shape without wiring a bus.
func Summarise(evs []events.Event) (subject, from string) {
	n := len(evs)
	if n == 0 {
		return "", ""
	}
	latest := evs[n-1]
	if n == 1 {
		return latest.Subject, latest.From
	}
	return fmt.Sprintf("%d new messages", n),
		fmt.Sprintf("latest: %s — %s", latest.Subject, latest.From)
}
