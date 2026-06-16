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

func TestKillConfirmFooter(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.width, m.height = 50, 20
	m.cursor = 1 // s1, name "first"
	next, _ := m.Update(key("x"))
	m = next.(Model)
	out := m.View()
	// The confirm reads as a distinct banner (like passthrough): a KILL tag, the
	// session name, and what y / any other key do.
	if !strings.Contains(out, "KILL") {
		t.Fatalf("footer missing kill banner tag:\n%s", out)
	}
	if !strings.Contains(out, "first") {
		t.Fatalf("kill banner missing the session name:\n%s", out)
	}
	if !strings.Contains(out, "y") || !strings.Contains(out, "cancel") {
		t.Fatalf("kill banner missing the y/cancel hint:\n%s", out)
	}
	if strings.Contains(out, "r refresh") {
		t.Fatalf("normal footer should be hidden during the confirm:\n%s", out)
	}
}

func TestSessionLayout(t *testing.T) {
	// Icon-only with a 1-cell gutter: the name starts at column 4 (gutter 1 +
	// indent 1 + glyph 1 + gap 1) and flexes to fill the row minus the gap and
	// age columns.
	nameW, nameStart := sessionLayout(80)
	if nameW != 71 || nameStart != 4 {
		t.Errorf("sessionLayout(80) = %d,%d want 71,4", nameW, nameStart)
	}
	nameW, nameStart = sessionLayout(32)
	if nameW != 23 || nameStart != 4 {
		t.Errorf("sessionLayout(32) = %d,%d want 23,4", nameW, nameStart)
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
	// The working row's glyph is the animated spinner (frame 0 by default).
	if string(runes[2]) != spinnerFrames[0] {
		t.Errorf("glyph at col 2 = %q, want spinner frame 0: %q", string(runes[2]), line)
	}
	// Icon-only with a 1-cell gutter: no status word; the name starts at column 4.
	if got := string(runes[4:9]); got != "first" {
		t.Errorf("name at col 4 = %q: %q", got, line)
	}
	if strings.Contains(line, "working") {
		t.Errorf("status word should not appear: %q", line)
	}
	if !strings.HasSuffix(line, "0m") {
		t.Errorf("age not right-aligned at edge: %q", line)
	}
}

func TestSessionRowOmitsStatusWord(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowSession && rr.Item.Session.Kind == status.Working {
			r = rr
		}
	}
	line := m.renderSessionRow(r, false, 32)
	if strings.Contains(line, "working") {
		t.Errorf("status word should never appear: %q", line)
	}
	if got := utf8.RuneCountInString(line); got != 32 {
		t.Errorf("row width = %d, want 32: %q", got, line)
	}
	runes := []rune(line)
	if string(runes[2]) != spinnerFrames[0] {
		t.Errorf("glyph at col 2 = %q, want spinner frame 0: %q", string(runes[2]), line)
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

func TestRepoRowNoBreakdown(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowRepo {
			r = rr
		}
	}
	line := m.renderRepoRow(r, false, 60)
	if !strings.Contains(line, "▾ aaa") {
		t.Errorf("missing caret+name: %q", line)
	}
	// The status breakdown and branch are both gone from the repo row.
	for _, gone := range []string{"working", "recent", "·", "main"} {
		if strings.Contains(line, gone) {
			t.Errorf("repo row should not contain %q: %q", gone, line)
		}
	}
}

func TestSelectionHighlight(t *testing.T) {
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
	if !strings.Contains(sel, "48;5;238") {
		t.Errorf("selected row missing the highlight background: %q", sel)
	}
	unsel := m.renderSessionRow(sessionRow, false, 60)
	if strings.Contains(unsel, "48;5;238") {
		t.Errorf("unselected row should not have the selection highlight: %q", unsel)
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
	// Icon-only: both rows share the ○ glyph and carry no status word; only the
	// glyph color distinguishes recent from stale.
	if strings.Contains(freshLine, "idle") || strings.Contains(staleLine, "idle") {
		t.Errorf("status word should not appear:\n%q\n%q", freshLine, staleLine)
	}
}

func TestWorkingRowAnimates(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var work, idle Row
	for _, r := range m.rows {
		if r.Kind != RowSession {
			continue
		}
		if r.Item.Session.Kind == status.Working {
			work = r
		} else {
			idle = r
		}
	}

	m.spinnerFrame = 0
	if line := m.renderSessionRow(work, false, 60); !strings.Contains(line, spinnerFrames[0]) {
		t.Errorf("working row missing spinner frame 0: %q", line)
	}
	m.spinnerFrame = 3
	if line := m.renderSessionRow(work, false, 60); !strings.Contains(line, spinnerFrames[3]) {
		t.Errorf("working row missing spinner frame 3: %q", line)
	}
	// The frame index wraps around the slice.
	m.spinnerFrame = len(spinnerFrames) + 1
	if line := m.renderSessionRow(work, false, 60); !strings.Contains(line, spinnerFrames[1]) {
		t.Errorf("spinner frame should wrap: %q", line)
	}
	// Non-working rows keep their static glyph and never spin.
	if line := m.renderSessionRow(idle, false, 60); strings.Contains(line, spinnerFrames[0]) {
		t.Errorf("idle row should not animate: %q", line)
	}
}

func TestSidebarWidth(t *testing.T) {
	cases := []struct{ w, want int }{
		{40, 24}, {60, 24}, {90, 30}, {120, 40}, {200, 40},
	}
	for _, c := range cases {
		if got := sidebarWidth(c.w); got != c.want {
			t.Errorf("sidebarWidth(%d) = %d, want %d", c.w, got, c.want)
		}
	}
}

func TestUseSplit(t *testing.T) {
	m := Model{showPreview: true}
	if !m.useSplit(splitMinWidth) {
		t.Errorf("useSplit(%d) = false, want true at the threshold", splitMinWidth)
	}
	if m.useSplit(splitMinWidth - 1) {
		t.Error("useSplit below threshold = true, want false")
	}
	m.showPreview = false
	if m.useSplit(200) {
		t.Error("useSplit with preview off = true, want false")
	}
}

func TestPreviewPaneFillsHeight(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // session s1
	m.showPreview = true
	next, _ := m.Update(previewMsg{sid: "s1", text: "line one\nline two"})
	m = next.(Model)

	// 5 content rows requested: 2 real lines, 3 padded; plus top+bottom borders.
	lines := m.renderPreviewPane(40, 5)
	if len(lines) != 7 {
		t.Fatalf("pane height = %d, want 7 (5 content + 2 borders)", len(lines))
	}
	if !strings.HasPrefix(lines[0], "╭─ preview · s1 (aaa)") {
		t.Errorf("top border/label wrong: %q", lines[0])
	}
	if !strings.HasSuffix(lines[len(lines)-1], "╯") {
		t.Errorf("bottom border wrong: %q", lines[len(lines)-1])
	}
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w != 40 {
			t.Errorf("pane line width = %d, want 40: %q", w, ln)
		}
	}
	if !strings.Contains(lines[1], "line one") || !strings.Contains(lines[2], "line two") {
		t.Errorf("content lines missing:\n%q\n%q", lines[1], lines[2])
	}
}

func TestPreviewReflowsLongLine(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // session s1
	m.showPreview = true
	// One logical line wider than the pane's inner width (40 - boxFrameW = 36).
	long := strings.Repeat("x", 60)
	next, _ := m.Update(previewMsg{sid: "s1", text: long})
	m = next.(Model)

	lines := m.renderPreviewPane(40, 5)
	body := strings.Join(lines, "\n")
	if strings.Contains(body, "…") {
		t.Errorf("long line truncated instead of reflowed:\n%s", body)
	}
	if got := strings.Count(body, "x"); got != 60 {
		t.Errorf("reflow lost content: got %d x's, want 60:\n%s", got, body)
	}
	// 60 chars wrap to a full 36-wide row plus a 24-char remainder.
	if !strings.Contains(lines[1], strings.Repeat("x", 36)) {
		t.Errorf("first content row not full width:\n%q", lines[1])
	}
	if !strings.Contains(lines[2], strings.Repeat("x", 24)) {
		t.Errorf("wrap remainder missing on second row:\n%q", lines[2])
	}
}

func TestReflowCarriesColorAcrossWraps(t *testing.T) {
	// A colored logical line wider than the width keeps its color on the
	// continuation row: Hardwrap drops the active style, reflow re-emits it.
	got := reflow([]string{"\x1b[31mhello world\x1b[m"}, 5)
	if len(got) < 2 {
		t.Fatalf("expected the line to wrap into rows, got %q", got)
	}
	if !strings.HasPrefix(got[1], "\x1b[31m") {
		t.Errorf("continuation row lost the color: %q", got[1])
	}
}

func TestReflowCarriesColonDelimitedSGR(t *testing.T) {
	// Colon-form SGR (truecolor and styled underlines kitty can emit) must
	// carry across a wrap too, not just the semicolon form.
	got := reflow([]string{"\x1b[38:2:255:0:0mhello world\x1b[m"}, 5)
	if len(got) < 2 {
		t.Fatalf("expected the line to wrap into rows, got %q", got)
	}
	if !strings.HasPrefix(got[1], "\x1b[38:2:255:0:0m") {
		t.Errorf("continuation row dropped colon-form color: %q", got[1])
	}
}

func TestReflowLeavesPlainTextUnstyled(t *testing.T) {
	// Plain content gets no spurious style prefix on its continuation rows.
	got := reflow([]string{strings.Repeat("x", 12)}, 5)
	for i, row := range got {
		if i > 0 && strings.Contains(row, "\x1b") {
			t.Errorf("plain continuation row %d got an escape: %q", i, row)
		}
	}
}

func TestPreviewBoxStaysWithinBudgetWhenReflowed(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // session s1
	m.showPreview = true
	m.width, m.height = 50, 60 // stacked (width < splitMinWidth), tall terminal
	// One long logical line that reflows into far more rows than the stacked
	// preview's intended budget, but fewer than the short-terminal safety trim.
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("y", 2000)})
	m = next.(Model)

	content := len(m.renderPreviewBox(m.width)) - 2 // minus top+bottom borders
	if budget := m.previewSize(); content > budget {
		t.Errorf("reflowed box = %d content rows, want <= previewSize %d (list would collapse)", content, budget)
	}
}

func TestPreviewContentPlaceholder(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row: no session selected
	label, content := m.previewContent()
	if label != "preview" {
		t.Errorf("label on repo row = %q, want \"preview\"", label)
	}
	if len(content) != 1 || !strings.Contains(content[0], "select a session") {
		t.Errorf("placeholder content = %q, want [\"select a session\"]", content)
	}
}

func TestPreviewBoxKeepsPlaceholderOnShortTerminal(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row → "select a session" placeholder
	m.height = 5 // keep = 5 - previewReservedRows < 0, no room to trim
	box := strings.Join(m.renderPreviewBox(40), "\n")
	if !strings.Contains(box, "select a session") {
		t.Errorf("placeholder dropped on short terminal:\n%s", box)
	}
}

func TestSplitLayoutSideBySide(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := applyLoad(twoSessionModel())
	m.width, m.height = 100, 20
	m.showPreview = true
	m.cursor = 1 // session s1, named "first"
	next, _ := m.Update(previewMsg{sid: "s1", text: "pane content"})
	m = next.(Model)

	out := strings.TrimSuffix(m.View(), "\n")
	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w != 100 {
			t.Fatalf("line width = %d, want 100: %q", w, ln)
		}
	}
	// A session row and the preview share the same physical row.
	sideBySide := false
	for _, ln := range lines {
		if strings.Contains(ln, "first") && strings.Contains(ln, "│") {
			sideBySide = true
		}
	}
	if !sideBySide {
		t.Fatalf("expected sidebar session beside the preview:\n%s", out)
	}
	if !strings.Contains(out, "preview · s1 (aaa)") {
		t.Fatalf("preview pane label missing:\n%s", out)
	}
	if !strings.Contains(out, "pane content") {
		t.Fatalf("preview content missing:\n%s", out)
	}
}

func TestSplitShowsPlaceholderWithoutSelection(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.width, m.height = 100, 20
	m.showPreview = true
	m.cursor = 0 // repo row: nothing to preview

	out := m.View()
	if !strings.Contains(out, "select a session") {
		t.Fatalf("expected placeholder in the main area:\n%s", out)
	}
	for _, ln := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if w := lipgloss.Width(ln); w != 100 {
			t.Fatalf("line width = %d, want 100 (split active): %q", w, ln)
		}
	}
}

func TestSessionRowStripes(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "b", PID: 1, CWD: "/a", Name: "blocked", Kind: status.Blocked, UpdatedAt: now},
		{ID: "w", PID: 2, CWD: "/a", Name: "working", Kind: status.Working, UpdatedAt: now},
		{ID: "r", PID: 3, CWD: "/a", Name: "recent", Kind: status.Idle, UpdatedAt: now.Add(-time.Minute)},
		{ID: "i", PID: 4, CWD: "/a", Name: "stale", Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	byName := map[string]Row{}
	for _, r := range m.rows {
		if r.Kind == RowSession {
			byName[r.Item.Session.Name] = r
		}
	}

	redBar := accentBar(lipgloss.NewStyle(), classBlocked.style().GetForeground())
	greenBar := accentBar(lipgloss.NewStyle(), classWorking.style().GetForeground())
	yellowBar := accentBar(lipgloss.NewStyle(), classRecentIdle.style().GetForeground())

	if line := m.renderSessionRow(byName["blocked"], false, 60); !strings.Contains(line, redBar) {
		t.Errorf("blocked row missing red accent stripe: %q", line)
	}
	if line := m.renderSessionRow(byName["working"], false, 60); !strings.Contains(line, greenBar) {
		t.Errorf("working row missing green accent stripe: %q", line)
	}
	if line := m.renderSessionRow(byName["recent"], false, 60); !strings.Contains(line, yellowBar) {
		t.Errorf("recent-idle row missing yellow accent stripe: %q", line)
	}
	if line := m.renderSessionRow(byName["stale"], false, 60); strings.Contains(line, "▌") {
		t.Errorf("idle row should have no accent stripe: %q", line)
	}
}

func TestIdleRowDimsWholeRow(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "i", PID: 1, CWD: "/a", Name: "stale", Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var row Row
	for _, r := range m.rows {
		if r.Kind == RowSession {
			row = r
		}
	}
	line := m.renderSessionRow(row, false, 60)

	// The whole row dims: the name (not just the glyph) renders in dimColor.
	nameW, _ := sessionLayout(60)
	padded := "stale" + strings.Repeat(" ", nameW-len("stale"))
	dimName := lipgloss.NewStyle().Foreground(dimColor).Render(padded)
	if !strings.Contains(line, dimName) {
		t.Errorf("idle row name should be dimmed: %q", line)
	}
}

func TestSelectionKeepsStripe(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "b", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var row Row
	for _, r := range m.rows {
		if r.Kind == RowSession {
			row = r
		}
	}

	// Unselected: a plain red stripe on the normal background.
	plainBar := accentBar(lipgloss.NewStyle(), classBlocked.style().GetForeground())
	if unsel := m.renderSessionRow(row, false, 60); !strings.Contains(unsel, plainBar) {
		t.Errorf("unselected blocked row missing red accent stripe: %q", unsel)
	}

	// Selected: the stripe stays, now painted over the selection highlight (it
	// must not punch a default-background hole in the highlighted row).
	sel := m.renderSessionRow(row, true, 60)
	highlightedBar := accentBar(lipgloss.NewStyle().Background(selectHighlight), classBlocked.style().GetForeground())
	if !strings.Contains(sel, highlightedBar) {
		t.Errorf("selected blocked row should keep its stripe over the highlight: %q", sel)
	}
	if !strings.Contains(sel, "48;5;238") {
		t.Errorf("selected blocked row missing the highlight: %q", sel)
	}
	// The glyph keeps its red foreground on the highlight background too.
	glyph := lipgloss.NewStyle().Background(selectHighlight).Foreground(lipgloss.Color("1")).Render("⚠")
	if !strings.Contains(sel, glyph) {
		t.Errorf("selected blocked glyph lost its red color: %q", sel)
	}
}

func TestRepoHeaderStripeWhenBlocked(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked, UpdatedAt: now},
		{ID: "s2", PID: 2, CWD: "/b", Name: "calm", Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{
		"/a": {Root: "/a", Name: "aaa"},
		"/b": {Root: "/b", Name: "bbb"},
	}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var blockedRepo, calmRepo Row
	for _, r := range m.rows {
		if r.Kind != RowRepo {
			continue
		}
		if r.Group.Label == "aaa" {
			blockedRepo = r
		} else {
			calmRepo = r
		}
	}
	redBar := accentBar(lipgloss.NewStyle(), classBlocked.style().GetForeground())
	if line := m.renderRepoRow(blockedRepo, false, 60); !strings.Contains(line, redBar) {
		t.Errorf("repo with a blocked child should show a red stripe: %q", line)
	}
	if line := m.renderRepoRow(calmRepo, false, 60); strings.Contains(line, "▌") {
		t.Errorf("repo without a blocked child should have no stripe: %q", line)
	}
}

func TestRepoSelectionHighlight(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := applyLoad(twoSessionModel())
	var repoRow Row
	for _, r := range m.rows {
		if r.Kind == RowRepo {
			repoRow = r
			break
		}
	}
	// twoSessionModel's group has no blocked session, so a selected repo row
	// shows the highlight and no stripe (the blocked case is covered separately).
	sel := m.renderRepoRow(repoRow, true, 60)
	if strings.Contains(sel, "▌") {
		t.Errorf("non-blocked repo row should have no stripe: %q", sel)
	}
	if !strings.Contains(sel, "48;5;238") {
		t.Errorf("selected repo row missing the highlight: %q", sel)
	}
}

func TestRepoSelectionKeepsStripe(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var repoRow Row
	for _, r := range m.rows {
		if r.Kind == RowRepo {
			repoRow = r
			break
		}
	}
	// A selected repo whose group is blocked keeps its red stripe, painted over
	// the selection highlight (the !selected guard was removed deliberately).
	sel := m.renderRepoRow(repoRow, true, 60)
	highlightedBar := accentBar(lipgloss.NewStyle().Background(selectHighlight), classBlocked.style().GetForeground())
	if !strings.Contains(sel, highlightedBar) {
		t.Errorf("selected blocked repo row should keep its stripe over the highlight: %q", sel)
	}
	if !strings.Contains(sel, "48;5;238") {
		t.Errorf("selected repo row missing the highlight: %q", sel)
	}
}

func TestFooterListsNewKeys(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.width, m.height = 80, 20
	out := m.View()
	for _, want := range []string{"x kill", "n new", "s sleep"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q:\n%s", want, out)
		}
	}
}
