package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wkirschbaum/whkmail/internal/types"
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

// folderManagerModal is the full-screen folder manager overlay. It shows all
// folders grouped by state — combined and normal at the top, hidden at the
// bottom — and lets the user cycle states with Space.
type folderManagerModal struct {
	cursor int
}

func (folderManagerModal) overlay() bool { return true }

func (p folderManagerModal) handleKey(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	sorted := sortedFolders(m.folders, m.folderStates)
	switch msg.String() {
	case "j", "down", "ctrl+n":
		p.cursor = clamp(p.cursor+1, len(sorted)-1)
		m.modal = p
	case "k", "up", "ctrl+p":
		p.cursor = clamp(p.cursor-1, len(sorted)-1)
		m.modal = p
	case " ":
		if p.cursor < len(sorted) {
			folder := sorted[p.cursor].Name
			next := cycleState(folderStateFor(folder, m.folderStates))
			m.folderStates[folder] = next
			// Keep cursor tracking the same folder in the re-sorted list.
			newSorted := sortedFolders(m.folders, m.folderStates)
			for i, f := range newSorted {
				if f.Name == folder {
					p.cursor = i
					break
				}
			}
			m.modal = p
			return m, saveFolderStateCmd(folder, next)
		}
	case "esc", "backspace":
		m.modal = nil
		// Clamp the folder-list cursor in case hidden folders were added.
		if m.view == viewFolders {
			m.cursor = clamp(m.cursor, len(m.visibleFolders())-1)
		}
	}
	return m, nil
}

func (p folderManagerModal) render(m Model) string {
	sorted := sortedFolders(m.folders, m.folderStates)

	var b strings.Builder
	b.WriteString(styleHeader.Render("Folder Manager") + "\n\n")

	hiddenHeaderShown := false
	for i, f := range sorted {
		state := folderStateFor(f.Name, m.folderStates)

		if !hiddenHeaderShown && state == FolderStateHidden {
			b.WriteString("\n" + styleDim.Render("── hidden ──") + "\n")
			hiddenHeaderShown = true
		}

		var stateTag string
		switch state {
		case FolderStateCombined:
			stateTag = "◉ combined"
		case FolderStateNormal:
			stateTag = "○ normal  "
		case FolderStateHidden:
			stateTag = ""
		}

		name := truncate(f.Name, 30)
		var line string
		if state != FolderStateHidden && f.Unread > 0 {
			line = fmt.Sprintf("%-30s  %s  %d", name, stateTag, f.Unread)
		} else {
			line = fmt.Sprintf("%-30s  %s", name, stateTag)
		}

		switch {
		case i == p.cursor:
			b.WriteString(styleSelected.Render("> " + line) + "\n")
		case state == FolderStateHidden:
			b.WriteString(styleDim.Render("  " + line) + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}

	b.WriteString("\n" + styleDim.Render("space: combined → normal → hidden   j/k: move   esc: close"))
	return b.String()
}

// sortedFolders returns folders in display order for the folder manager:
// combined first, then normal, then hidden — preserving server order within
// each group.
func sortedFolders(folders []types.Folder, states map[string]FolderState) []types.Folder {
	var combined, normal, hidden []types.Folder
	for _, f := range folders {
		switch folderStateFor(f.Name, states) {
		case FolderStateCombined:
			combined = append(combined, f)
		case FolderStateNormal:
			normal = append(normal, f)
		default:
			hidden = append(hidden, f)
		}
	}
	out := make([]types.Folder, 0, len(folders))
	out = append(out, combined...)
	out = append(out, normal...)
	out = append(out, hidden...)
	return out
}
