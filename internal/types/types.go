package types

import "time"

// DefaultMarkReadDelay is the fallback delay before the TUI asks the daemon to
// flag an opened message as seen.
const DefaultMarkReadDelay = 2 * time.Second

type Folder struct {
	Name         string `json:"name"`
	Delimiter    string `json:"delimiter"`
	MessageCount uint32 `json:"message_count"`
	Unread       uint32 `json:"unread"`
}

type Message struct {
	UID         uint32    `json:"uid"`
	Folder      string    `json:"folder"`
	Subject     string    `json:"subject"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	Date        time.Time `json:"date"`
	Unread      bool      `json:"unread"`
	Flagged     bool      `json:"flagged"`
	Answered    bool      `json:"answered,omitempty"`
	Draft       bool      `json:"draft,omitempty"`
	BodyText    string    `json:"body_text,omitempty"`
	BodyFetched bool      `json:"body_fetched,omitempty"`
	MessageID   string    `json:"message_id,omitempty"`
	InReplyTo   string    `json:"in_reply_to,omitempty"`
}

// AccountConfig holds the configuration for a single mail account.
type AccountConfig struct {
	Email    string `json:"email"`
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
}

// Config is the top-level configuration file. It supports both a legacy
// single-account format (top-level email/imap_host/imap_port) and the newer
// multi-account format (accounts array). ResolvedAccounts always returns a
// non-empty slice.
type Config struct {
	// Legacy single-account fields — still read when Accounts is empty.
	IMAPHost string `json:"imap_host,omitempty"`
	IMAPPort int    `json:"imap_port,omitempty"`
	Email    string `json:"email,omitempty"`

	Accounts []AccountConfig `json:"accounts,omitempty"`

	// MarkReadDelaySeconds controls how long a message must stay open in the
	// TUI before it is flagged as seen. 0 means use DefaultMarkReadDelay.
	MarkReadDelaySeconds int `json:"mark_read_delay_seconds,omitempty"`
}

// MarkReadDelay returns the configured delay, falling back to the default.
func (c Config) MarkReadDelay() time.Duration {
	if c.MarkReadDelaySeconds <= 0 {
		return DefaultMarkReadDelay
	}
	return time.Duration(c.MarkReadDelaySeconds) * time.Second
}

// ResolvedAccounts returns the configured accounts, falling back to the legacy
// top-level fields when the accounts array is absent.
func (c Config) ResolvedAccounts() []AccountConfig {
	if len(c.Accounts) > 0 {
		return c.Accounts
	}
	if c.Email != "" {
		return []AccountConfig{{Email: c.Email, IMAPHost: c.IMAPHost, IMAPPort: c.IMAPPort}}
	}
	return nil
}

// Wire types for the REST API.

// AccountStatus is the per-account payload inside StatusResponse.
type AccountStatus struct {
	Account string   `json:"account"`
	Syncing bool     `json:"syncing"`
	Folders []Folder `json:"folders"`
}

type StatusResponse struct {
	Accounts []AccountStatus `json:"accounts"`
}

type MessagesResponse struct {
	Folder   string    `json:"folder"`
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
}

type MessageResponse struct {
	Message Message `json:"message"`
}

// SendRequest is the wire payload for POST /accounts/{account}/send.
// Mirrors smtp.Message (minus derived fields like Date and MessageID)
// so the TUI can construct it without importing internal/smtp.
type SendRequest struct {
	To         []string `json:"to,omitempty"`
	Cc         []string `json:"cc,omitempty"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`
	// SourceFolder is the folder the replied-to message lives in. The
	// daemon re-syncs this folder after a successful submission so the
	// \Answered flag set by the server appears in the TUI right away
	// instead of waiting for the next IDLE-driven refresh.
	SourceFolder string `json:"source_folder,omitempty"`
}
