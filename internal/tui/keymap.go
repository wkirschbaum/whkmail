package tui

// InputStyle is the user-selectable keymap profile. The handler accepts
// bindings from both styles unconditionally so muscle memory never fails;
// this type only controls which set is displayed in the help footer. A
// future per-action rebinding feature plugs in here — nothing in the
// renderer hardcodes a key, every display string goes through Key.
type InputStyle string

const (
	StyleVim   InputStyle = "vim"
	StyleEmacs InputStyle = "emacs"
)

// Action enumerates the logical operations exposed to the user. Every
// entry in the help footer is keyed by Action, never by raw strings, so
// switching style (or later remapping one action) is a single-point
// change.
type Action int

const (
	ActMove Action = iota
	ActOpen
	ActMarkRead
	ActMarkUnread
	ActTrash
	ActRefresh
	ActBack
	ActQuit
	ActScrollBody
	ActJumpMessage
	ActConfig
	ActTopBottom
	ActHalfPage
	ActHelp
)

// Key returns the display string for an action under this style. Emacs
// falls back to the vim binding for actions where there is no distinct
// emacs convention (enter, esc, q, d, r, etc.) — most keys are shared.
func (s InputStyle) Key(a Action) string {
	if s == StyleEmacs {
		if k, ok := emacsKeys[a]; ok {
			return k
		}
	}
	return vimKeys[a]
}

// Normalize returns a valid InputStyle, defaulting to vim when the value
// is empty or unrecognised. Callers use it to coerce user input or
// config-file values without special-casing each site.
func (s InputStyle) Normalize() InputStyle {
	if s == StyleEmacs {
		return StyleEmacs
	}
	return StyleVim
}

var vimKeys = map[Action]string{
	ActMove:        "j/k",
	ActOpen:        "enter",
	ActMarkRead:    "s",
	ActMarkUnread:  "N",
	ActTrash:       "d",
	ActRefresh:     "r",
	ActBack:        "esc",
	ActQuit:        "C-d",
	ActScrollBody:  "j/k",
	ActJumpMessage: "n/p",
	ActConfig:      ",",
	ActTopBottom:   "g/G",
	ActHalfPage:    "PgDn/PgUp",
	ActHelp:        "?",
}

// emacsKeys overrides vim bindings for actions that have a distinct
// emacs-world convention. ActMarkUnread was once `?` but `?` is now the
// shared help key; emacs users fall back to the `N` binding (also
// accepted by the handler).
var emacsKeys = map[Action]string{
	ActMove:       "↓/↑",
	ActMarkRead:   "!",
	ActScrollBody: "↓/↑",
	ActHalfPage:   "PgDn/PgUp",
}
