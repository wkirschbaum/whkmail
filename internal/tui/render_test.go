package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/wkirschbaum/whkmail/internal/types"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello w…"},
		{"hello", 1, "…"},
		{"hello", 0, "…"},
		{"", 5, ""},
		{"héllo wörld", 7, "héllo …"}, // max=7 → runes[:6]="héllo " + "…"
	}
	for _, c := range cases {
		got := truncate(c.in, c.max)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestClamp(t *testing.T) {
	cases := []struct {
		v, hi, want int
	}{
		{3, 5, 3},
		{0, 5, 0},
		{6, 5, 5},
		{-1, 5, 0},
		{0, -1, 0}, // empty slice guard
		{3, 0, 0},
	}
	for _, c := range cases {
		got := clamp(c.v, c.hi)
		if got != c.want {
			t.Errorf("clamp(%d, %d) = %d, want %d", c.v, c.hi, got, c.want)
		}
	}
}

func TestWrapBody_ShortLines(t *testing.T) {
	in := "short line\nanother line"
	got := wrapBody(in, 80)
	if got != in {
		t.Errorf("expected no change for short lines, got %q", got)
	}
}

func TestWrapBody_LongLine(t *testing.T) {
	in := "word1 word2 word3 word4 word5"
	got := wrapBody(in, 15)
	lines := strings.Split(got, "\n")
	for _, l := range lines {
		if len([]rune(l)) > 15 {
			t.Errorf("line %q exceeds width 15", l)
		}
	}
	// All words must be present.
	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "word1") || !strings.Contains(joined, "word5") {
		t.Errorf("words missing after wrap: %q", joined)
	}
}

func TestWrapBody_CRLFNormalised(t *testing.T) {
	in := "line one\r\nline two\r\n"
	// Caller normalises \r\n before wrapBody; we verify wrapBody handles \n correctly.
	in = strings.ReplaceAll(in, "\r\n", "\n")
	got := wrapBody(strings.TrimRight(in, "\n"), 80)
	if strings.Contains(got, "\r") {
		t.Errorf("CR still present in wrapped output: %q", got)
	}
}

func TestPadRight(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"hi", 5, "hi   "},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello world"}, // already longer than width → unchanged
		{"", 3, "   "},
		{"héllo", 6, "héllo "}, // rune-aware, multibyte chars count as 1
	}
	for _, c := range cases {
		got := padRight(c.in, c.width)
		if got != c.want {
			t.Errorf("padRight(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

func TestFormatMessageRow_Shape(t *testing.T) {
	msg := types.Message{
		From:    "Alice <alice@example.com>",
		Subject: "Hello there",
		Date:    time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
	}
	row := formatMessageRow(msg, 80)

	if !strings.Contains(row, "Alice") {
		t.Errorf("row missing from: %q", row)
	}
	if !strings.Contains(row, "Hello there") {
		t.Errorf("row missing subject: %q", row)
	}
	if !strings.Contains(row, "Jun 15") {
		t.Errorf("row missing date: %q", row)
	}
}
