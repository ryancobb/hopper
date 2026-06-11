package tui

import (
	"strings"
	"testing"
	"time"

	"cctop/internal/repo"
	"cctop/internal/session"
	"cctop/internal/terminal"
	tea "github.com/charmbracelet/bubbletea"
)

type fakeLoader struct {
	sessions []session.Session
	err      error
}

func (f fakeLoader) Load() ([]session.Session, error) { return f.sessions, f.err }

type fakeRepos struct{ infos map[string]repo.Info }

func (f fakeRepos) Resolve(cwd string) repo.Info { return f.infos[cwd] }

type fakeNamer struct{ names map[string]string }

func (f fakeNamer) Name(id string) string { return f.names[id] }

type fakeTerm struct {
	caps    terminal.Capability
	located map[int]terminal.Handle
	focused []terminal.Handle
	preview string
}

func (f *fakeTerm) Name() string                      { return "fake" }
func (f *fakeTerm) Capabilities() terminal.Capability { return f.caps }
func (f *fakeTerm) Locate(pid int) (terminal.Handle, bool) {
	h, ok := f.located[pid]
	return h, ok
}
func (f *fakeTerm) Focus(h terminal.Handle) error { f.focused = append(f.focused, h); return nil }
func (f *fakeTerm) Preview(h terminal.Handle, n int) (string, error) {
	return f.preview, nil
}

func twoSessionModel() Model {
	loader := fakeLoader{sessions: []session.Session{
		{ID: "s1", PID: 1, CWD: "/a", Status: "busy", StatusUpdatedAt: time.Now()},
		{ID: "s2", PID: 2, CWD: "/a", Status: "idle", StatusUpdatedAt: time.Now()},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	names := fakeNamer{names: map[string]string{"s1": "first", "s2": "second"}}
	term := &fakeTerm{caps: terminal.CapFocus | terminal.CapPreview,
		located: map[int]terminal.Handle{1: 11, 2: 22}}
	return New(loader, repos, names, term)
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
	// move to first session row (index 1)
	m.cursor = 1
	m.syncSel()
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
	m.syncSel()
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
	loader := fakeLoader{}
	m := applyLoad(New(loader, fakeRepos{}, fakeNamer{}, &fakeTerm{}))
	if !strings.Contains(m.View(), "no live sessions") {
		t.Errorf("empty view: %s", m.View())
	}
}
