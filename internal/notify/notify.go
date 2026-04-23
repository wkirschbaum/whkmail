package notify

import (
	"context"
	"log/slog"

	"github.com/wkirschbaum/whkmail/internal/events"
)

type Notifier interface {
	Send(subject, from string) error
}

// Run listens on the event bus and sends desktop notifications for new
// messages. Each Send runs in its own goroutine so a hung notifier (e.g. a
// blocked D-Bus call) cannot stall Run's response to ctx cancellation.
func Run(ctx context.Context, bus *events.Bus, n Notifier) {
	ch := bus.Subscribe(16)
	defer bus.Unsubscribe(ch)
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
			go func(e events.Event) {
				if err := n.Send(e.Subject, e.From); err != nil {
					slog.Warn("notification failed", "err", err)
				}
			}(e)
		}
	}
}
