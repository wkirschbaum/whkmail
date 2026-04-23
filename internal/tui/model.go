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

// flashState is the debounced "you got mail" banner shown on the left of
// the status bar. pending accumulates events during the debounce window,
// gen invalidates stale ticks when a newer event re-arms the timer, and
// text is the committed line actually rendered.
type flashState struct {
	text    string
	pending []flashEntry
	gen     int
}

// syncState reflects the daemon's in-flight IMAP sync. active drives the
// spinner; folder/current/total render the "⟳ INBOX (3/12)" detail when
// KindSyncProgress is being published.
type syncState struct {
	active  bool
	folder  string
	current int
	total   int
}

// markReadTimer holds the auto-mark-as-seen debounce state. delay is how
// long a detail view must stay open before the daemon is asked to flag
// the message, and gen invalidates stale ticks when the user navigates
// away mid-wait.
type markReadTimer struct {
	delay time.Duration
	gen   int
}

// flashEntry is one new-message arrival buffered during the debounce
// window. Kept small — the status bar only renders a count and the most
// recent subject/from, so nothing else needs to be captured.
type flashEntry struct {
	subject string
	from    string
}

// flashDebounce is the quiet period after the most recent KindNewMessage
// event before the status bar commits. A burst of arrivals collapses
// into a single update so the bar never flickers.
const flashDebounce = 300 * time.Millisecond

// prefetchKey identifies a message across the prefetch cache and body-error
// map. Account is part of the key so a multi-account session can't cross
// bodies between INBOX/foo and INBOX/bar.
type prefetchKey struct {
	account string
	folder  string
	uid     uint32
}

// NewModel returns a fresh Model. markReadDelay controls how long a message
// must stay open in the detail view before the daemon is asked to flag it
// as seen; pass 0 to use types.DefaultMarkReadDelay. style selects the help
// footer flavour; empty/unrecognised values fall through to vim.
func NewModel(c *Client, markReadDelay time.Duration, style InputStyle) Model {
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
		client:     c,
		eventCh:    eventCh,
		loading:    true,
		style:      style.Normalize(),
		mark:       markReadTimer{delay: markReadDelay},
		prefetched: make(map[prefetchKey]bool),
		bodyErr:    make(map[prefetchKey]string),
	}
}

// Model is the bubbletea state for the TUI. It is a value type so
// tea.Program's functional Update contract is preserved; the reference-
// typed fields (maps, slices, channels) still carry mutations across
// copies. Related fields are grouped into small sub-structs so new
// features add one field to one group rather than sprawling.
type Model struct {
	// wires to the outside world
	client  *Client
	eventCh chan events.Event

	// what's on screen right now
	view    view
	width   int
	height  int
	loading bool
	err     error
	style   InputStyle
	modal   modal // nil when no popup is open

	// navigation selection
	accounts []types.AccountStatus
	account  string
	folders  []types.Folder
	folder   string

	// message-list viewport
	messages  []types.Message
	msgDepths []int // parallel to messages: thread depth of each row (0 = root)
	cursor    int
	msgTop    int

	// message-detail view
	message *types.Message
	bodyTop int

	// auto-mark-as-read timer
	mark markReadTimer

	// prefetch cache + body-load error memory
	prefetched map[prefetchKey]bool
	bodyErr    map[prefetchKey]string

	// debounced new-mail flash (status bar left)
	flash flashState

	// daemon sync status (status bar spinner)
	sync syncState
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
	// msgFlash fires when the debounce window closes. The gen field lets
	// Update discard stale ticks when a newer KindNewMessage has pushed
	// the window out.
	msgFlash struct{ gen int }
)

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), waitEvent(m.eventCh))
}
