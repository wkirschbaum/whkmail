package imap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomail "github.com/emersion/go-message/mail"
	html2text "github.com/k3a/html2text"
)

// Body / MIME operations on an account. Every method here goes through
// withOpsConn so a burst of calls shares one cached IMAP session and backs
// off on sustained failures.

// FetchBody fetches the full body of a message from the server and caches it.
func (s *Syncer) FetchBody(ctx context.Context, folder string, uid uint32) (string, error) {
	var text string
	err := s.withOpsConn(ctx, func(c *imapclient.Client) error {
		if _, err := c.Select(folder, &goimap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return fmt.Errorf("select %s: %w", folder, err)
		}
		section := &goimap.FetchItemBodySection{}
		msgs, err := c.Fetch(goimap.UIDSetNum(goimap.UID(uid)), &goimap.FetchOptions{
			BodySection: []*goimap.FetchItemBodySection{section},
		}).Collect()
		if err != nil {
			return fmt.Errorf("fetch body: %w", err)
		}
		if len(msgs) == 0 {
			text = ""
			return nil
		}
		text = extractText(msgs[0].FindBodySection(section))
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := s.store.SetBodyText(ctx, folder, uid, text); err != nil {
		slog.Warn("cache body", "uid", uid, "err", err)
	}
	return text, nil
}

// MarkRead adds the \Seen flag on the server and marks the message
// unread=false in the cache.
func (s *Syncer) MarkRead(ctx context.Context, folder string, uid uint32) error {
	err := s.withOpsConn(ctx, func(c *imapclient.Client) error {
		if _, err := c.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select %s: %w", folder, err)
		}
		if err := c.Store(goimap.UIDSetNum(goimap.UID(uid)), &goimap.StoreFlags{
			Op:     goimap.StoreFlagsAdd,
			Flags:  []goimap.Flag{goimap.FlagSeen},
			Silent: true,
		}, nil).Close(); err != nil {
			return fmt.Errorf("store seen: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.store.MarkSeen(ctx, folder, uid)
}

// extractText parses a raw RFC 2822 message and returns the best plain-text body.
// Prefers text/plain; falls back to converted text/html.
func extractText(raw []byte) string {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if mr == nil {
		// CreateReader only returns nil on a hard parse failure; charset errors
		// still yield a usable (partial) reader. Nothing to recover here.
		slog.Debug("extractText: failed to parse message", "err", err)
		return ""
	}
	defer func() { _ = mr.Close() }()

	var plain, htmlBody string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		h, ok := p.Header.(*gomail.InlineHeader)
		if !ok {
			continue
		}

		ct, _, _ := h.ContentType()
		body, _ := io.ReadAll(p.Body)
		// Normalise CRLF → LF so terminals render correctly.
		text := strings.ReplaceAll(string(body), "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")

		switch ct {
		case "text/plain":
			if plain == "" {
				plain = strings.TrimSpace(text)
			}
		case "text/html":
			if htmlBody == "" {
				htmlBody = strings.TrimSpace(html2text.HTML2Text(text))
			}
		}
	}

	if plain != "" {
		return plain
	}
	return htmlBody
}
