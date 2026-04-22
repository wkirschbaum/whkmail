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
