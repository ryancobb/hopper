package tui

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	repo "hopper/internal/repo"
	"hopper/internal/source"
	"hopper/internal/status"
	"hopper/internal/terminal"
)

func TestKillConfirmThenKill(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var killed []int
	m.kill = func(pid int) error { killed = append(killed, pid); return nil }
	m.cursor = 1 // s1, PID 1

	next, cmd := m.Update(key("x"))
	m = next.(Model)
	if m.pendingKill == nil || m.pendingKill.pid != 1 {
		t.Fatalf("x should open a confirm for PID 1, got pendingKill=%#v", m.pendingKill)
	}
	if cmd != nil {
		t.Fatal("opening the confirm should not kill anything yet")
	}

	next, cmd = m.Update(key("y"))
	m = next.(Model)
	if m.pendingKill != nil {
		t.Fatal("y should close the confirm")
	}
	if cmd == nil {
		t.Fatal("y should fire the kill command")
	}
	msg, ok := cmd().(killMsg)
	if !ok || msg.err != nil {
		t.Fatalf("kill msg = %#v", msg)
	}
	if len(killed) != 1 || killed[0] != 1 {
		t.Fatalf("killed = %#v, want [1]", killed)
	}
}

func TestKillConfirmCancel(t *testing.T) {
	m := applyLoad(twoSessionModel())
	called := false
	m.kill = func(int) error { called = true; return nil }
	m.cursor = 1
	next, _ := m.Update(key("x"))
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.pendingKill != nil {
		t.Fatal("esc should close the confirm")
	}
	if cmd != nil || called {
		t.Fatal("cancelling must not kill")
	}
}

func TestKillIgnoredOnRepoRow(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row
	next, _ := m.Update(key("x"))
	m = next.(Model)
	if m.pendingKill != nil {
		t.Fatal("x on a repo row should not open a confirm")
	}
}

func TestKillTargetsSnapshotAcrossReorder(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var killed []int
	m.kill = func(pid int) error { killed = append(killed, pid); return nil }
	m.cursor = 1 // s1, PID 1
	next, _ := m.Update(key("x"))
	m = next.(Model) // confirm pending for PID 1

	// A refresh reorders/cursor-moves; the confirm must still target PID 1.
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa", Branch: "main"}
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "second", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}})
	m = next.(Model)
	m.cursor = 0 // move the cursor off the target
	next, cmd := m.Update(key("y"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("y should fire the kill")
	}
	cmd()
	if len(killed) != 1 || killed[0] != 1 {
		t.Fatalf("kill targeted %#v, want [1] (the snapshot)", killed)
	}
}

func TestKillReportsError(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.kill = func(int) error { return errors.New("nope") }
	m.cursor = 1
	next, _ := m.Update(key("x"))
	m = next.(Model)
	_, cmd := m.Update(key("y"))
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.statusMsg == "" {
		t.Fatal("a kill error should surface in the status line")
	}
}

func TestNewSessionLaunchesInRepoRoot(t *testing.T) {
	m := twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapLaunch
	m = applyLoad(m)
	m.cursor = 0 // repo row "aaa" (Key "/a")
	next, cmd := m.Update(key("n"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("n on a repo row should return a launch command")
	}
	if msg, ok := cmd().(launchMsg); !ok || msg.err != nil {
		t.Fatalf("launch msg = %#v", cmd())
	}
	ft := m.term.(*fakeTerm)
	if len(ft.launched) != 1 || ft.launched[0] != "/a" {
		t.Fatalf("launched cwds = %#v, want [/a]", ft.launched)
	}
}

func TestNewSessionUsesSessionRepoRoot(t *testing.T) {
	m := twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapLaunch
	m = applyLoad(m)
	m.cursor = 1 // a session row; its Repo.Root is "/a"
	next, cmd := m.Update(key("n"))
	m = next.(Model)
	if cmd != nil {
		cmd() // execute to trigger the Launch side-effect
	}
	ft := m.term.(*fakeTerm)
	if len(ft.launched) != 1 || ft.launched[0] != "/a" {
		t.Fatalf("launched cwds = %#v, want [/a]", ft.launched)
	}
}

func TestNewSessionWarnsWithoutCapability(t *testing.T) {
	m := applyLoad(twoSessionModel()) // caps = CapFocus|CapPreview, no CapLaunch
	m.cursor = 0
	next, cmd := m.Update(key("n"))
	m = next.(Model)
	if cmd != nil {
		t.Fatal("n without CapLaunch should not launch")
	}
	if m.statusMsg == "" {
		t.Fatal("n without CapLaunch should warn in the status line")
	}
}

func TestNewSessionReportsLaunchError(t *testing.T) {
	m := twoSessionModel()
	ft := m.term.(*fakeTerm)
	ft.caps |= terminal.CapLaunch
	ft.launchErr = errors.New("boom")
	m = applyLoad(m)
	m.cursor = 0
	_, cmd := m.Update(key("n"))
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.statusMsg == "" {
		t.Fatal("a launch error should surface in the status line")
	}
}

func TestSleepDemotesRecentIdle(t *testing.T) {
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "done", Kind: status.Idle, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	s := m.groups[0].Items[0].Session
	if got := m.displayClassFor(s); got != classRecentIdle {
		t.Fatalf("fresh idle should be recent-idle, got %v", got)
	}

	m.cursor = 1 // the session row
	next, _ := m.Update(key("s"))
	m = next.(Model)
	if got := m.displayClassFor(s); got != classIdle {
		t.Fatalf("after sleep should be plain idle, got %v", got)
	}
}

func TestSleepReFiresOnNewActivity(t *testing.T) {
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "done", Kind: status.Idle, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))
	m.cursor = 1
	next, _ := m.Update(key("s"))
	m = next.(Model)

	// The session works and finishes again: UpdatedAt advances. The snooze must
	// lapse so the new completion draws attention again.
	later := now.Add(time.Minute)
	info := repo.Info{Root: "/a", Name: "aaa"}
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "done", Kind: status.Idle, UpdatedAt: later}, Repo: info},
	}})
	m = next.(Model)
	s := m.groups[0].Items[0].Session
	if got := m.displayClassFor(s); got != classRecentIdle {
		t.Fatalf("a new completion should re-fire (recent-idle), got %v", got)
	}
	if _, stale := m.slept["s1"]; stale {
		t.Fatal("the stale slept entry should be pruned on load")
	}
}

func TestSleepIgnoresNonIdle(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // s1 is working
	next, _ := m.Update(key("s"))
	m = next.(Model)
	if _, ok := m.slept["s1"]; ok {
		t.Fatal("sleeping a working session should be a no-op")
	}
}

func launchReadyModel(t *testing.T) Model {
	t.Helper()
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	return applyLoad(New(src, repos, &fakeTerm{caps: terminal.CapLaunch}))
}

func TestLaunchAdoptsNewSession(t *testing.T) {
	m := launchReadyModel(t)
	m.cursor = 0 // repo row /a
	next, _ := m.Update(key("n"))
	m = next.(Model)
	if m.pendingLaunch == nil || m.pendingLaunch.cwd != "/a" {
		t.Fatalf("n should arm a pending launch for /a, got %#v", m.pendingLaunch)
	}
	// The new session shows up in /a on a later refresh.
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa"}
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "new one", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}})
	m = next.(Model)
	if m.pendingLaunch != nil {
		t.Fatal("adopting the new session should clear pendingLaunch")
	}
	r := m.rows[m.cursor]
	if r.Kind != RowSession || r.Item.Session.ID != "s2" {
		t.Fatalf("cursor should land on the newly created session s2, got %#v", r)
	}
}

func TestLaunchAdoptIgnoresExistingAndExpires(t *testing.T) {
	m := launchReadyModel(t)
	m.cursor = 0
	next, _ := m.Update(key("n"))
	m = next.(Model)
	// A refresh that still only has the pre-existing session: keep waiting, do
	// not adopt s1 (it was present before the launch).
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa"}
	onlyExisting := []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
	}
	next, _ = m.Update(loadedMsg{items: onlyExisting})
	m = next.(Model)
	if m.pendingLaunch == nil {
		t.Fatal("a refresh without the new session should keep waiting")
	}
	// Past the deadline, the next refresh gives up rather than waiting forever.
	m.pendingLaunch.deadline = time.Now().Add(-time.Second)
	next, _ = m.Update(loadedMsg{items: onlyExisting})
	m = next.(Model)
	if m.pendingLaunch != nil {
		t.Fatal("an expired pending launch should clear")
	}
}

func TestKillConfirmDismissedWhenSessionVanishes(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // s1
	next, _ := m.Update(key("x"))
	m = next.(Model)
	if m.pendingKill == nil || m.pendingKill.id != "s1" {
		t.Fatalf("setup: expected a pending kill for s1, got %#v", m.pendingKill)
	}
	// A refresh in which s1 has ended must dismiss the confirm: y would otherwise
	// signal PID 1, which the OS may have recycled to an unrelated process.
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa", Branch: "main"}
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "second", Kind: status.Idle, UpdatedAt: now}, Repo: info},
	}})
	m = next.(Model)
	if m.pendingKill != nil {
		t.Fatal("a refresh dropping the kill target should dismiss the confirm")
	}
}

func TestLaunchErrorClearsPendingLaunch(t *testing.T) {
	m := launchReadyModel(t)
	m.cursor = 0
	next, _ := m.Update(key("n"))
	m = next.(Model)
	if m.pendingLaunch == nil {
		t.Fatal("setup: n should arm pendingLaunch")
	}
	next, _ = m.Update(launchMsg{err: errors.New("boom")})
	m = next.(Model)
	if m.pendingLaunch != nil {
		t.Fatal("a failed launch should clear pendingLaunch so nothing is mis-adopted")
	}
}

func TestAdoptionDeferredWhileFiltering(t *testing.T) {
	m := launchReadyModel(t)
	m.cursor = 0
	next, _ := m.Update(key("n"))
	m = next.(Model)
	m.filter = "zzz" // hides the new session
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa"}
	items := []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "new one", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}
	next, _ = m.Update(loadedMsg{items: items})
	m = next.(Model)
	if m.pendingLaunch == nil {
		t.Fatal("adoption should be deferred while filtering, keeping pendingLaunch")
	}
	// Once the filter clears, the next refresh adopts and the cursor lands on s2.
	m.filter = ""
	next, _ = m.Update(loadedMsg{items: items})
	m = next.(Model)
	if m.pendingLaunch != nil {
		t.Fatal("after the filter clears the new session should be adopted")
	}
	if r := m.rows[m.cursor]; r.Kind != RowSession || r.Item.Session.ID != "s2" {
		t.Fatalf("cursor should land on s2 once the filter clears, got %#v", r)
	}
}

func TestAdoptionDeferredWhileConfirming(t *testing.T) {
	m := launchReadyModel(t) // s1 in /a
	m.cursor = 0
	next, _ := m.Update(key("n")) // arm pendingLaunch (not a mode)
	m = next.(Model)
	m.cursor = 1                  // s1 session row
	next, _ = m.Update(key("x"))  // arm the kill confirm
	m = next.(Model)
	if m.pendingLaunch == nil || m.pendingKill == nil {
		t.Fatalf("setup: expected both pending launch and kill, launch=%v kill=%v", m.pendingLaunch, m.pendingKill)
	}
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa"}
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "new one", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}})
	m = next.(Model)
	if m.pendingLaunch == nil {
		t.Fatal("adoption should be deferred while a kill confirm is pending")
	}
}

func TestDoubleLaunchKeepsEarlierSnapshot(t *testing.T) {
	m := launchReadyModel(t) // s1 in /a
	m.cursor = 0
	next, _ := m.Update(key("n"))
	m = next.(Model) // pendingLaunch.before = {s1}
	// A refresh adds an unrelated session s2 in /b (different cwd → not adopted).
	now := time.Now()
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: repo.Info{Root: "/a", Name: "aaa"}},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/b", Name: "other", Kind: status.Idle, UpdatedAt: now}, Repo: repo.Info{Root: "/b", Name: "bbb"}},
	}})
	m = next.(Model)
	if m.pendingLaunch == nil {
		t.Fatal("setup: a non-matching refresh should keep pendingLaunch")
	}
	// Press n again on the /a repo row: the earlier snapshot must be kept so a
	// session from the first launch still counts as new (s2 must not be in it).
	found := false
	for i, r := range m.rows {
		if r.Kind == RowRepo && r.Group.Key == "/a" {
			m.cursor = i
			found = true
		}
	}
	if !found {
		t.Fatal("setup: no /a repo row to reposition the cursor onto")
	}
	next, _ = m.Update(key("n"))
	m = next.(Model)
	if m.pendingLaunch.before["s2"] {
		t.Fatal("a second n should keep the earlier snapshot, not absorb s2")
	}
	if !m.pendingLaunch.before["s1"] {
		t.Fatal("the earlier snapshot should still contain the pre-launch session s1")
	}
}

func TestAdoptionDeferredInEmptyFilterMode(t *testing.T) {
	m := launchReadyModel(t)
	m.cursor = 0
	next, _ := m.Update(key("n"))
	m = next.(Model)
	// Open the filter prompt but type nothing: filtering is active with an empty
	// filter. A new session must not yank the cursor mid-input.
	next, _ = m.Update(key("/"))
	m = next.(Model)
	if !m.filtering || m.filter != "" {
		t.Fatalf("setup: expected empty filter mode, filtering=%v filter=%q", m.filtering, m.filter)
	}
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa"}
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s1", PID: 1, CWD: "/a", Name: "first", Kind: status.Idle, UpdatedAt: now}, Repo: info},
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "new one", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}})
	m = next.(Model)
	if m.pendingLaunch == nil {
		t.Fatal("adoption should defer while the filter prompt is open, even when empty")
	}
}

func TestAdoptionResumesWhenConfirmDismissed(t *testing.T) {
	m := launchReadyModel(t) // s1 in /a
	m.cursor = 0
	next, _ := m.Update(key("n")) // arm pendingLaunch (before {s1}, cwd /a)
	m = next.(Model)
	m.cursor = 1                 // s1 session row
	next, _ = m.Update(key("x")) // arm kill confirm on s1
	m = next.(Model)
	if m.pendingLaunch == nil || m.pendingKill == nil {
		t.Fatalf("setup: expected both pending, launch=%v kill=%v", m.pendingLaunch, m.pendingKill)
	}
	// One refresh both drops the kill target (s1) and surfaces the new session
	// (s2): the confirm dismisses AND adoption fires in the same tick.
	now := time.Now()
	info := repo.Info{Root: "/a", Name: "aaa"}
	next, _ = m.Update(loadedMsg{items: []Item{
		{Session: source.Session{ID: "s2", PID: 2, CWD: "/a", Name: "new one", Kind: status.Working, UpdatedAt: now}, Repo: info},
	}})
	m = next.(Model)
	if m.pendingKill != nil {
		t.Fatal("the vanished kill target should dismiss the confirm")
	}
	if m.pendingLaunch != nil {
		t.Fatal("adoption should fire the same tick the confirm is dismissed")
	}
	if r := m.rows[m.cursor]; r.Kind != RowSession || r.Item.Session.ID != "s2" {
		t.Fatalf("cursor should land on the adopted session s2, got %#v", r)
	}
}
