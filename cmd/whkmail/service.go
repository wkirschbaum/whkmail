package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func installService() error {
	daemonPath, err := daemonBinPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(daemonPath); err != nil {
		return fmt.Errorf("whkmaild not found at %s — build it first", daemonPath)
	}

	switch runtime.GOOS {
	case "linux":
		return installSystemd(daemonPath)
	case "darwin":
		return installLaunchd(daemonPath)
	default:
		return fmt.Errorf("automatic service install not supported on %s — start whkmaild manually", runtime.GOOS)
	}
}

func installSystemd(daemonPath string) error {
	dir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create systemd dir: %w", err)
	}

	svcFile := filepath.Join(dir, "whkmaild.service")
	content := fmt.Sprintf(systemdUnit, daemonPath)
	if err := os.WriteFile(svcFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}

	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "whkmaild.service"},
		{"--user", "restart", "whkmaild.service"},
	} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil { //nolint:noctx
			return fmt.Errorf("systemctl %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

func installLaunchd(daemonPath string) error {
	dir := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	plistFile := filepath.Join(dir, "com.whkmail.daemon.plist")
	content := fmt.Sprintf(launchdPlist, daemonPath)
	if err := os.WriteFile(plistFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	label := "com.whkmail.daemon"
	// Unload first in case it was previously loaded (upgrade path).
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), plistFile).Run()                                       //nolint:noctx
	if out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistFile).CombinedOutput(); err != nil { //nolint:noctx
		return fmt.Errorf("launchctl bootstrap %s: %w\n%s", label, err, out)
	}
	return nil
}

func daemonBinPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(exe, "whkmail") + "whkmaild", nil
}

const systemdUnit = `[Unit]
Description=whkmail Gmail sync daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.whkmail.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
</dict>
</plist>
`
