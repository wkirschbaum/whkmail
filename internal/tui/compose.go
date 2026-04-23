package tui

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wkirschbaum/whkmail/internal/compose"
	"github.com/wkirschbaum/whkmail/internal/smtp"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// draftSaveDebounce is the quiet period after the most recent keystroke
// before the current compose state is flushed to disk. Mirrors the 300ms
// flash debounce in shape but uses 10s — drafts are long-term state and
// saving more often would be wasteful I/O.
const draftSaveDebounce = 10 * time.Second

// composeState holds the in-progress reply draft. The textarea owns the
// body (including cursor / wrapping / undo); the headers are pinned and
// shown above it so the user can see who the reply goes to while they
// type. Header editing is out-of-scope for the first cut — recipients
// are fully derived from the original message.
type composeState struct {
	draft        smtp.Message   // headers are frozen at open time; body is synced on send / save
	body         textarea.Model // live editor for the body only
	sourceFolder string         // folder the parent message lives in; used for post-send resync

	// sending flips true the moment Ctrl+S is pressed and stays set until
	// the server acks or errors. Keys are swallowed while sending so the
	// user can't double-submit.
	sending bool

	// draft-save state. key identifies the draft file on disk; gen is
	// bumped on every keystroke so stale save ticks can detect they've
	// been superseded by later input and no-op.
	draftKey string
	draftGen int
}

// newComposeState opens a reply draft from the given original message.
// Reply-all is the default mode; Shift+R on the caller side switches to
// sender-only. If a previous draft exists on disk for this thread its
// body is restored; otherwise the textarea is pre-seeded with the quoted
// body so the user starts above the attribution.
func newComposeState(orig types.Message, self string, allRecipients bool, sourceFolder string) composeState {
	mode := compose.ReplyAll
	if !allRecipients {
		mode = compose.ReplySender
	}
	draft := compose.Build(mode, orig, self)
	key := draftKey(orig)

	// If a saved draft exists, prefer its body — the user was in the
	// middle of something. Everything else stays as freshly computed
	// (recipients / subject could have shifted since last save).
	if saved, err := loadDraft(self, key); err == nil && saved != nil && saved.Body != "" {
		draft.Body = saved.Body
	} else if err != nil {
		slog.Warn("load draft", "err", err)
	}

	ta := textarea.New()
	ta.Placeholder = "Write your reply…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetValue(draft.Body)
	// Land the cursor at the very top — the attribution + quoted body
	// sit below it, so the user types above the quote.
	ta.CursorStart()
	ta.Focus()

	return composeState{
		draft:        draft,
		body:         ta,
		sourceFolder: sourceFolder,
		draftKey:     key,
	}
}

// handleKey routes input to the textarea, intercepting the app-level
// bindings (send, cancel) before passing the rest through. Returns the
// updated Model + any command — Ctrl+S produces a sendCmd, Esc clears
// the compose.
func (m Model) handleComposeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.compose.sending {
		// Lock input until the send resolves — prevents double-submit.
		return m, nil
	}
	switch msg.String() {
	case "ctrl+s", "alt+enter":
		// Alt+Enter is the fallback for terminals that eat Ctrl+S as
		// XOFF (flow control). Both bindings do the same thing.
		m.compose.draft.Body = m.compose.body.Value()
		if err := compose.Validate(m.compose.draft); err != nil {
			m.err = err
			return m, nil
		}
		m.compose.sending = true
		return m, sendCmd(m.client, m.account, m.compose.draft, m.compose.sourceFolder, m.compose.draftKey)
	case "esc":
		m.compose = nil
		return m, nil
	}
	// Pass everything else through to the textarea and arm a draft-save
	// tick so the current body hits disk after draftSaveDebounce of
	// quiet. Every keystroke bumps the generation counter so stale
	// ticks mid-burst are discarded.
	var cmd tea.Cmd
	m.compose.body, cmd = m.compose.body.Update(msg)
	m.compose.draftGen++
	gen := m.compose.draftGen
	save := tea.Tick(draftSaveDebounce, func(time.Time) tea.Msg {
		return msgDraftSave{gen: gen}
	})
	if cmd == nil {
		return m, save
	}
	return m, tea.Batch(cmd, save)
}

// msgDraftSave fires when the draft-save debounce closes. gen is checked
// against the live composeState.draftGen so an earlier tick that's been
// superseded by later typing is discarded.
type msgDraftSave struct{ gen int }

// resize tells the textarea how many rows / cols it has. Called from
// Update on every WindowSizeMsg and whenever compose opens so the
// textarea doesn't default to an unusable size on first render.
func (c *composeState) resize(width, height int) {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	c.body.SetWidth(width)
	c.body.SetHeight(height)
}

// renderCompose draws the compose pane below the open message. Headers
// are a dim read-only block; the textarea's own View supplies the
// editable body with cursor.
func (m Model) renderCompose() string {
	if m.compose == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(strings.Repeat("─", composeDividerWidth(m.width))) + "\n")
	b.WriteString(styleHeader.Render("Reply"))
	if m.compose.sending {
		b.WriteString(styleMuted.Render("  (sending…)"))
	}
	b.WriteString("\n")
	if to := strings.Join(m.compose.draft.To, ", "); to != "" {
		b.WriteString(styleDim.Render("To:   "+to) + "\n")
	}
	if cc := strings.Join(m.compose.draft.Cc, ", "); cc != "" {
		b.WriteString(styleDim.Render("Cc:   "+cc) + "\n")
	}
	b.WriteString(styleDim.Render("Subj: "+m.compose.draft.Subject) + "\n")
	b.WriteString(styleDim.Render(strings.Repeat("─", composeDividerWidth(m.width))) + "\n")
	b.WriteString(m.compose.body.View())
	b.WriteString("\n" + styleDim.Render("C-s / Alt-Enter send  ·  esc cancel"))
	return b.String()
}

// composeDividerWidth picks a reasonable width for the pane separators.
// Never zero — the renderer falls back to 40 before we even get a
// WindowSizeMsg so help screens render.
func composeDividerWidth(w int) int {
	if w < 20 {
		return 40
	}
	return w
}

// msgSent is dispatched when the daemon acks a send. Clears the compose
// pane and deletes the on-disk draft for this thread — it's been sent,
// no reason to keep a copy.
type msgSent struct {
	account  string
	draftKey string
}

// sendCmd POSTs the draft to the daemon's send endpoint. Errors flow
// through the standard msgErr channel; success dispatches msgSent so
// Update can clean up the local draft file.
func sendCmd(c *Client, account string, draft smtp.Message, sourceFolder, key string) tea.Cmd {
	req := types.SendRequest{
		To:           draft.To,
		Cc:           draft.Cc,
		Subject:      draft.Subject,
		Body:         draft.Body,
		InReplyTo:    draft.InReplyTo,
		References:   draft.References,
		SourceFolder: sourceFolder,
	}
	return func() tea.Msg {
		ctx, cancel := contextWithSendTimeout()
		defer cancel()
		if err := c.Send(ctx, account, req); err != nil {
			return msgErr{fmt.Errorf("send: %w", err)}
		}
		return msgSent{account: account, draftKey: key}
	}
}
