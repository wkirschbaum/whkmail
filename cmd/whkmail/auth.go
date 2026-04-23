package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/oauth"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// runAuth walks the user through the one-time OAuth2 setup: it prints
// instructions if credentials.json is missing, then hands off to the
// loopback-redirect flow in performOAuth.
func runAuth(ctx context.Context) error {
	printBanner()

	credFile := dirs.CredentialsFile()
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

	return performOAuth(ctx)
}

// performOAuth runs the OAuth2 loopback redirect flow and saves the token.
func performOAuth(ctx context.Context) error {
	cfg, err := oauth.LoadSharedConfig(oauth.GmailScope, oauth.UserinfoEmailScope)
	if err != nil {
		return err
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("bind callback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
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

	email, err := oauth.FetchEmail(ctx, cfg.Client(ctx, tok))
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
	raw, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	if err := os.WriteFile(dirs.TokenFile(), raw, 0o600); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	printSuccess(email)
	return nil
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
