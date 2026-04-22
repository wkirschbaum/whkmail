package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	mailsync "github.com/wkirschbaum/whkmail/internal/sync"
	"github.com/wkirschbaum/whkmail/internal/types"
)


// -- styles --

var (
	clrAccent  = lipgloss.Color("12")  // blue
	clrGreen   = lipgloss.Color("10")  // green
	clrYellow  = lipgloss.Color("11")  // yellow
	clrMuted   = lipgloss.Color("8")   // dark grey
	clrWhite   = lipgloss.Color("15")
	clrRed     = lipgloss.Color("9")

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

func runAuth(ctx context.Context) error {
	printBanner()

	credFile := dirs.ConfigDir() + "/credentials.json"
	_, err := os.Stat(credFile)
	credsMissing := os.IsNotExist(err)

	if credsMissing {
		printSetupInstructions(credFile)
		fmt.Printf("\n%s\n", styleMuted.Render("Press Enter once credentials.json is in place…"))
		_, _ = fmt.Scanln()

		if _, err := os.Stat(credFile); err != nil {
			return fmt.Errorf("%s\n  Expected: %s",
				styleErr.Render("credentials.json not found"),
				stylePath.Render(credFile),
			)
		}
	}

	return performOAuth(ctx, credFile)
}

// performOAuth runs the OAuth2 loopback redirect flow and saves the token.
func performOAuth(ctx context.Context, credFile string) error {
	b, err := os.ReadFile(credFile)
	if err != nil {
		return fmt.Errorf("read credentials.json: %w", err)
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("bind callback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	cfg, err := google.ConfigFromJSON(b, mailsync.GmailIMAPScope, "email")
	if err != nil {
		return fmt.Errorf("parse credentials.json: %w", err)
	}
	cfg.RedirectURL = redirectURI

	state := randomState()
	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			errCh <- fmt.Errorf("state mismatch — possible CSRF")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			msg := r.URL.Query().Get("error")
			http.Error(w, "authorization denied", http.StatusBadRequest)
			errCh <- fmt.Errorf("authorization denied: %s", msg)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintln(w, authSuccessPage)
		codeCh <- code
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
		}
	}()

	printAuthPrompt(authURL)

	if err := openBrowser(ctx, authURL); err != nil {
		fmt.Printf("  %s\n\n", styleMuted.Render("(could not open browser automatically — copy the URL above)"))
	} else {
		fmt.Printf("  %s\n\n", styleMuted.Render("Browser opened. Complete the authorization there."))
	}

	fmt.Printf("  %s", styleMuted.Render("Waiting for Google's response…"))

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		fmt.Println()
		return err
	case <-ctx.Done():
		fmt.Println()
		return ctx.Err()
	case <-time.After(5 * time.Minute):
		fmt.Println()
		return fmt.Errorf("timed out after 5 minutes")
	}

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		fmt.Println()
		return fmt.Errorf("exchange code: %w", err)
	}

	email, err := fetchEmail(ctx, cfg.Client(ctx, tok))
	if err != nil {
		fmt.Println()
		return fmt.Errorf("fetch account email: %w", err)
	}

	if err := writeConfig(email); err != nil {
		return err
	}

	if err := os.MkdirAll(dirs.StateDir(), 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(tok)
	if err := os.WriteFile(dirs.TokenFile(), raw, 0o600); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	printSuccess(email)
	return nil
}

func fetchEmail(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v1/userinfo", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}
	if info.Email == "" {
		return "", fmt.Errorf("userinfo response contained no email")
	}
	return info.Email, nil
}

func writeConfig(email string) error {
	cfg := types.Config{IMAPHost: "imap.gmail.com", IMAPPort: 993, Email: email}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(dirs.ConfigFile(), raw, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// -- print helpers --

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
