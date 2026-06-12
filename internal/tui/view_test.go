package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"hopper/internal/repo"
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

func TestRepoRowBreakdownRightAligned(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowRepo {
			r = rr
		}
	}
	line := m.renderRepoRow(r, false, 60)
	if got := lipgloss.Width(line); got != 60 {
		t.Errorf("repo row width = %d, want 60: %q", got, line)
	}
	if !strings.HasSuffix(line, "● 1 working · ○ 1 recent") {
		t.Errorf("breakdown not right-aligned, worst-first: %q", line)
	}
	if !strings.Contains(line, "▾ aaa") {
		t.Errorf("missing caret+name: %q", line)
	}
	if strings.Contains(line, "main") {
		t.Errorf("branch should be removed: %q", line)
	}
}

func TestBreakdownCounts(t *testing.T) {
	now := time.Now()
	g := Group{Items: []Item{
		{Session: source.Session{Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)}},
		{Session: source.Session{Kind: status.Idle, UpdatedAt: now.Add(-2 * time.Minute)}},
		{Session: source.Session{Kind: status.Blocked, UpdatedAt: now}},
		{Session: source.Session{Kind: status.Working, UpdatedAt: now}},
		{Session: source.Session{Kind: status.Working, UpdatedAt: now}},
	}}
	got := breakdownCounts(g)
	want := []classCount{{classBlocked, 1}, {classWorking, 2}, {classRecentIdle, 1}, {classIdle, 1}}
	if len(got) != len(want) {
		t.Fatalf("breakdownCounts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("breakdownCounts = %v, want %v", got, want)
		}
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

	var repoRow Row
	for _, r := range m.rows {
		if r.Kind == RowRepo {
			repoRow = r
			break
		}
	}
	selRepo := m.renderRepoRow(repoRow, true, 60)
	if !strings.Contains(selRepo, "▌") {
		t.Errorf("selected repo row missing ▌ bar: %q", selRepo)
	}
	if got := lipgloss.Width(selRepo); got != 60 {
		t.Errorf("selected repo row width = %d, want 60: %q", got, selRepo)
	}
}

func TestRecentIdleRowStyling(t *testing.T) {
	// Force a color profile so styles emit ANSI; restore afterward.
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "fresh", Kind: status.Idle, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "s2", PID: 2, CWD: "/a", Name: "stale", Kind: status.Idle, UpdatedAt: now.Add(-18 * time.Minute)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var fresh, stale Row
	for _, r := range m.rows {
		if r.Kind != RowSession {
			continue
		}
		if r.Item.Session.Name == "fresh" {
			fresh = r
		} else {
			stale = r
		}
	}

	freshLine := m.renderSessionRow(fresh, false, 60)
	if !strings.Contains(freshLine, classRecentIdle.style().Render("○")) {
		t.Errorf("fresh idle row not in recent style: %q", freshLine)
	}
	staleLine := m.renderSessionRow(stale, false, 60)
	if !strings.Contains(staleLine, classIdle.style().Render("○")) {
		t.Errorf("stale idle row not in idle style: %q", staleLine)
	}
	if strings.Contains(staleLine, classRecentIdle.style().Render("○")) {
		t.Errorf("stale idle row styled as recent: %q", staleLine)
	}
	// The status word still reads "idle" on both rows; only color differs.
	if !strings.Contains(freshLine, "idle") || !strings.Contains(staleLine, "idle") {
		t.Errorf("status word should stay \"idle\":\n%q\n%q", freshLine, staleLine)
	}
}

func TestBlockedReasonContinuationLine(t *testing.T) {
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked, WaitingFor: "permission: rm", UpdatedAt: now},
		{ID: "s2", PID: 2, CWD: "/a", Name: "fine", Kind: status.Working, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))
	m.width = 60

	out := m.View()
	if !strings.Contains(out, "↳ permission: rm") {
		t.Errorf("missing reason continuation line:\n%s", out)
	}
	if got := strings.Count(out, "↳"); got != 1 {
		t.Errorf("continuation lines = %d, want 1 (only the blocked session):\n%s", got, out)
	}
}
