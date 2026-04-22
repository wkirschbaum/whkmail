package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
	return newClientWithTransport(unixTransport(dirs.SocketFile()))
}

// newClientWithTransport constructs a Client with a custom transport.
// Swap the transport here to switch from Unix socket to TCP without touching call sites.
func newClientWithTransport(t http.RoundTripper) *Client {
	return &Client{
		base: "http://whkmaild",
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

func (c *Client) Messages(ctx context.Context, folder string) (*types.MessagesResponse, error) {
	return getJSON[types.MessagesResponse](ctx, c, "/folders/"+folder+"/messages")
}

func (c *Client) Message(ctx context.Context, folder string, uid uint32) (*types.MessageResponse, error) {
	return getJSON[types.MessageResponse](ctx, c, fmt.Sprintf("/folders/%s/messages/%d", folder, uid))
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
