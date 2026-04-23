package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// modal is a uniform shape for every popup the TUI can raise. When
// Model.modal is non-nil the main key handler and renderer route through
// the modal instead of their usual logic. Adding a new popup means
// implementing this interface — no touching keys.go / render.go / model
// fields.
type modal interface {
	// handleKey processes a single keystroke for this modal. The returned
	// Model must have its modal field updated: set to nil to close, set to
	// a new modal to transition, or kept (often with an updated copy of
	// the receiver) to stay open with mutated state.
	handleKey(m Model, msg tea.KeyMsg) (Model, tea.Cmd)

	// render produces the modal's display string. When overlay() is true
	// the result replaces the entire view; when false it replaces only
	// the bottom status bar so the underlying view stays visible.
	render(m Model) string

	// overlay distinguishes full-screen popups (help, style picker) from
	// inline status-bar modals (destructive-action confirm). Inline
	// modals don't hide context; overlays do.
	overlay() bool
}

// confirmModal is the bottom-bar y/N prompt used before destructive
// actions. The onConfirm closure runs when the user types `y`; it
// receives the current Model so the action can produce optimistic
// updates and a matching daemon command in one step.
type confirmModal struct {
	prompt    string
	onConfirm func(Model) (Model, tea.Cmd)
}

func (c confirmModal) handleKey(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.modal = nil
		if c.onConfirm != nil {
			return c.onConfirm(m)
		}
		return m, nil
	case "n", "N", "esc", "ctrl+c":
		m.modal = nil
	}
	return m, nil
}

func (c confirmModal) render(m Model) string {
	width := m.width
	if width < 1 {
		width = 40
	}
	prompt := " " + c.prompt + " "
	return styleFlash.Render(padRight(truncate(prompt, width), width))
}

func (confirmModal) overlay() bool { return false }

// helpModal is a read-only overlay showing every keybinding. Any key
// closes it — there's nothing to interact with, so no state is needed.
type helpModal struct{}

func (helpModal) handleKey(m Model, _ tea.KeyMsg) (Model, tea.Cmd) {
	m.modal = nil
	return m, nil
}

func (helpModal) render(m Model) string { return renderHelpBody(m) }
func (helpModal) overlay() bool         { return true }

// stylePickerModal is the input-style chooser. The cursor lives on the
// modal value itself so Model stays free of picker bookkeeping; updates
// store a fresh copy of the modal back into Model.modal.
type stylePickerModal struct {
	cursor int
}

// newStylePickerModal opens the picker with the cursor on the currently
// active style so the user sees what's already selected.
func newStylePickerModal(current InputStyle) stylePickerModal {
	return stylePickerModal{cursor: styleIndex(current)}
}

// openStylePicker is the Model-facing entry point that raises the style
// picker. Kept as a method so callers don't have to know how the modal
// is constructed.
func (m Model) openStylePicker() Model {
	m.modal = newStylePickerModal(m.style)
	return m
}

func (p stylePickerModal) handleKey(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down", "ctrl+n":
		p.cursor = clamp(p.cursor+1, len(configStyles)-1)
		m.modal = p
	case "k", "up", "ctrl+p":
		p.cursor = clamp(p.cursor-1, len(configStyles)-1)
		m.modal = p
	case "enter":
		chosen := configStyles[p.cursor].Normalize()
		m.modal = nil
		if chosen == m.style {
			return m, nil
		}
		m.style = chosen
		return m, saveStyleCmd(chosen)
	case "esc", "ctrl+c", ",":
		m.modal = nil
	}
	return m, nil
}

func (p stylePickerModal) render(_ Model) string { return renderStylePickerBody(p.cursor) }
func (stylePickerModal) overlay() bool           { return true }

// configStyles is the ordered list of pickable input styles. Declared
// here (rather than in keys.go) so it lives next to the modal that
// consumes it.
var configStyles = []InputStyle{StyleVim, StyleEmacs}

// styleIndex returns the index of style in configStyles, or 0 for unknown
// values so opening the picker never lands on an out-of-range cursor.
func styleIndex(style InputStyle) int {
	for i, st := range configStyles {
		if st == style {
			return i
		}
	}
	return 0
}
