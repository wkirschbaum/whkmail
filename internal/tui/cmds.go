package tui

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wkirschbaum/whkmail/internal/types"
)

// tea.Cmd factories. One per daemon endpoint. The shared shape is: build a
// short-timeout context, call the client method, map the result into a typed
// msg. Errors route to msgErr which the Update loop turns into the error
// banner — except prefetchMessage which deliberately swallows its error so a
// flaky background fetch doesn't trash the foreground UI.

const (
	requestTimeout = 30 * time.Second
	sendTimeout    = 90 * time.Second
)

// requestCtx returns a context pre-armed with requestTimeout. Every
// outbound daemon call uses this so the timeout is defined in one place.
func requestCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), requestTimeout)
}

// sendCtx returns a context pre-armed with sendTimeout. Used for SMTP
// submission which is slower than ordinary IMAP ops.
func sendCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), sendTimeout)
}

func fetchStatus(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := requestCtx()
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
		ctx, cancel := requestCtx()
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
		ctx, cancel := requestCtx()
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
		ctx, cancel := requestCtx()
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
		ctx, cancel := requestCtx()
		defer cancel()
		if err := c.MarkRead(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgMarkedRead{account: account, folder: folder, uid: uid}
	}
}

// autoMarkReadCmd is the silent auto-mark-read variant used by tickMarkRead.
// Errors are swallowed — a background flag update that times out or hits a
// transient network issue must not interrupt the user with an error overlay.
// The next sync will reconcile the server-side flag.
func autoMarkReadCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := requestCtx()
		defer cancel()
		if err := c.MarkRead(ctx, account, folder, uid); err != nil {
			return nil
		}
		return msgMarkedRead{account: account, folder: folder, uid: uid}
	}
}

func markUnreadCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := requestCtx()
		defer cancel()
		if err := c.MarkUnread(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgMarkedUnread{account: account, folder: folder, uid: uid}
	}
}

// saveStyleCmd persists the chosen input style. Kept as a tea.Cmd so the
// key handler stays pure and disk failures surface through the usual
// msgErr path instead of being silently dropped.
func saveStyleCmd(style InputStyle) tea.Cmd {
	return func() tea.Msg {
		if err := saveInputStyle(style); err != nil {
			return msgErr{err}
		}
		return nil
	}
}

func markSpamCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := requestCtx()
		defer cancel()
		if err := c.MarkSpam(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgSpammed{account: account, folder: folder, uid: uid}
	}
}

func trashCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := requestCtx()
		defer cancel()
		if err := c.Trash(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgTrashed{account: account, folder: folder, uid: uid}
	}
}

func permanentDeleteCmd(c *Client, account, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := requestCtx()
		defer cancel()
		if err := c.PermanentDelete(ctx, account, folder, uid); err != nil {
			return msgErr{err}
		}
		return msgDeleted{account: account, folder: folder, uid: uid}
	}
}

// fetchCombinedMessages fetches all listed folders in parallel and delivers
// a single msgCombinedMessages when every fetch has settled. Errors on
// individual folders are silently skipped so one slow/broken folder
// doesn't block the combined view.
func fetchCombinedMessages(c *Client, account string, folders []string) tea.Cmd {
	if len(folders) == 0 {
		return func() tea.Msg { return msgCombinedMessages{} }
	}
	return func() tea.Msg {
		ctx, cancel := requestCtx()
		defer cancel()
		var (
			mu  sync.Mutex
			all []types.Message
			wg  sync.WaitGroup
		)
		for _, f := range folders {
			wg.Add(1)
			go func(folder string) {
				defer wg.Done()
				r, err := c.Messages(ctx, account, folder)
				if err != nil {
					return
				}
				mu.Lock()
				all = append(all, r.Messages...)
				mu.Unlock()
			}(f)
		}
		wg.Wait()
		return msgCombinedMessages{messages: all}
	}
}

// saveFolderStateCmd persists one folder's display state. Errors surface via
// the normal msgErr path so a disk failure shows in the error banner.
func saveFolderStateCmd(folder string, state FolderState) tea.Cmd {
	return func() tea.Msg {
		if err := saveFolderState(folder, state); err != nil {
			return msgErr{err}
		}
		return nil
	}
}
