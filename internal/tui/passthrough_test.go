package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"hopper/internal/repo"
	"hopper/internal/source"
	"hopper/internal/status"
	"hopper/internal/terminal"
)

// enteredPassthrough returns a loaded model in passthrough on s1 (cursor row 1)
// with its pane handle (11) resolved, as the locate command would.
func enteredPassthrough(t *testing.T) Model {
	t.Helper()
	m := twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapSendText
	m = applyLoad(m)
	m.cursor = 1 // s1
	next, _ := m.Update(key("i"))
	m = next.(Model)
	if !m.inPassthrough() || m.passthroughID != "s1" {
		t.Fatalf("setup: expected passthrough on s1, got id=%q", m.passthroughID)
	}
	next, _ = m.Update(passthroughLocatedMsg{id: "s1", handle: 11, ok: true})
	m = next.(Model)
	if m.passthroughHandle == nil {
		t.Fatal("setup: expected the pane handle cached after locate")
	}
	return m
}

func TestPassthroughEnterNeedsSessionAndCapability(t *testing.T) {
	// Without CapSendText: i warns and does not enter.
	m := applyLoad(twoSessionModel()) // caps = CapFocus|CapPreview
	m.cursor = 1
	next, _ := m.Update(key("i"))
	m = next.(Model)
	if m.inPassthrough() || m.statusMsg == "" {
		t.Fatal("i without CapSendText should warn, not enter")
	}
	// With the capability but on a repo row: no-op.
	m = twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapSendText
	m = applyLoad(m)
	m.cursor = 0 // repo row
	next, _ = m.Update(key("i"))
	m = next.(Model)
	if m.inPassthrough() {
		t.Fatal("i on a repo row should not enter passthrough")
	}
}

func TestPassthroughForwardsTranslatedKeys(t *testing.T) {
	m := enteredPassthrough(t)
	// A digit answers a menu; Enter submits text. Both go to s1's handle (11).
	for _, tc := range []struct {
		msg  tea.KeyMsg
		want string
	}{
		{key("2"), "2"},
		{tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
	} {
		next, cmd := m.Update(tc.msg)
		m = next.(Model)
		if cmd == nil {
			t.Fatalf("forwarding %q should return a send command", tc.want)
		}
		got := cmd()
		if msg, ok := got.(sendMsg); !ok || msg.err != nil {
			t.Fatalf("send msg = %#v", got)
		}
	}
	ft := m.term.(*fakeTerm)
	if len(ft.sent) != 2 || ft.sent[0].data != "2" || ft.sent[1].data != "\r" {
		t.Fatalf("forwarded data = %#v", ft.sent)
	}
	if ft.sent[0].handle.(int) != 11 {
		t.Fatalf("sent to handle %v, want 11", ft.sent[0].handle)
	}
}

func TestPassthroughExitsOnCtrlCloseBracket(t *testing.T) {
	m := enteredPassthrough(t)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = next.(Model)
	if m.inPassthrough() || m.passthroughID != "" {
		t.Fatal("ctrl+] should exit passthrough")
	}
	if cmd != nil {
		t.Fatal("the exit chord should not send anything")
	}
	if len(m.term.(*fakeTerm).sent) != 0 {
		t.Fatal("the exit chord must not be forwarded to the pane")
	}
}

func TestPassthroughPinsTargetAcrossReorder(t *testing.T) {
	m := enteredPassthrough(t) // pinned to s1 (PID 1 → handle 11)
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa", Branch: "main"}
	// A refresh flips which session sorts first; the target must stay s1.
	next, _ := m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "second", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}})
	m = next.(Model)
	next, cmd := m.Update(key("x"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected a send command")
	}
	cmd()
	ft := m.term.(*fakeTerm)
	if len(ft.sent) != 1 || ft.sent[0].handle.(int) != 11 {
		t.Fatalf("after reorder, send should still target s1 (handle 11), got %#v", ft.sent)
	}
}

func TestPassthroughExitsWhenPaneCannotBeLocated(t *testing.T) {
	m := twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapSendText
	m = applyLoad(m)
	m.cursor = 1 // s1
	next, _ := m.Update(key("i"))
	m = next.(Model)
	if !m.inPassthrough() {
		t.Fatal("entering should set passthrough pending the locate")
	}
	// The locate resolves with ok=false (the pane could not be found).
	next, _ = m.Update(passthroughLocatedMsg{id: "s1", ok: false})
	m = next.(Model)
	if m.inPassthrough() {
		t.Fatal("a failed locate should drop out of passthrough")
	}
	if m.statusMsg == "" {
		t.Fatal("a failed locate should report in the status line")
	}
}

func TestPassthroughIgnoresStaleLocate(t *testing.T) {
	m := enteredPassthrough(t) // pinned to s1, handle 11 cached
	// A locate result for a different (already-exited) session must not clobber
	// the live handle.
	next, _ := m.Update(passthroughLocatedMsg{id: "s2", handle: 22, ok: true})
	m = next.(Model)
	if m.passthroughID != "s1" || m.passthroughHandle.(int) != 11 {
		t.Fatalf("stale locate changed state: id=%q handle=%v", m.passthroughID, m.passthroughHandle)
	}
}

func TestPassthroughExitsWhenSessionEnded(t *testing.T) {
	m := enteredPassthrough(t) // pinned to s1
	// A refresh drops all sessions: the pinned session is gone from the groups,
	// so passthrough ends proactively on the reload.
	next, _ := m.Update(loadedMsg{items: []Item{}})
	m = next.(Model)
	if m.inPassthrough() {
		t.Fatal("a vanished session should drop out of passthrough on reload")
	}
	if m.statusMsg == "" {
		t.Fatal("ending the session should report in the status line")
	}
	if m.passthroughHandle != nil || m.passthroughName != "" {
		t.Fatal("exit should clear all passthrough state")
	}
}

func TestLocateCmdReportsHandle(t *testing.T) {
	term := &fakeTerm{caps: terminal.CapSendText, located: map[int]terminal.Handle{7: 70}}
	msg, ok := locateCmd(term, "sX", 7)().(passthroughLocatedMsg)
	if !ok || msg.id != "sX" || !msg.ok || msg.handle.(int) != 70 {
		t.Fatalf("locate msg = %#v", msg)
	}
	// A pid that cannot be located reports ok=false (and is handled as pane-gone).
	msg, _ = locateCmd(term, "sY", 99)().(passthroughLocatedMsg)
	if msg.ok {
		t.Fatalf("expected not-found for an unlocatable pid, got %#v", msg)
	}
}

func TestPassthroughPreviewTracksPinnedNotCursor(t *testing.T) {
	m := enteredPassthrough(t) // pinned to s1, handle cached, preview on
	m.width = 50
	// A capture for the pinned pane arrives, then the cursor moves off s1.
	next, _ := m.Update(previewMsg{sid: "s1", text: "s1 pane"})
	m = next.(Model)
	m.cursor = 0 // repo row, not the pinned session
	if out := m.View(); !strings.Contains(out, "s1 pane") {
		t.Fatalf("passthrough preview should track the pinned session, not the cursor:\n%s", out)
	}
}

func TestPassthroughDropsKeysBeforeLocate(t *testing.T) {
	m := twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapSendText
	m = applyLoad(m)
	m.cursor = 1 // s1
	next, _ := m.Update(key("i"))
	m = next.(Model) // handle not resolved yet
	next, cmd := m.Update(key("2"))
	m = next.(Model)
	if cmd != nil {
		t.Fatal("a key pressed before the pane is located should not send")
	}
	if !m.inPassthrough() {
		t.Fatal("dropping a pre-locate key should not exit passthrough")
	}
	if len(m.term.(*fakeTerm).sent) != 0 {
		t.Fatalf("nothing should be sent before the handle is cached, got %#v", m.term.(*fakeTerm).sent)
	}
}

func TestPassthroughSendEchoesPreviewImmediately(t *testing.T) {
	m := enteredPassthrough(t) // pane handle cached
	// A successful send re-captures the pinned pane so the keystroke echoes
	// without waiting for the next passthrough tick.
	next, cmd := m.Update(sendMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("a successful send should trigger an immediate preview re-capture")
	}
	got := cmd()
	if msg, ok := got.(previewMsg); !ok || msg.sid != "s1" {
		t.Fatalf("the re-capture should preview the pinned session, got %#v", got)
	}
	// A failed send must not re-capture, and a transient error stays in the mode.
	next, cmd = m.Update(sendMsg{err: errors.New("boom")})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("a failed send should not trigger a capture")
	}
	if !m.inPassthrough() {
		t.Fatal("a transient send error should not exit passthrough")
	}
}

func TestPassthroughTickStartsAndLapses(t *testing.T) {
	m := enteredPassthrough(t)
	if !m.passthroughTick {
		t.Fatal("entering passthrough should start the preview tick")
	}
	// While in passthrough the tick reschedules (and recaptures the pinned pane).
	next, cmd := m.Update(passthroughTickMsg(time.Now()))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("the tick should reschedule while in passthrough")
	}
	// After exit the tick lapses: no reschedule, flag cleared.
	m = m.exitPassthrough("")
	next, cmd = m.Update(passthroughTickMsg(time.Now()))
	m = next.(Model)
	if cmd != nil {
		t.Fatal("the tick should lapse after leaving passthrough")
	}
	if m.passthroughTick {
		t.Fatal("the tick flag should clear once the tick lapses")
	}
}

func TestPassthroughPreviewBorderMatchesFooter(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := enteredPassthrough(t)
	m.width, m.height = 50, 20 // < splitMinWidth → stacked, single preview box

	out := m.View()
	if want := passthroughBorder.Render("╭─ "); !strings.Contains(out, want) {
		t.Fatalf("preview border should take the passthrough accent while relaying:\n%s", out)
	}

	// Leaving passthrough returns the preview frame to the dim meta border.
	m = m.exitPassthrough("")
	out = m.View()
	if calm := st.meta.Render("╭─ "); !strings.Contains(out, calm) {
		t.Fatalf("preview border should revert to the dim frame outside passthrough:\n%s", out)
	}
}

func TestKeyToBytes(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		want string
		ok   bool
	}{
		{"letter", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}, "a", true},
		{"digit", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}, "2", true},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, " ", true},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, "\r", true},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, "\t", true},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z", true},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f", true},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, "\x1b", true},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A", true},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, "\x1b[B", true},
		{"right", tea.KeyMsg{Type: tea.KeyRight}, "\x1b[C", true},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}, "\x1b[D", true},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, "\x1b[H", true},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}, "\x1b[F", true},
		{"pgup", tea.KeyMsg{Type: tea.KeyPgUp}, "\x1b[5~", true},
		{"pgdown", tea.KeyMsg{Type: tea.KeyPgDown}, "\x1b[6~", true},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03", true},
		{"ctrl+u", tea.KeyMsg{Type: tea.KeyCtrlU}, "\x15", true},
		{"f1 dropped", tea.KeyMsg{Type: tea.KeyF1}, "", false},
		{"ctrl+] dropped", tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}, "", false},
		{"ctrl+@ dropped", tea.KeyMsg{Type: tea.KeyCtrlAt}, "", false}, // type 0 = zero value
	}
	for _, c := range cases {
		got, ok := keyToBytes(c.msg)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: keyToBytes = %q,%v; want %q,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestPassthroughBannerInFooter(t *testing.T) {
	m := enteredPassthrough(t) // pinned to s1, name "first"
	m.width, m.height = 50, 20 // < splitMinWidth → stacked, footer rendered
	out := m.View()
	if !strings.Contains(out, "PASSTHROUGH") {
		t.Fatalf("footer missing passthrough banner:\n%s", out)
	}
	if !strings.Contains(out, "first") {
		t.Fatalf("banner missing the pinned session name:\n%s", out)
	}
	if !strings.Contains(out, "Ctrl-] to exit") {
		t.Fatalf("banner missing the exit hint:\n%s", out)
	}
	// The normal key-hint footer is replaced while in passthrough.
	if strings.Contains(out, "r refresh") {
		t.Fatalf("normal footer should be hidden in passthrough:\n%s", out)
	}
}
