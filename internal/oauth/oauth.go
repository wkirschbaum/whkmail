// Package oauth centralises the Google OAuth2 plumbing used by both the
// IMAP syncer (today) and the future SMTP sender. It owns:
//
//   - The Gmail scope string.
//   - Parsing credentials.json into an *oauth2.Config.
//   - Loading and persisting account tokens (account-scoped with a
//     legacy-path fallback).
//   - Wrapping an oauth2.TokenSource into a "give me a fresh access token"
//     closure that auto-persists refreshed tokens.
//   - Fetching the authorised account's email from Google's userinfo
//     endpoint — used by the auth wizard to populate config.json.
//
// Callers pass the account email in; this package does not care whether the
// caller is in daemon, TUI, or test code.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/wkirschbaum/whkmail/internal/dirs"
)

// GmailScope is the Gmail OAuth2 scope covering IMAP + SMTP access.
// Gmail requires the same scope for both reading and sending, so one scope
// string serves both the Syncer and the future SMTP Sender.
const GmailScope = "https://mail.google.com/"

// UserinfoEmailScope is the OAuth2 scope needed to read the authorized
// account's email address from Google's userinfo endpoint during setup.
const UserinfoEmailScope = "email"

// Userinfo endpoint used to learn which email the granted token belongs to.
const userinfoURL = "https://www.googleapis.com/oauth2/v1/userinfo"

// LoadConfig reads the credentials.json for an account (account-scoped path
// first, shared fallback) and parses it into an *oauth2.Config with the
// given scopes applied.
func LoadConfig(email string, scopes ...string) (*oauth2.Config, error) {
	credFile := dirs.AccountCredentialsFile(email) // falls back to shared
	b, err := os.ReadFile(credFile)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return cfg, nil
}

// LoadSharedConfig parses the shared credentials.json (pre-account-setup,
// when there is no account email yet).
func LoadSharedConfig(scopes ...string) (*oauth2.Config, error) {
	b, err := os.ReadFile(dirs.CredentialsFile())
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return cfg, nil
}

// LoadToken finds the OAuth2 token for an account, trying the account-scoped
// path first and falling back to the legacy single-account path for existing
// installations.
func LoadToken(email string) (*oauth2.Token, error) {
	for _, path := range []string{dirs.AccountTokenFile(email), dirs.TokenFile()} {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var tok oauth2.Token
		if err := json.Unmarshal(b, &tok); err != nil {
			return nil, err
		}
		return &tok, nil
	}
	return nil, fmt.Errorf("no token found — run: whkmail auth")
}

// SaveToken persists a refreshed token. Prefers the account-scoped path when
// that file already exists; otherwise falls back to the legacy shared path
// for backward compatibility with single-account installs.
func SaveToken(email string, tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	path := dirs.AccountTokenFile(email)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = dirs.TokenFile()
	}
	return os.WriteFile(path, b, 0o600)
}

// TokenFn returns a closure that yields a fresh access token for the given
// account on each call. The underlying oauth2.TokenSource handles refresh;
// refreshed tokens are persisted back to disk so a long-running daemon
// survives restarts without re-running the auth wizard.
func TokenFn(ctx context.Context, email string) (func(context.Context) (string, error), error) {
	cfg, err := LoadConfig(email, GmailScope)
	if err != nil {
		return nil, err
	}
	tok, err := LoadToken(email)
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}
	ts := cfg.TokenSource(ctx, tok)
	return func(ctx context.Context) (string, error) {
		t, err := ts.Token()
		if err != nil {
			return "", err
		}
		if err := SaveToken(email, t); err != nil {
			slog.Warn("failed to persist refreshed token", "account", email, "err", err)
		}
		return t.AccessToken, nil
	}, nil
}

// FetchEmail asks Google's userinfo endpoint which email the given OAuth2
// http.Client is authorised as. Used by the auth wizard to populate
// config.json without prompting the user.
func FetchEmail(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
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
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo request failed (HTTP %d): %s", resp.StatusCode, body)
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
