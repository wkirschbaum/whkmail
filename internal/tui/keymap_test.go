package tui

import "testing"

func TestInputStyle_Normalize(t *testing.T) {
	cases := map[InputStyle]InputStyle{
		"":        StyleVim, // empty → default
		"vim":     StyleVim,
		"emacs":   StyleEmacs,
		"unknown": StyleVim, // unrecognised → default
		"VIM":     StyleVim, // case-sensitive: anything not "emacs" → vim
	}
	for in, want := range cases {
		if got := in.Normalize(); got != want {
			t.Errorf("InputStyle(%q).Normalize() = %q, want %q", in, got, want)
		}
	}
}

func TestInputStyle_Key_Vim(t *testing.T) {
	cases := map[Action]string{
		ActMove:        "j/k",
		ActMarkRead:    "s",
		ActMarkUnread:  "N",
		ActScrollBody:  "j/k",
		ActJumpMessage: "n/p",
		ActConfig:      ",",
		ActOpen:        "enter",
		ActTrash:       "d",
		ActRefresh:     "r",
		ActBack:        "esc",
		ActQuit:        "C-d",
	}
	for action, want := range cases {
		if got := StyleVim.Key(action); got != want {
			t.Errorf("StyleVim.Key(%v) = %q, want %q", action, got, want)
		}
	}
}

func TestInputStyle_Key_Emacs(t *testing.T) {
	// Distinct emacs bindings.
	distinct := map[Action]string{
		ActMove:       "↓/↑",
		ActMarkRead:   "!",
		ActScrollBody: "↓/↑",
		ActHalfPage:   "PgDn/PgUp",
	}
	for action, want := range distinct {
		if got := StyleEmacs.Key(action); got != want {
			t.Errorf("StyleEmacs.Key(%v) = %q, want %q", action, got, want)
		}
	}

	// Actions without a distinct emacs binding must fall back to the vim
	// display. ActMarkUnread used to be `?` but `?` is now reserved for
	// the help overlay, so emacs users see the shared `N` binding.
	shared := []Action{
		ActOpen, ActTrash, ActRefresh, ActBack, ActQuit, ActConfig,
		ActJumpMessage, ActMarkUnread, ActHelp,
	}
	for _, action := range shared {
		if got, want := StyleEmacs.Key(action), StyleVim.Key(action); got != want {
			t.Errorf("StyleEmacs.Key(%v) = %q, want fallback %q", action, got, want)
		}
	}
}

func TestStyleIndex(t *testing.T) {
	cases := map[InputStyle]int{
		StyleVim:   0,
		StyleEmacs: 1,
		"":         0, // unknown falls back to the first entry
		"garbage":  0,
	}
	for in, want := range cases {
		if got := styleIndex(in); got != want {
			t.Errorf("styleIndex(%q) = %d, want %d", in, got, want)
		}
	}
}
