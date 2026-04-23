package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// Client talks to the whkmaild daemon over HTTP.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a Client that connects via the whkmaild Unix socket.
func NewClient() *Client {
	return newClient("http://whkmaild", unixTransport(dirs.SocketFile()))
}

// newClient constructs a Client with a custom base URL and transport. Tests
// point base at an httptest server; production uses the Unix socket wrapper
// above where base is a fixed authority string.
func newClient(base string, t http.RoundTripper) *Client {
	return &Client{
		base: base,
		http: &http.Client{Transport: t},
	}
}

// unixTransport returns an http.RoundTripper that dials the given Unix socket path.
func unixTransport(sockPath string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}
}

func (c *Client) Status(ctx context.Context) (*types.StatusResponse, error) {
	return getJSON[types.StatusResponse](ctx, c, "/status")
}

func (c *Client) Messages(ctx context.Context, account, folder string) (*types.MessagesResponse, error) {
	path := "/accounts/" + url.PathEscape(account) + "/folders/" + url.PathEscape(folder) + "/messages"
	return getJSON[types.MessagesResponse](ctx, c, path)
}

func (c *Client) Message(ctx context.Context, account, folder string, uid uint32) (*types.MessageResponse, error) {
	path := fmt.Sprintf("/accounts/%s/folders/%s/messages/%d",
		url.PathEscape(account), url.PathEscape(folder), uid)
	return getJSON[types.MessageResponse](ctx, c, path)
}

// MarkRead asks the daemon to flag a message as seen.
func (c *Client) MarkRead(ctx context.Context, account, folder string, uid uint32) error {
	return c.post(ctx, account, folder, uid, "read")
}

// MarkUnread asks the daemon to clear the \Seen flag on a message.
func (c *Client) MarkUnread(ctx context.Context, account, folder string, uid uint32) error {
	return c.post(ctx, account, folder, uid, "unread")
}

// Trash moves a message to the account's Trash mailbox.
func (c *Client) Trash(ctx context.Context, account, folder string, uid uint32) error {
	return c.post(ctx, account, folder, uid, "trash")
}

// PermanentDelete permanently expunges a message (use from the Trash folder).
func (c *Client) PermanentDelete(ctx context.Context, account, folder string, uid uint32) error {
	return c.post(ctx, account, folder, uid, "delete")
}

// Send hands a composed message to the daemon for SMTP submission.
// The daemon's 60-second fetch/send timeout applies; the returned error
// carries the HTTP body on non-2xx so the TUI can surface specific
// failures (e.g. "no sender configured" on 503).
func (c *Client) Send(ctx context.Context, account string, req types.SendRequest) error {
	path := "/accounts/" + url.PathEscape(account) + "/send"
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// RemoveAccount deregisters an account from the running daemon. Cleanup of
// on-disk state (token/DB/config) is the caller's responsibility.
func (c *Client) RemoveAccount(ctx context.Context, account string) error {
	path := "/accounts/" + url.PathEscape(account)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("remove account: HTTP %d", resp.StatusCode)
	}
	return nil
}

// post dispatches a fire-and-forget mutation on a specific message.
func (c *Client) post(ctx context.Context, account, folder string, uid uint32, action string) error {
	path := fmt.Sprintf("/accounts/%s/folders/%s/messages/%d/%s",
		url.PathEscape(account), url.PathEscape(folder), uid, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: HTTP %d", action, resp.StatusCode)
	}
	return nil
}

// StreamEvents connects to /events and sends parsed events to ch until ctx is cancelled.
func (c *Client) StreamEvents(ctx context.Context, ch chan<- events.Event) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/events", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e events.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			continue
		}
		select {
		case ch <- e:
		case <-ctx.Done():
			return nil
		}
	}
	return scanner.Err()
}

func getJSON[T any](ctx context.Context, c *Client, path string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}
