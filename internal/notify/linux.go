//go:build linux

package notify

import (
	"fmt"

	"github.com/godbus/dbus/v5"
)

type LinuxNotifier struct {
	conn *dbus.Conn
}

func NewPlatform() (Notifier, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("dbus session bus: %w", err)
	}
	return &LinuxNotifier{conn: conn}, nil
}

func (n *LinuxNotifier) Send(subject, from string) error {
	obj := n.conn.Object(
		"org.freedesktop.Notifications",
		"/org/freedesktop/Notifications",
	)
	// args: app_name, replaces_id, app_icon, summary, body, actions, hints, expire_timeout
	call := obj.Call(
		"org.freedesktop.Notifications.Notify", 0,
		"whkmail", uint32(0), "mail-unread",
		subject, from,
		[]string{}, map[string]dbus.Variant{},
		int32(8000),
	)
	return call.Err
}
