package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// tea.Cmd factories. One per daemon endpoint. The shared shape is: build a
// short-timeout context, call the client method, map the result into a typed
// msg. Errors route to msgErr which the Update loop turns into the error
// banner — except prefetchMessage which deliberately swallows its error so a
// flaky background fetch doesn't trash the foreground UI.

const requestTimeout = 30 * time.Second

func fetchStatus(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		s, err := c.Status(ctx)
		if err != nil {
			return msgErr{err}
		}
		return msgStatus(*s)
	}
}

func fetchMessages(c *Client, account, folder string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		r, err := c.Messages(ctx, account, folder)
		if err != nil {
			return msgErr{err}
		}
		return msgMessages(*r)
	}
}

func fetchMessage(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		r, err := c.Message(ctx, account, folder, uid)
		if err != nil {
			return msgErr{err}
		}
		return msgMessage(*r)
	}
}

// prefetchMessage warms a body in the background. Errors are swallowed: a
// flaky prefetch must not render an error banner over an otherwise working
// TUI. Success merges into the local message cache via msgPrefetched.
func prefetchMessage(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		r, err := c.Message(ctx, account, folder, uid)
		if err != nil {
			return msgPrefetched{}
		}
		cp := r.Message
		return msgPrefetched{message: &cp}
	}
}

func markReadCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := c.MarkRead(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgMarkedRead{account: account, folder: folder, uid: uid}
	}
}

func markUnreadCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := c.MarkUnread(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgMarkedUnread{account: account, folder: folder, uid: uid}
	}
}

func trashCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := c.Trash(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgTrashed{account: account, folder: folder, uid: uid}
	}
}

func permanentDeleteCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := c.PermanentDelete(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgDeleted{account: account, folder: folder, uid: uid}
	}
}
