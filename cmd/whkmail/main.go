package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/tui"
)

func main() {
	if len(os.Args) > 1 {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var err error
		switch os.Args[1] {
		case "setup":
			err = runSetup(ctx)
		case "auth":
			err = runAuth(ctx)
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n\nUsage: whkmail [setup|auth]\n", os.Args[1])
			os.Exit(1)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	client := tui.NewClient()
	m := tui.NewModel(client)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func ensureDaemon() error {
	if socketAlive() {
		return nil
	}

	fmt.Fprintln(os.Stderr, "Daemon not running, starting whkmaild…")
	if err := startDaemon(); err != nil {
		return err
	}

	// Poll for up to 10s.
	for range 100 {
		time.Sleep(100 * time.Millisecond)
		if socketAlive() {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for daemon to start")
}

func socketAlive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", dirs.SocketFile())
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func startDaemon() error {
	// Prefer the platform service manager so the daemon stays under its supervision.
	switch runtime.GOOS {
	case "linux":
		svcFile := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "whkmaild.service")
		if _, err := os.Stat(svcFile); err == nil {
			//nolint:noctx
			if err := exec.Command("systemctl", "--user", "start", "whkmaild.service").Run(); err == nil {
				return nil
			}
		}
	case "darwin":
		//nolint:noctx
		if err := exec.Command("launchctl", "start", "com.whkmail.daemon").Run(); err == nil {
			return nil
		}
	}
	return spawnDirect()
}

func spawnDirect() error {
	bin, err := daemonBinPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("whkmaild not found at %s — run whkmail setup", bin)
	}
	//nolint:noctx
	return exec.Command(bin).Start()
}
