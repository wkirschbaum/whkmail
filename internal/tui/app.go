package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/types"
)

type view int

const (
	viewFolders view = iota
	viewMessages
	viewMessage
)

func NewModel(c *Client) Model {
	return Model{client: c}
}

type Model struct {
	client   *Client
	view     view
	folders  []types.Folder
	messages []types.Message
	message  *types.Message
	cursor   int
	folder   string
	err      error
	width    int
	height   int
}

type msgStatus types.StatusResponse
type msgMessages types.MessagesResponse
type msgMessage types.MessageResponse
type msgErr struct{ err error }

func (m Model) Init() tea.Cmd {
	return fetchStatus(m.client)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case msgStatus:
		m.folders = msg.Folders
		m.cursor = clamp(m.cursor, len(m.folders)-1)

	case msgMessages:
		m.messages = msg.Messages
		m.cursor = 0

	case msgMessage:
		m.message = &msg.Message

	case msgErr:
		m.err = msg.err

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		switch m.view {
		case viewFolders:
			m.cursor = clamp(m.cursor+1, len(m.folders)-1)
		case viewMessages:
			m.cursor = clamp(m.cursor+1, len(m.messages)-1)
		case viewMessage:
		}

	case "k", "up":
		switch m.view {
		case viewFolders:
			m.cursor = clamp(m.cursor-1, len(m.folders)-1)
		case viewMessages:
			m.cursor = clamp(m.cursor-1, len(m.messages)-1)
		case viewMessage:
		}

	case "enter":
		switch m.view {
		case viewFolders:
			if len(m.folders) == 0 {
				break
			}
			m.folder = m.folders[m.cursor].Name
			m.view = viewMessages
			return m, fetchMessages(m.client, m.folder)
		case viewMessages:
			if len(m.messages) == 0 {
				break
			}
			selected := m.messages[m.cursor]
			m.view = viewMessage
			return m, fetchMessage(m.client, m.folder, selected.UID)
		case viewMessage:
		}

	case "esc", "backspace":
		switch m.view {
		case viewFolders:
		case viewMessages:
			m.view = viewFolders
			m.cursor = 0
		case viewMessage:
			m.view = viewMessages
			m.message = nil
		}
	}
	return m, nil
}

func fetchStatus(c *Client) tea.Cmd {
	return func() tea.Msg {
		s, err := c.Status(context.Background())
		if err != nil {
			return msgErr{err}
		}
		return msgStatus(*s)
	}
}

func fetchMessages(c *Client, folder string) tea.Cmd {
	return func() tea.Msg {
		r, err := c.Messages(context.Background(), folder)
		if err != nil {
			return msgErr{err}
		}
		return msgMessages(*r)
	}
}

func fetchMessage(c *Client, folder string, uid uint32) tea.Cmd {
	return func() tea.Msg {
		r, err := c.Message(context.Background(), folder, uid)
		if err != nil {
			return msgErr{err}
		}
		return msgMessage(*r)
	}
}

// clamp returns v clamped to [0, hi]. Returns 0 when hi < 0 (empty slice).
func clamp(v, hi int) int {
	if hi < 0 {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}
