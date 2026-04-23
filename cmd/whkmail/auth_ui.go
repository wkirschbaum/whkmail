package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/wkirschbaum/whkmail/internal/dirs"
)

// Shared CLI styles for every whkmail subcommand that prints to stdout.
// Kept in one place so the banner, step numbers, and status markers look
// the same across auth / setup / resync / remove.
var (
	clrAccent = lipgloss.Color("12") // blue
	clrGreen  = lipgloss.Color("10") // green
	clrYellow = lipgloss.Color("11") // yellow
	clrMuted  = lipgloss.Color("8")  // dark grey
	clrWhite  = lipgloss.Color("15")
	clrRed    = lipgloss.Color("9")

	styleBanner = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrWhite).
			Background(clrAccent).
			Padding(0, 2)

	styleSection = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrAccent)

	styleStep = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrYellow)

	stylePath = lipgloss.NewStyle().
			Foreground(clrGreen).
			Bold(true)

	styleURL = lipgloss.NewStyle().
			Foreground(clrAccent).
			Underline(true)

	styleMuted = lipgloss.NewStyle().
			Foreground(clrMuted)

	styleOK = lipgloss.NewStyle().
		Bold(true).
		Foreground(clrGreen)

	styleErr = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrRed)

	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrMuted).
			Padding(0, 1).
			MarginLeft(2)
)

func printBanner() {
	fmt.Println()
	fmt.Println("  " + styleBanner.Render("  whkmail auth  "))
	fmt.Println()
	fmt.Println("  Connecting your Gmail account takes about 5 minutes")
	fmt.Println("  and only needs to be done " + styleStep.Render("once") + ".")
	fmt.Println()
}

func printSetupInstructions(credFile string) {
	w := 56

	section := func(n, title string) {
		fmt.Printf("\n  %s  %s\n\n", styleStep.Render(n), styleSection.Render(title))
	}
	item := func(n, text string) {
		fmt.Printf("    %s  %s\n", styleMuted.Render(n+"."), text)
	}
	note := func(text string) {
		fmt.Printf("       %s\n", styleMuted.Render(text))
	}

	fmt.Println(styleBox.Width(w).Render(
		styleMuted.Render("credentials.json not found — follow these steps\nto create it, then come back here.")))

	section("①", "Create a Google Cloud project")
	item("1", "Open "+hyperlink("https://console.cloud.google.com", styleURL.Render("console.cloud.google.com")))
	item("2", "Create a new project (or pick an existing one)")
	item("3", "Go to "+styleStep.Render("APIs & Services → Library"))
	item("4", "Search for "+styleStep.Render("Gmail API")+" and click "+styleStep.Render("Enable"))

	section("②", "Create OAuth2 credentials")
	item("1", "Go to "+styleStep.Render("APIs & Services → Credentials"))
	item("2", "Click "+styleStep.Render("Create Credentials → OAuth client ID"))
	item("3", "Choose "+styleStep.Render("Desktop app")+" as the application type")
	item("4", "Name it anything, e.g. "+styleMuted.Render(`"mail cli"`)+", and click Create")
	item("5", "Click "+styleStep.Render("Download JSON")+" and save it as:")
	fmt.Println()
	fmt.Println("       " + stylePath.Render(credFile))
	fmt.Println()
	note("Run this first if the directory doesn't exist yet:")
	fmt.Println("       " + styleStep.Render("mkdir -p "+dirs.ConfigDir()))
	fmt.Println()

	section("③", "Add yourself as a test user")
	item("1", "Go to "+styleStep.Render("APIs & Services → OAuth consent screen"))
	item("2", "Under "+styleStep.Render("Test users")+", click "+styleStep.Render("Add users"))
	item("3", "Add your Gmail address and save")
	fmt.Println()
	note("You don't need to publish the app.")
	note("Test users have full access with no review required.")

	fmt.Println()
	fmt.Println("  " + styleMuted.Render(strings.Repeat("─", w+2)))
}

func printAuthPrompt(authURL string) {
	fmt.Printf("\n  %s\n\n", styleOK.Render("✓ credentials.json found — opening Google's authorization page"))
	fmt.Printf("  %s\n", styleMuted.Render("If your browser doesn't open, visit this URL:"))
	fmt.Printf("  %s\n\n", styleURL.Render(authURL))
}

func printSuccess(email string) {
	fmt.Printf("\r  %s\n", styleOK.Render("✓ Authorized as "+email))
}

// authSuccessPage is shown in the browser after a successful redirect.
const authSuccessPage = `<!DOCTYPE html>
<html>
<head><title>mail — authorized</title>
<style>
  body { font-family: -apple-system, sans-serif; display: flex; align-items: center;
         justify-content: center; height: 100vh; margin: 0; background: #0f172a; color: #e2e8f0; }
  .card { text-align: center; padding: 2rem 3rem; border: 1px solid #334155; border-radius: 12px; }
  h1 { font-size: 1.5rem; margin: 0 0 .5rem; color: #38bdf8; }
  p  { margin: 0; color: #94a3b8; }
</style>
</head>
<body>
  <div class="card">
    <h1>✓ Authorized</h1>
    <p>You can close this tab and return to the terminal.</p>
  </div>
</body>
</html>`

func openBrowser(ctx context.Context, url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	return exec.CommandContext(ctx, cmd, args...).Start()
}

func hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func randomState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
