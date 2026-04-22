//go:build darwin

package notify

import (
	"fmt"
	"os/exec"
)

type MacOSNotifier struct{}

func NewPlatform() (Notifier, error) { return &MacOSNotifier{}, nil }

func (n *MacOSNotifier) Send(subject, from string) error {
	script := fmt.Sprintf(
		`display notification %q with title "New Mail" subtitle %q`,
		from, subject,
	)
	return exec.Command("osascript", "-e", script).Run()
}
