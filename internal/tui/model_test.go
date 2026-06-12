package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"hopper/internal/repo"
	"hopper/internal/source"
	"hopper/internal/status"
	"hopper/internal/terminal"
)

type fakeSource struct {
	label    string
	sessions []source.Session
	err      error
}

func (f fakeSource) Label() string { return f.label }
func (f fakeSource) Sessions(context.Context) ([]source.Session, error) {
	return f.sessions, f.err
}

type fakeRepos struct{ infos map[string]repo.Info }

func (f fakeRepos) Resolve(_ context.Context, cwd string) repo.Info { return f.infos[cwd] }

type fakeTerm struct {
	caps    terminal.Capability
	located map[int]terminal.Handle
	focused []terminal.Handle
	preview string
}

func (f *fakeTerm) Name() string                      { return "fake" }
func (f *fakeTerm) Capabilities() terminal.Capability { return f.caps }
func (f *fakeTerm) Locate(_ context.Context, pid int) (terminal.Handle, bool) {
	h, ok := f.located[pid]
	return h, ok
}
func (f *fakeTerm) Focus(_ context.Context, h terminal.Handle) error {
	f.focused = append(f.focused, h)
	return nil
}
func (f *fakeTerm) Preview(_ context.Context, h terminal.Handle, n int) (string, error) {
	return f.preview, nil
}

func twoSessionModel() Model {
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Working, RawStatus: "busy", UpdatedAt: now},
		{ID: "s2", PID: 2, CWD: "/a", Name: "second", Kind: status.Idle, RawStatus: "idle", UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	term := &fakeTerm{caps: terminal.CapFocus | terminal.CapPreview,
		located: map[int]terminal.Handle{1: 11, 2: 22}}
	return New(src, repos, term)
}

func applyLoad(m Model) Model {
	cmd := m.loadCmd()
	msg := cmd()
	next, _ := m.Update(msg)
	return next.(Model)
}

func key(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestLoadBuildsRows(t *testing.T) {
	m := applyLoad(twoSessionModel())
	if len(m.rows) != 3 { // 1 repo + 2 sessions
		t.Fatalf("want 3 rows, got %d", len(m.rows))
	}
}

func TestNavigationMovesCursor(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0
	next, _ := m.Update(key("j"))
	m = next.(Model)
	if m.cursor != 1 {
		t.Fatalf("after j cursor=%d", m.cursor)
	}
	next, _ = m.Update(key("G"))
	m = next.(Model)
	if m.cursor != len(m.rows)-1 {
		t.Fatalf("after G cursor=%d", m.cursor)
	}
}

func TestCollapseHidesSessions(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row
	next, _ := m.Update(key("h"))
	m = next.(Model)
	if len(m.rows) != 1 {
		t.Fatalf("after collapse want 1 row, got %d", len(m.rows))
	}
}

func TestEnterFocusesSession(t *testing.T) {
	m := applyLoad(twoSessionModel())
	// s1 (busy→working) sorts first, so the first session row is PID 1 → handle 11.
	m.cursor = 1
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter on session should return a focus command")
	}
	msg := cmd() // execute the focus command
	if fm, ok := msg.(focusMsg); !ok || fm.err != nil {
		t.Fatalf("focus msg = %#v", msg)
	}
	term := m.term.(*fakeTerm)
	if len(term.focused) != 1 || term.focused[0].(int) != 11 {
		t.Fatalf("expected focus on handle 11, got %v", term.focused)
	}
}

func TestEnterOnRepoTogglesCollapse(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row
	next, _ := m.Update(key("enter"))
	m = next.(Model)
	if len(m.rows) != 1 {
		t.Fatalf("enter on repo should collapse, rows=%d", len(m.rows))
	}
}

func TestTogglePreviewRequiresCapability(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	next, cmd := m.Update(key("p"))
	m = next.(Model)
	if !m.showPreview || cmd == nil {
		t.Fatalf("preview should open with a command")
	}

	// backend without preview capability
	m2 := applyLoad(twoSessionModel())
	m2.term = &fakeTerm{caps: terminal.CapFocus}
	next2, _ := m2.Update(key("p"))
	m2 = next2.(Model)
	if m2.showPreview || m2.statusMsg == "" {
		t.Fatalf("preview should be refused without capability")
	}
}

func TestFilterMode(t *testing.T) {
	m := applyLoad(twoSessionModel())
	// names: s1="first", s2="second"; filter "seco" should keep only s2
	next, _ := m.Update(key("/"))
	m = next.(Model)
	if !m.filtering {
		t.Fatal("should be in filter mode")
	}
	for _, r := range "seco" {
		next, _ = m.Update(key(string(r)))
		m = next.(Model)
	}
	// 1 repo + 1 matching session
	if len(m.rows) != 2 {
		t.Fatalf("filtered rows=%d", len(m.rows))
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.filtering || m.filter != "" || len(m.rows) != 3 {
		t.Fatalf("esc should clear filter; rows=%d filtering=%v", len(m.rows), m.filtering)
	}
}

func TestFilterBackspace(t *testing.T) {
	m := applyLoad(twoSessionModel())
	next, _ := m.Update(key("/"))
	m = next.(Model)
	for _, r := range "secz" { // matches nothing
		next, _ = m.Update(key(string(r)))
		m = next.(Model)
	}
	if len(m.rows) != 0 {
		t.Fatalf("filter 'secz' should match nothing, rows=%d", len(m.rows))
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	// back to "sec" → matches "second" → repo + 1 session
	if m.filter != "sec" || len(m.rows) != 2 {
		t.Fatalf("after backspace filter=%q rows=%d", m.filter, len(m.rows))
	}
}

func TestPreviewSizeTracksHeight(t *testing.T) {
	m := twoSessionModel()
	if got := m.previewSize(); got != previewDefaultLines {
		t.Fatalf("unknown height: previewSize=%d want %d", got, previewDefaultLines)
	}
	m.height = 60
	if got := m.previewSize(); got != 20 { // a third of the screen
		t.Fatalf("height 60: previewSize=%d want 20", got)
	}
	m.height = 12
	if got := m.previewSize(); got != previewMinLines {
		t.Fatalf("height 12: previewSize=%d want %d", got, previewMinLines)
	}
	m.height = 200
	if got := m.previewSize(); got != previewMaxLines {
		t.Fatalf("height 200: previewSize=%d want %d", got, previewMaxLines)
	}
}

func TestPreviewLineTruncatesAndResetsColor(t *testing.T) {
	// A red line longer than the view, with no closing reset: it must be
	// truncated to fit between the box borders, and its color must be reset
	// before the right border so it cannot tint it.
	line := previewLine("\x1b[31m"+strings.Repeat("x", 50), 30)
	if w := lipgloss.Width(line); w != 30 {
		t.Errorf("preview line width = %d, want 30: %q", w, line)
	}
	if !strings.HasPrefix(line, "│ ") || !strings.HasSuffix(line, "│") {
		t.Errorf("preview line missing box borders: %q", line)
	}
	if !strings.Contains(line, ansi.ResetStyle+" │") {
		t.Errorf("color should be reset before the right border: %q", line)
	}
	// A short line is padded so the right border stays aligned.
	line = previewLine("hi", 30)
	if w := lipgloss.Width(line); w != 30 {
		t.Errorf("padded preview line width = %d, want 30: %q", w, line)
	}
}

func TestPreviewPanelBoxed(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.width = 60
	m.cursor = 1 // first session row (s1)
	m.showPreview = true
	next, _ := m.Update(previewMsg{sid: "s1", text: "pane line"})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "╭─ preview · s1 (aaa) ") {
		t.Errorf("missing top border with embedded label:\n%s", out)
	}
	if !strings.Contains(out, "\n\n╭") {
		t.Errorf("missing blank line between list and preview box:\n%s", out)
	}
	if !strings.Contains(out, "│ pane line") {
		t.Errorf("missing boxed content line:\n%s", out)
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "╭") || strings.HasPrefix(ln, "│") || strings.HasPrefix(ln, "╰") {
			if w := lipgloss.Width(ln); w != 60 {
				t.Errorf("box line width = %d, want 60: %q", w, ln)
			}
		}
	}
	var bottom string
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "╰") {
			bottom = ln
		}
	}
	if !strings.HasSuffix(bottom, "╯") {
		t.Errorf("missing or malformed bottom border %q:\n%s", bottom, out)
	}

	// On a repo row there is no selected session: the box stays, with a bare
	// label and no stale content.
	m.cursor = 0
	out = m.View()
	if !strings.Contains(out, "╭─ preview ─") {
		t.Errorf("repo row should show bare preview box:\n%s", out)
	}
	if strings.Contains(out, "pane line") {
		t.Errorf("stale pane content shown on repo row:\n%s", out)
	}
}

func TestPreviewTopWideRunesStayInWidth(t *testing.T) {
	// Rune count and cell width disagree for CJK names; the top border must
	// be measured in cells or it overflows and the ╮ corner gets clipped.
	top := previewTop("preview · abcd (日本語のリポジトリ名)", 30)
	if w := lipgloss.Width(top); w != 30 {
		t.Errorf("CJK label top border width = %d, want 30: %q", w, top)
	}
}

func TestPreviewNotShownForOtherSession(t *testing.T) {
	// A capture is tagged with the session it came from; after the cursor
	// moves to another session the stale pane must not render under the new
	// session's label while the fresh capture is in flight.
	m := applyLoad(twoSessionModel())
	m.width = 60
	m.showPreview = true
	m.cursor = 1 // session s1
	next, _ := m.Update(previewMsg{sid: "s1", text: "s1 pane"})
	m = next.(Model)
	if out := m.View(); !strings.Contains(out, "s1 pane") {
		t.Fatalf("own capture should render:\n%s", out)
	}
	m.cursor = 2 // session s2, capture still from s1
	if out := m.View(); strings.Contains(out, "s1 pane") {
		t.Errorf("s1 capture rendered under s2 label:\n%s", out)
	}
}

func TestViewFitsShortTerminalWithPreview(t *testing.T) {
	// At heights where the old divider+label chrome fit exactly, the boxed
	// preview must still fit: the preview gives way before the footer does.
	m := applyLoad(twoSessionModel())
	m.width, m.height = 80, 16
	m.showPreview = true
	m.cursor = 1
	next, _ := m.Update(previewMsg{sid: "s1",
		text: strings.TrimRight(strings.Repeat("pane\n", 20), "\n")})
	m = next.(Model)

	out := strings.TrimSuffix(m.View(), "\n")
	lines := strings.Split(out, "\n")
	if len(lines) > m.height {
		t.Fatalf("view is %d lines for a %d-row terminal:\n%s", len(lines), m.height, out)
	}
	if !strings.Contains(out, "q quit") {
		t.Fatalf("footer clipped:\n%s", out)
	}
}

func TestViewFitsTerminalHeight(t *testing.T) {
	// Enough sessions that the list alone overflows a 24-row terminal.
	now := time.Now()
	sessions := make([]source.Session, 40)
	for i := range sessions {
		sessions[i] = source.Session{ID: fmt.Sprintf("s%d", i), PID: i + 1,
			CWD: "/a", Name: fmt.Sprintf("session-%d", i), Kind: status.Working, UpdatedAt: now}
	}
	src := fakeSource{label: "Claude Code", sessions: sessions}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{caps: terminal.CapPreview}))
	m.width, m.height = 80, 24
	m.showPreview = true
	next, _ := m.Update(key("G")) // cursor on the last session
	m = next.(Model)
	next, _ = m.Update(previewMsg{sid: "s39",
		text: strings.TrimRight(strings.Repeat("pane\n", m.previewSize()), "\n")})
	m = next.(Model)

	out := strings.TrimSuffix(m.View(), "\n")
	lines := strings.Split(out, "\n")
	if len(lines) > m.height {
		t.Fatalf("view is %d lines for a %d-row terminal", len(lines), m.height)
	}
	if !strings.Contains(out, "session-39") {
		t.Fatal("selected row was clipped out of the view")
	}
	if !strings.Contains(out, "pane") || !strings.Contains(out, "q quit") {
		t.Fatal("preview or footer was clipped out of the view")
	}
}

func TestSplitRowsResolvesEmbeddedNewlines(t *testing.T) {
	lines, cursor := splitRows([]string{"a", "b\nc", "d"}, 2)
	if want := []string{"a", "b", "c", "d"}; !slices.Equal(lines, want) {
		t.Fatalf("splitRows lines = %q, want %q", lines, want)
	}
	if cursor != 3 { // element "d" now starts at row 3
		t.Fatalf("splitRows cursor = %d, want 3", cursor)
	}
}

func TestViewContents(t *testing.T) {
	m := applyLoad(twoSessionModel())
	out := m.View()
	for _, want := range []string{"fake", "aaa", "working", "idle", "q quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n---\n%s", want, out)
		}
	}
}

func TestViewEmpty(t *testing.T) {
	m := applyLoad(New(fakeSource{}, fakeRepos{}, &fakeTerm{}))
	if !strings.Contains(m.View(), "no live sessions") {
		t.Errorf("empty view: %s", m.View())
	}
}

func TestRefreshKeepsCursorOnRepoAfterNavigatingUp(t *testing.T) {
	m := applyLoad(twoSessionModel()) // rows: [0]=repo, [1]=s1, [2]=s2
	next, _ := m.Update(key("j"))     // cursor=1 (a session)
	m = next.(Model)
	next, _ = m.Update(key("k")) // cursor=0 (the repo header)
	m = next.(Model)
	if m.rows[m.cursor].Kind != RowRepo {
		t.Fatalf("setup: expected cursor on repo, got kind=%v", m.rows[m.cursor].Kind)
	}
	// A refresh rebuilds rows; the cursor must not jump down to a session.
	next, _ = m.Update(m.loadCmd()())
	m = next.(Model)
	if m.rows[m.cursor].Kind != RowRepo {
		t.Fatalf("refresh moved cursor off repo to kind=%v", m.rows[m.cursor].Kind)
	}
}

func TestRefreshFollowsSessionAcrossReorder(t *testing.T) {
	m := applyLoad(twoSessionModel())
	next, _ := m.Update(key("G")) // bottom row = s2 (idle sorts last)
	m = next.(Model)
	if m.rows[m.cursor].Item.Session.ID != "s2" {
		t.Fatalf("setup: expected cursor on s2, got %s", m.rows[m.cursor].Item.Session.ID)
	}
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa", Branch: "main"}
	// s2 is now working and so sorts above the now-idle s1.
	items := []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "second", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}
	next, _ = m.Update(loadedMsg{items: items})
	m = next.(Model)
	if m.rows[m.cursor].Kind != RowSession || m.rows[m.cursor].Item.Session.ID != "s2" {
		t.Fatalf("cursor did not follow s2 after reorder: %#v", m.rows[m.cursor])
	}
}

func TestRefreshClampsWhenAnchoredSessionGone(t *testing.T) {
	m := applyLoad(twoSessionModel())
	next, _ := m.Update(key("G")) // cursor on last session
	m = next.(Model)
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa", Branch: "main"}
	items := []Item{ // s2 has ended
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}
	next, _ = m.Update(loadedMsg{items: items})
	m = next.(Model)
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		t.Fatalf("cursor out of range after anchored session gone: %d (rows=%d)", m.cursor, len(m.rows))
	}
}

func TestRefreshClampsWhenAnchoredRepoGone(t *testing.T) {
	now := time.Now()
	infoA := repo.Info{Root: "/a", Name: "aaa", Branch: "main"}
	infoB := repo.Info{Root: "/b", Name: "bbb", Branch: "main"}
	// Two repos; groups sort by Label, so "aaa" precedes "bbb".
	m := applyLoad(twoSessionModel())
	next, _ := m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Working, UpdatedAt: now}, Repo: infoA},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/b", Name: "second", Kind: status.Idle, UpdatedAt: now}, Repo: infoB},
	}})
	m = next.(Model)

	// Anchor the cursor onto the SECOND repo header ("bbb").
	found := false
	for i, r := range m.rows {
		if r.Kind == RowRepo && r.Group.Label == "bbb" {
			m.cursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("setup: no repo header for bbb in rows %#v", m.rows)
	}
	m.setAnchor()
	if !m.anchor.set || m.anchor.kind != RowRepo || m.anchor.key != "/b" {
		t.Fatalf("setup: expected anchor on repo /b, got %#v", m.anchor)
	}

	// Refresh drops repo /b entirely; only /a's session remains.
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Working, UpdatedAt: now}, Repo: infoA},
	}})
	m = next.(Model)

	// The vanished repo anchor must fall through to clamp, not panic or go out of range.
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		t.Fatalf("cursor out of range after anchored repo gone: %d (rows=%d)", m.cursor, len(m.rows))
	}
}
