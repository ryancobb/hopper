package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"hopper/internal/source"
	"hopper/internal/status"
)

func TestSessionLayout(t *testing.T) {
	nameW, nameStart, showWord := sessionLayout(80)
	if nameW != 58 || nameStart != 16 || !showWord {
		t.Errorf("sessionLayout(80) = %d,%d,%v want 58,16,true", nameW, nameStart, showWord)
	}
	// 32-22 = 10 < minNameW, so the status word drops out.
	nameW, nameStart, showWord = sessionLayout(32)
	if nameW != 19 || nameStart != 7 || showWord {
		t.Errorf("sessionLayout(32) = %d,%d,%v want 19,7,false", nameW, nameStart, showWord)
	}
}

func TestSessionRowGeometry(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowSession && rr.Item.Session.Kind == status.Working {
			r = rr
		}
	}
	line := m.renderSessionRow(r, false, 60)
	if got := utf8.RuneCountInString(line); got != 60 {
		t.Fatalf("row width = %d, want 60: %q", got, line)
	}
	runes := []rune(line)
	if string(runes[4]) != "●" {
		t.Errorf("glyph at col 4 = %q: %q", string(runes[4]), line)
	}
	if got := string(runes[6:13]); got != "working" {
		t.Errorf("status word = %q: %q", got, line)
	}
	if got := string(runes[16:21]); got != "first" {
		t.Errorf("name at col 16 = %q: %q", got, line)
	}
	if !strings.HasSuffix(line, "0m") {
		t.Errorf("age not right-aligned at edge: %q", line)
	}
}

func TestSessionRowNarrowDropsStatusWord(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowSession && rr.Item.Session.Kind == status.Working {
			r = rr
		}
	}
	line := m.renderSessionRow(r, false, 32)
	if strings.Contains(line, "working") {
		t.Errorf("status word should be dropped at width 32: %q", line)
	}
	if got := utf8.RuneCountInString(line); got != 32 {
		t.Errorf("row width = %d, want 32: %q", got, line)
	}
	runes := []rune(line)
	if string(runes[4]) != "●" {
		t.Errorf("glyph at col 4 = %q: %q", string(runes[4]), line)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hell…"},
		{"hi", 1, "…"},
		{"hi", 0, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q,%d)=%q want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestWorstKindCount(t *testing.T) {
	g := Group{Kind: status.Working, Items: []Item{
		{Session: source.Session{Kind: status.Working}},
		{Session: source.Session{Kind: status.Idle}},
		{Session: source.Session{Kind: status.Working}},
	}}
	if got := worstKindCount(g); got != 2 {
		t.Fatalf("worstKindCount=%d want 2", got)
	}
}

func TestSelectedRowBar(t *testing.T) {
	// Force a color profile so styles emit ANSI; restore afterward.
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := applyLoad(twoSessionModel())
	var sessionRow Row
	for _, r := range m.rows {
		if r.Kind == RowSession {
			sessionRow = r
			break
		}
	}

	sel := m.renderSessionRow(sessionRow, true, 60)
	if !strings.Contains(sel, "▌") {
		t.Errorf("selected row missing ▌ bar: %q", sel)
	}
	if !strings.ContainsRune(sel, '\x1b') {
		t.Errorf("selected row should keep ANSI styling: %q", sel)
	}
	unsel := m.renderSessionRow(sessionRow, false, 60)
	if strings.Contains(unsel, "▌") {
		t.Errorf("unselected row has ▌ bar: %q", unsel)
	}
}
