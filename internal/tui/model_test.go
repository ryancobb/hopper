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

type sentText struct {
	handle terminal.Handle
	data   string
}

type fakeTerm struct {
	caps      terminal.Capability
	located   map[int]terminal.Handle
	focused   []terminal.Handle
	preview   string
	sent      []sentText
	sendErr   error
	launched  []string // cwds passed to Launch
	launchErr error
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
func (f *fakeTerm) SendText(_ context.Context, h terminal.Handle, data string) error {
	f.sent = append(f.sent, sentText{handle: h, data: data})
	return f.sendErr
}
func (f *fakeTerm) Launch(_ context.Context, cwd string) error {
	f.launched = append(f.launched, cwd)
	return f.launchErr
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

func TestFoldHidesSessions(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row
	next, _ := m.Update(key("z"))
	m = next.(Model)
	if len(m.rows) != 1 {
		t.Fatalf("after fold want 1 row, got %d", len(m.rows))
	}
}

func TestFoldFromSessionCollapsesParent(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // a session row
	next, _ := m.Update(key("z"))
	m = next.(Model)
	if len(m.rows) != 1 {
		t.Fatalf("z on a session should collapse its group, rows=%d", len(m.rows))
	}
	if m.rows[m.cursor].Kind != RowRepo {
		t.Fatalf("cursor should land on the repo header, got kind %v", m.rows[m.cursor].Kind)
	}
}

func TestFoldToggleReopens(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0                  // repo row
	next, _ := m.Update(key("z")) // collapse
	m = next.(Model)
	next, _ = m.Update(key("z")) // expand
	m = next.(Model)
	if len(m.rows) != 3 {
		t.Fatalf("second z should reopen the group, rows=%d", len(m.rows))
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

func TestTogglePreviewDefaultsOnAndToggles(t *testing.T) {
	m := applyLoad(twoSessionModel()) // CapPreview → preview on by default
	if !m.showPreview {
		t.Fatal("preview should default on when the terminal supports it")
	}
	m.cursor = 1
	next, cmd := m.Update(key("p"))
	m = next.(Model)
	if m.showPreview || cmd != nil {
		t.Fatal("p should toggle preview off and return no command")
	}
	next, cmd = m.Update(key("p"))
	m = next.(Model)
	if !m.showPreview || cmd == nil {
		t.Fatal("p should toggle preview back on and request a capture")
	}

	// A terminal without preview capability: off by default, p warns.
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Working, UpdatedAt: time.Now()},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m2 := applyLoad(New(src, repos, &fakeTerm{caps: terminal.CapFocus}))
	if m2.showPreview {
		t.Fatal("preview should default off without capability")
	}
	next2, _ := m2.Update(key("p"))
	m2 = next2.(Model)
	if m2.showPreview || m2.statusMsg == "" {
		t.Fatal("p should refuse and warn without capability")
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
	m.width = 40 // narrow → stacked sizing
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
	m.height = 300
	if got := m.previewSize(); got != 100 { // no upper cap: a third of the screen
		t.Fatalf("tall terminal: previewSize=%d want 100 (no cap)", got)
	}
}

func TestPreviewSizeSplit(t *testing.T) {
	m := twoSessionModel()      // CapPreview → preview on
	m.width, m.height = 100, 30 // width ≥ splitMinWidth(60) → split active
	// split: body = height - header(2) - footer(2) - borders(2) = 24.
	if got := m.previewSize(); got != 24 {
		t.Fatalf("split previewSize = %d, want 24", got)
	}
}

func TestSpinnerTickAdvancesFrame(t *testing.T) {
	m := applyLoad(twoSessionModel()) // s1 is working → load starts the spinner
	if !m.spinning {
		t.Fatal("loading a working session should start the spinner")
	}
	if m.spinnerFrame != 0 {
		t.Fatalf("initial spinner frame = %d, want 0", m.spinnerFrame)
	}
	next, cmd := m.Update(spinnerTickMsg(time.Now()))
	m = next.(Model)
	if m.spinnerFrame != 1 {
		t.Fatalf("after tick spinner frame = %d, want 1", m.spinnerFrame)
	}
	if cmd == nil {
		t.Fatal("spinner tick should reschedule while a session is working")
	}
}

func TestSpinnerStopsWithoutWorking(t *testing.T) {
	// With nothing working, the spinner tick lapses: no reschedule, no advance.
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "idle", Kind: status.Idle, UpdatedAt: time.Now()},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))
	if m.spinning {
		t.Fatal("spinner should not start with no working session")
	}
	next, cmd := m.Update(spinnerTickMsg(time.Now()))
	m = next.(Model)
	if cmd != nil {
		t.Fatal("spinner tick should not reschedule when nothing is working")
	}
	if m.spinnerFrame != 0 {
		t.Fatalf("frame should hold at 0 with no working session, got %d", m.spinnerFrame)
	}
}

func TestBoxLineTruncatesAndResetsColor(t *testing.T) {
	// A red line longer than the view, with no closing reset: it must be
	// truncated to fit between the box borders, and its color must be reset
	// before the right border so it cannot tint it.
	line := boxLine("\x1b[31m"+strings.Repeat("x", 50), 30, st.meta)
	if w := lipgloss.Width(line); w != 30 {
		t.Errorf("box line width = %d, want 30: %q", w, line)
	}
	if !strings.HasPrefix(line, "│ ") || !strings.HasSuffix(line, "│") {
		t.Errorf("box line missing box borders: %q", line)
	}
	if !strings.Contains(line, ansi.ResetStyle+" │") {
		t.Errorf("color should be reset before the right border: %q", line)
	}
	// A short line is padded so the right border stays aligned.
	line = boxLine("hi", 30, st.meta)
	if w := lipgloss.Width(line); w != 30 {
		t.Errorf("padded box line width = %d, want 30: %q", w, line)
	}
}

func TestPreviewPanelBoxed(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.width = 50
	m.cursor = 1 // first session row (s1)
	m.showPreview = true
	next, _ := m.Update(previewMsg{sid: "s1", text: "pane line"})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "╭─ first - s1 ") {
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
			if w := lipgloss.Width(ln); w != 50 {
				t.Errorf("box line width = %d, want 50: %q", w, ln)
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

func TestBoxTopWideRunesStayInWidth(t *testing.T) {
	// Rune count and cell width disagree for CJK names; the top border must
	// be measured in cells or it overflows and the ╮ corner gets clipped.
	top := boxTop("日本語のセッション名 - abcd", 30, st.meta)
	if w := lipgloss.Width(top); w != 30 {
		t.Errorf("CJK label top border width = %d, want 30: %q", w, top)
	}
}

func TestPreviewNotShownForOtherSession(t *testing.T) {
	// A capture is tagged with the session it came from; after the cursor
	// moves to another session the stale pane must not render under the new
	// session's label while the fresh capture is in flight.
	m := applyLoad(twoSessionModel())
	m.width = 50
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
	m.width, m.height = 50, 16
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
	m.width, m.height = 50, 24
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
	m.width = 40
	out := m.View()
	// Rows are icon-only (no status words anywhere), so assert the repo label
	// and session names instead.
	for _, want := range []string{"fake", "aaa", "first", "second", "q quit"} {
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

func TestSplitFullWidthWithEmptyList(t *testing.T) {
	// CapPreview defaults preview on, so a wide terminal renders the split even
	// with no sessions. The single "no live sessions" body line must not
	// collapse the sidebar column: every row stays the full width.
	m := applyLoad(New(fakeSource{}, fakeRepos{}, &fakeTerm{caps: terminal.CapPreview}))
	m.width, m.height = 100, 20
	out := strings.TrimSuffix(m.View(), "\n")
	if !strings.Contains(out, "no live sessions") {
		t.Fatalf("empty body missing:\n%s", out)
	}
	for _, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w != 100 {
			t.Fatalf("line width = %d, want 100 (sidebar box must fill its column): %q", w, ln)
		}
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

func TestPreviewScrollPansRight(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30 // stacked
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)

	if m.previewCol != 0 {
		t.Fatalf("previewCol should start at 0, got %d", m.previewCol)
	}
	next, _ = m.Update(key("l"))
	m = next.(Model)
	if m.previewCol != previewScrollStep {
		t.Fatalf("l should pan right by %d, got %d", previewScrollStep, m.previewCol)
	}
	body := strings.Join(m.renderPreviewBox(m.width), "\n")
	if !strings.Contains(body, "…x") {
		t.Errorf("scrolled rows should start with the cut ellipsis:\n%s", body)
	}
	next, _ = m.Update(key("h"))
	m = next.(Model)
	if m.previewCol != 0 {
		t.Fatalf("h should pan back to 0, got %d", m.previewCol)
	}
}

func TestPreviewScrollClampsLow(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30
	next, _ := m.Update(previewMsg{sid: "s1", text: "short"})
	m = next.(Model)
	next, _ = m.Update(key("h")) // pan left from 0
	m = next.(Model)
	if m.previewCol != 0 {
		t.Fatalf("previewCol should clamp at 0, got %d", m.previewCol)
	}
}

func TestPreviewScrollClampsHigh(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30 // stacked, inner = 50 - boxFrameW = 46
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 50)})
	m = next.(Model)
	for i := 0; i < 10; i++ { // many right-pans
		nx, _ := m.Update(key("l"))
		m = nx.(Model)
	}
	want := m.maxPreviewCol()
	if want == 0 {
		t.Fatal("precondition: content is wider than the box, expected a positive max")
	}
	if m.previewCol != want {
		t.Fatalf("previewCol should clamp at maxPreviewCol %d, got %d", want, m.previewCol)
	}
}

func TestPreviewScrollResetsOnCursorMove(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	next, _ = m.Update(key("l"))
	m = next.(Model)
	if m.previewCol == 0 {
		t.Fatal("precondition: expected a non-zero scroll")
	}
	next, _ = m.Update(key("k")) // move cursor
	m = next.(Model)
	if m.previewCol != 0 {
		t.Fatalf("moving the cursor should reset previewCol, got %d", m.previewCol)
	}
}

func TestPreviewScrollSurvivesRefresh(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	next, _ = m.Update(key("l"))
	m = next.(Model)
	col := m.previewCol
	if col == 0 {
		t.Fatal("precondition: expected a non-zero scroll")
	}
	// A fresh capture of the same session must not snap the view back to 0.
	next, _ = m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	if m.previewCol != col {
		t.Fatalf("refresh should not reset previewCol: was %d, now %d", col, m.previewCol)
	}
}

func TestPreviewScrollClampsOnNarrowerRefresh(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30 // stacked, inner = 50 - boxFrameW = 46
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	for i := 0; i < 10; i++ { // pan right
		nx, _ := m.Update(key("l"))
		m = nx.(Model)
	}
	if m.previewCol == 0 {
		t.Fatal("precondition: expected a scrolled view")
	}
	// A refresh delivers much narrower content; previewCol must clamp to fit it,
	// landing exactly at the new maxPreviewCol (not 0, not the old offset).
	next, _ = m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 60)})
	m = next.(Model)
	if want := m.maxPreviewCol(); m.previewCol != want {
		t.Fatalf("previewCol should clamp to maxPreviewCol %d after a narrower refresh, got %d", want, m.previewCol)
	}
}

// Folding while scrolled lands the cursor on the repo header, whose preview is
// the short "select a session" placeholder. The render must clamp the stale
// offset to that row so the placeholder is not sliced away to a blank box.
func TestPreviewScrollFoldKeepsPlaceholderVisible(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // session row
	m.showPreview = true
	m.width, m.height = 50, 30
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	for i := 0; i < 5; i++ { // scroll right
		nx, _ := m.Update(key("l"))
		m = nx.(Model)
	}
	if m.previewCol == 0 {
		t.Fatal("precondition: expected a scrolled view")
	}
	next, _ = m.Update(key("z")) // collapse → cursor lands on the repo header
	m = next.(Model)
	body := strings.Join(m.renderPreviewBox(m.width), "\n")
	if !strings.Contains(body, "select a session") {
		t.Errorf("folding while scrolled blanked the placeholder:\n%s", body)
	}
}

// When the widest captured rows are trimmed out of the visible window (short
// terminal), scrolling past them must not blank the narrow rows that remain:
// the render clamps the offset to the rows it actually shows.
func TestPreviewScrollClampsToVisibleRows(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 22 // small budget: the oldest rows get trimmed off
	lines := []string{strings.Repeat("W", 300)}
	for i := 0; i < 40; i++ {
		lines = append(lines, "narrow")
	}
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Join(lines, "\n")})
	m = next.(Model)
	for i := 0; i < 60; i++ { // scroll far past the visible (narrow) rows
		nx, _ := m.Update(key("l"))
		m = nx.(Model)
	}
	body := strings.Join(m.renderPreviewBox(m.width), "\n")
	if !strings.Contains(body, "narrow") {
		t.Errorf("scrolling past trimmed-off wide rows blanked the visible rows:\n%s", body)
	}
}

// At maximum scroll the rightmost column must be visible. The leading "…" marker
// costs a cell, so maxOffset reserves one extra column; without it the last
// column is replaced by a trailing "…" that can never be scrolled into view.
func TestPreviewScrollRevealsRightEdge(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30 // inner = 46
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 44) + "END"})
	m = next.(Model)
	for i := 0; i < 20; i++ { // scroll to the far right
		nx, _ := m.Update(key("l"))
		m = nx.(Model)
	}
	body := strings.Join(m.renderPreviewBox(m.width), "\n")
	if !strings.Contains(body, "END") {
		t.Errorf("right edge not reachable at max scroll:\n%s", body)
	}
}

func TestPassthroughForwardsScrollKeys(t *testing.T) {
	m := enteredPassthrough(t) // pane handle 11 cached
	next, cmd := m.Update(key("l"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("l in passthrough should forward to the pane, not scroll")
	}
	cmd()
	ft := m.term.(*fakeTerm)
	if len(ft.sent) != 1 || ft.sent[0].data != "l" {
		t.Fatalf("expected 'l' forwarded to the pane, got %#v", ft.sent)
	}
	if m.previewCol != 0 {
		t.Fatalf("passthrough must not consume the key as scroll, previewCol=%d", m.previewCol)
	}
}
