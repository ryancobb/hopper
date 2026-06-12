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

// statusColumn returns the display column of the status glyph in a rendered row.
// Every rune before the glyph is width-1 (spaces, ASCII text, the caret), so the
// rune count equals the column. The line must be plain (no ANSI).
func statusColumn(line, glyph string) int {
	i := strings.Index(line, glyph)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(line[:i])
}

func TestStatusColumnsAlign(t *testing.T) {
	m := applyLoad(twoSessionModel()) // repo "aaa" aggregates to working (●)
	var repoLine, sessLine string
	for _, r := range m.rows {
		switch {
		case r.Kind == RowRepo:
			repoLine = m.renderRepoRow(r, false)
		case r.Kind == RowSession && r.Item.Session.Kind == status.Working:
			sessLine = m.renderSessionRow(r, false)
		}
	}
	repoCol := statusColumn(repoLine, "●")
	sessCol := statusColumn(sessLine, "●")
	if repoCol < 0 || sessCol < 0 {
		t.Fatalf("status glyph missing:\nrepo=%q\nsess=%q", repoLine, sessLine)
	}
	if repoCol != sessCol {
		t.Errorf("status columns misaligned: repo=%d sess=%d\nrepo=%q\nsess=%q",
			repoCol, sessCol, repoLine, sessLine)
	}
	if repoCol != statusCol {
		t.Errorf("status column = %d, want statusCol=%d", repoCol, statusCol)
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

func TestSelectedRowContentIsPlain(t *testing.T) {
	// Force a color profile so styled (unselected) rows actually emit ANSI; restore
	// it afterward so other tests are unaffected.
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := applyLoad(twoSessionModel())
	var sessionRow, repoRow Row
	for _, r := range m.rows {
		switch r.Kind {
		case RowSession:
			sessionRow = r
		case RowRepo:
			repoRow = r
		}
	}

	// A selected row's content must be ANSI-free so the full-width background
	// highlight is continuous (inner color resets would break it mid-line).
	if got := m.renderSessionRow(sessionRow, true); strings.ContainsRune(got, '\x1b') {
		t.Errorf("selected session row should be plain, got ANSI: %q", got)
	}
	if got := m.renderRepoRow(repoRow, true); strings.ContainsRune(got, '\x1b') {
		t.Errorf("selected repo row should be plain, got ANSI: %q", got)
	}
	// Unselected rows should still be styled under a color profile.
	if got := m.renderSessionRow(sessionRow, false); !strings.ContainsRune(got, '\x1b') {
		t.Errorf("unselected session row should be styled, got plain: %q", got)
	}
}
