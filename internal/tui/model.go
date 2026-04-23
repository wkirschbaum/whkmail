package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/types"
)


// view enumerates the screens the TUI can show. The ordering is significant
// only insofar as the zero value (viewFolders) is the landing screen for
// single-account installs.
type view int

const (
	viewFolders view = iota
	viewAccounts
	viewMessages
	viewMessage
)

// NewModel returns a fresh Model. markReadDelay controls how long a message
// must stay open in the detail view before the daemon is asked to flag it
// as seen; pass 0 to use types.DefaultMarkReadDelay.
func NewModel(c *Client, markReadDelay time.Duration) Model {
	if markReadDelay <= 0 {
		markReadDelay = types.DefaultMarkReadDelay
	}
	eventCh := make(chan events.Event, 16)
	go func() {
		for {
			if err := c.StreamEvents(context.Background(), eventCh); err != nil {
				time.Sleep(5 * time.Second)
			}
		}
	}()
	return Model{
		client:        c,
		eventCh:       eventCh,
		loading:       true,
		markReadDelay: markReadDelay,
		prefetched:    make(map[prefetchKey]bool),
		bodyErr:       make(map[prefetchKey]string),
	}
}

// Model is the bubbletea state for the TUI. It is deliberately a value type:
// tea.Program manages updates functionally and the reference-typed fields
// (slices, maps, channels) carry mutations across copies.
type Model struct {
	client   *Client
	eventCh  chan events.Event
	view     view
	accounts []types.AccountStatus
	account  string
	folders  []types.Folder
	messages  []types.Message
	msgDepths []int // parallel to messages: thread depth of each row (0 = root)
	message   *types.Message
	cursor    int
	msgTop    int // top of the visible window in the message list
	folder   string
	loading  bool
	err      error
	width    int
	height   int

	// Body scroll state for viewMessage.
	bodyTop int // index of the first visible body line

	// Mark-as-read timer state.
	markReadDelay time.Duration
	markReadGen   int // bumped every time a message is opened; stale ticks ignored

	// prefetched tracks UIDs we've already asked the daemon to warm so we
	// don't re-queue the same fetch on every cursor move or refresh.
	prefetched map[prefetchKey]bool

	// bodyErr holds the last failure reason per message. A non-empty entry
	// means the daemon's background fetch failed for that UID; the message
	// view renders the error instead of hanging on "Loading…".
	bodyErr map[prefetchKey]string

	// confirmPrompt, when non-empty, puts the TUI into a confirmation state:
	// the renderer shows the prompt instead of the usual help line, and
	// handleKey intercepts y/n to run (or discard) onConfirm.
	confirmPrompt string
	onConfirm     func(Model) (Model, tea.Cmd)
}

// prefetchKey identifies a message across the prefetch cache and body-error
// map. Account is part of the key so a multi-account session can't cross
// bodies between INBOX/foo and INBOX/bar.
type prefetchKey struct {
	account string
	folder  string
	uid     uint32
}

// Message types delivered to Update. Each corresponds to one tea.Cmd or one
// event source. Keeping them typed (rather than opaque interfaces) makes the
// Update switch exhaustive at a glance.
type (
	msgStatus   types.StatusResponse
	msgMessages types.MessagesResponse
	msgMessage  types.MessageResponse
	msgEvent    events.Event
	msgErr      struct{ err error }

	// msgPrefetched carries a prefetch result. Errors are swallowed so a
	// transient background fetch can't poison the foreground error banner.
	msgPrefetched struct {
		message *types.Message // nil when the underlying fetch failed
	}
	msgMarkedRead struct {
		account string
		folder  string
		uid     uint32
	}
	msgMarkedUnread struct {
		account string
		folder  string
		uid     uint32
	}
	msgTrashed struct {
		account string
		folder  string
		uid     uint32
	}
	msgDeleted struct {
		account string
		folder  string
		uid     uint32
	}
	tickMarkRead struct {
		gen     int
		account string
		folder  string
		uid     uint32
	}
)

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), waitEvent(m.eventCh))
}
