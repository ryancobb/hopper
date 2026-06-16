# Session lifecycle keys (kill, new, sleep) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three TUI keymaps — `x` to kill the selected session (graceful, confirmed), `n` to start a new session in the selected repo, and `s` to snooze a recently-idle session so it stops drawing attention.

**Architecture:** Kill and new follow hopper's existing async pattern (`focusCmd`/`sendMsg`): a `tea.Cmd` returning a result message that sets `statusMsg` on error; the one-second refresh tick reconciles the list afterward. New is a backend capability (`CapLaunch`, kitty `@ launch`). Kill is a local-PID `SIGTERM` behind an injectable `kill func(int) error` on the Model, with a `y`/`n` footer confirm. Sleep is render-only: a `slept` map demotes the existing `classRecentIdle` to `classIdle`, snoozed against the acknowledged `UpdatedAt`.

**Tech Stack:** Go, Bubble Tea, lipgloss, kitty remote control (`kitty @`).

## Global Constraints

- Module path is `hopper`; packages are `internal/terminal` and `internal/tui`.
- No changes to `internal/source`, `internal/claude`, `internal/session`, `internal/transcript`, or `internal/status`.
- Tests are table-driven where natural; use the existing helpers `twoSessionModel()`, `applyLoad()`, `key()`, `fakeTerm`, and `fakeKitty()`. Do not introduce new test frameworks.
- Commit message style matches the repo: `feat(terminal): …`, `feat(tui): …`, `test(tui): …`, subject ≤ 72 chars.
- `kill` and `Launch` run off the UI goroutine inside a `tea.Cmd`. `launchCmd` uses `actionTimeout`; `killCmd` needs no context (a `SIGTERM` syscall is instant).
- Sleep is attention-only: it must not change `source`, `status`, the process, or any stored session field — only the render-time `displayClass`.

---

### Task 1: Terminal `Launch` capability

Adds a `CapLaunch` capability and a `Launch(ctx, cwd)` method to the `Terminal` interface, implemented by `none` (unsupported) and `Kitty` (`kitty @ launch`). Because the interface grows, the `fakeTerm` used by the tui tests must also gain `Launch` or the whole module stops compiling — that update is included here so the module stays green.

**Files:**
- Modify: `internal/terminal/terminal.go` (add `CapLaunch`, interface method, `none.Launch`)
- Modify: `internal/terminal/kitty.go` (advertise `CapLaunch`, implement `Launch`)
- Modify: `internal/tui/model_test.go` (add `Launch` + `launched`/`launchErr` to `fakeTerm`)
- Test: `internal/terminal/kitty_test.go`, `internal/terminal/terminal_test.go`

**Interfaces:**
- Produces:
  - `terminal.CapLaunch terminal.Capability` — capability bit.
  - `Terminal.Launch(ctx context.Context, cwd string) error` — new interface method.
  - `fakeTerm.launched []string` (cwds passed to `Launch`) and `fakeTerm.launchErr error`.

- [ ] **Step 1: Write the failing kitty test**

Add to `internal/terminal/kitty_test.go`:

```go
func TestKittyLaunchCommand(t *testing.T) {
	var calls [][]string
	k := fakeKitty("", &calls)
	if err := k.Launch(context.Background(), "/work/acme"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls[0], " ")
	if got != "launch --type=tab --cwd=/work/acme claude" {
		t.Fatalf("launch args: %q", got)
	}
}

func TestKittyAdvertisesLaunch(t *testing.T) {
	if !NewKitty().Capabilities().Has(CapLaunch) {
		t.Fatal("kitty should advertise CapLaunch")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/terminal/ -run 'TestKittyLaunchCommand|TestKittyAdvertisesLaunch' -v`
Expected: build failure / FAIL — `k.Launch` undefined and `CapLaunch` undefined.

- [ ] **Step 3: Add the capability and interface method**

In `internal/terminal/terminal.go`, add `CapLaunch` to the const block and the method to the interface and the `none` backend:

```go
const (
	CapFocus    Capability = 1 << iota // bring a session's pane to the foreground
	CapPreview                         // read a session's pane contents
	CapSendText                        // write keystrokes/text to a session's pane
	CapLaunch                          // spawn a new session pane in a working directory
)
```

```go
type Terminal interface {
	Name() string
	Capabilities() Capability
	Locate(ctx context.Context, pid int) (Handle, bool)
	Focus(ctx context.Context, h Handle) error
	Preview(ctx context.Context, h Handle, lines int) (string, error)
	SendText(ctx context.Context, h Handle, data string) error
	// Launch starts a new agent session (runs `claude`) in cwd, in a new
	// pane/tab. The backend chooses placement; the session is discovered on the
	// next source refresh once it writes its session file.
	Launch(ctx context.Context, cwd string) error
}
```

```go
func (none) Launch(context.Context, string) error { return ErrUnsupported }
```

- [ ] **Step 4: Implement kitty `Launch` and advertise the capability**

In `internal/terminal/kitty.go`, update `Capabilities` and add `Launch`:

```go
func (k *Kitty) Capabilities() Capability {
	return CapFocus | CapPreview | CapSendText | CapLaunch
}
```

```go
// Launch opens a new kitty tab whose foreground process is `claude`, running in
// cwd. The new tab takes focus by default, handing the user off to the session;
// hopper discovers it on the next source refresh. kitty prints the new window id
// on stdout, which is ignored.
func (k *Kitty) Launch(ctx context.Context, cwd string) error {
	_, err := k.run(ctx, "launch", "--type=tab", "--cwd="+cwd, "claude")
	return err
}
```

- [ ] **Step 5: Add `Launch` to `fakeTerm` so the tui package compiles**

In `internal/tui/model_test.go`, extend the `fakeTerm` struct and add the method:

```go
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
```

```go
func (f *fakeTerm) Launch(_ context.Context, cwd string) error {
	f.launched = append(f.launched, cwd)
	return f.launchErr
}
```

- [ ] **Step 6: Add the `none` Launch test**

Add to `internal/terminal/terminal_test.go`:

```go
func TestNoneLaunchUnsupported(t *testing.T) {
	n := none{}
	if n.Capabilities().Has(CapLaunch) {
		t.Fatal("none must not advertise CapLaunch")
	}
	if err := n.Launch(context.Background(), "/x"); err != ErrUnsupported {
		t.Fatalf("none.Launch err = %v, want ErrUnsupported", err)
	}
}
```

(If `internal/terminal/terminal_test.go` does not import `context`, add it.)

- [ ] **Step 7: Run all terminal + tui tests to verify they pass**

Run: `go test ./internal/terminal/ ./internal/tui/ -v`
Expected: PASS (the module compiles; the new terminal tests pass; existing tui tests still pass with the extended `fakeTerm`).

- [ ] **Step 8: Commit**

```bash
git add internal/terminal/terminal.go internal/terminal/kitty.go internal/terminal/kitty_test.go internal/terminal/terminal_test.go internal/tui/model_test.go
git commit -m "feat(terminal): Launch capability for spawning a session"
```

---

### Task 2: New session (`n`)

Adds the `n` key: launch `claude` in the selected row's repo root via `CapLaunch`. cwd resolves to the repo group's `Key` (repo row) or the session's `Repo.Root`, falling back to the session `CWD` in the no-repo bucket. Lives in a new `lifecycle.go` alongside the model.

**Files:**
- Create: `internal/tui/lifecycle.go`
- Modify: `internal/tui/model.go` (`n` case in `handleKey`; `launchMsg` case in `Update`)
- Test: `internal/tui/lifecycle_test.go`

**Interfaces:**
- Consumes: `terminal.CapLaunch`, `Terminal.Launch`, `fakeTerm.launched`/`launchErr` (Task 1).
- Produces:
  - `launchMsg struct{ err error }`
  - `launchCmd(term terminal.Terminal, cwd string) tea.Cmd`
  - `func (m Model) newSession() (tea.Model, tea.Cmd)`
  - `func (m Model) launchCwd(r Row) (string, bool)`

- [ ] **Step 1: Write the failing test**

Create `internal/tui/lifecycle_test.go`:

```go
package tui

import (
	"errors"
	"testing"

	"hopper/internal/terminal"
)

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
	next, _ := m.Update(key("n"))
	m = next.(Model)
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run TestNewSession -v`
Expected: build failure — `launchMsg` and the `n` handler do not exist.

- [ ] **Step 3: Create `lifecycle.go` with the new-session logic**

Create `internal/tui/lifecycle.go`:

```go
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"hopper/internal/terminal"
)

// launchMsg reports the result of a Launch command.
type launchMsg struct{ err error }

// launchCmd starts a new session in cwd off the UI goroutine.
func launchCmd(term terminal.Terminal, cwd string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return launchMsg{err: term.Launch(ctx, cwd)}
	}
}

// newSession launches a new session in the selected row's working directory.
// It is a no-op (with a status message) when the row has no directory or the
// terminal cannot launch. The new session appears on the next refresh.
func (m Model) newSession() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	cwd, ok := m.launchCwd(m.rows[m.cursor])
	if !ok {
		m.statusMsg = "new: no directory for this row"
		return m, nil
	}
	if !m.term.Capabilities().Has(terminal.CapLaunch) {
		m.statusMsg = "new session unavailable in this terminal"
		return m, nil
	}
	m.statusMsg = ""
	return m, launchCmd(m.term, cwd)
}

// launchCwd resolves the working directory for a new session from a row: a
// repo row uses its Key (repo root); a session row uses its repo root, falling
// back to the session CWD when there is no repo (the no-repo bucket).
func (m Model) launchCwd(r Row) (string, bool) {
	if r.Kind == RowRepo {
		if r.Group.Key == "" {
			return "", false // no-repo header has no single directory
		}
		return r.Group.Key, true
	}
	if r.Item.Repo.Root != "" {
		return r.Item.Repo.Root, true
	}
	if r.Item.Session.CWD != "" {
		return r.Item.Session.CWD, true
	}
	return "", false
}
```

- [ ] **Step 4: Wire the `n` key and `launchMsg` into `model.go`**

In `handleKey`'s switch (after the `"o"` case), add:

```go
	case "n":
		return m.newSession()
```

In `Update`'s type switch (after the `focusMsg` case), add:

```go
	case launchMsg:
		if msg.err != nil {
			m.statusMsg = "new: " + msg.err.Error()
		} else {
			m.statusMsg = ""
		}
		return m, nil
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run TestNewSession -v`
Expected: PASS (all four cases).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/lifecycle.go internal/tui/lifecycle_test.go internal/tui/model.go
git commit -m "feat(tui): n launches a new session in the selected repo"
```

---

### Task 3: Kill session (`x`) with confirm

Adds the `x` key: on a session row it opens a `y`/`n` confirm in the footer; `y` sends `SIGTERM` to the snapshotted PID via the injectable `kill` func; `n`/`esc`/anything else cancels. The dead process drops off on the next refresh.

**Files:**
- Modify: `internal/tui/model.go` (Model fields, `New` init, `handleKey` routing, `killMsg` case)
- Modify: `internal/tui/lifecycle.go` (kill command, confirm helpers, `defaultKill`)
- Modify: `internal/tui/view.go` (`renderFooter` confirm branch)
- Test: `internal/tui/lifecycle_test.go`, `internal/tui/view_test.go`

**Interfaces:**
- Produces:
  - Model fields: `confirming bool`, `pendingKillPID int`, `pendingKillName string`, `kill func(pid int) error`.
  - `killMsg struct{ err error }`
  - `killCmd(kill func(int) error, pid int) tea.Cmd`
  - `func defaultKill(pid int) error`
  - `func (m Model) enterKillConfirm() (tea.Model, tea.Cmd)`
  - `func (m Model) handleKillConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd)`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/lifecycle_test.go` (add `tea "github.com/charmbracelet/bubbletea"` and `"hopper/internal/source"`, `"hopper/internal/status"`, `repo "hopper/internal/repo"` to the imports as needed):

```go
func TestKillConfirmThenKill(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var killed []int
	m.kill = func(pid int) error { killed = append(killed, pid); return nil }
	m.cursor = 1 // s1, PID 1

	next, cmd := m.Update(key("x"))
	m = next.(Model)
	if !m.confirming || m.pendingKillPID != 1 {
		t.Fatalf("x should open a confirm for PID 1, got confirming=%v pid=%d", m.confirming, m.pendingKillPID)
	}
	if cmd != nil {
		t.Fatal("opening the confirm should not kill anything yet")
	}

	next, cmd = m.Update(key("y"))
	m = next.(Model)
	if m.confirming {
		t.Fatal("y should close the confirm")
	}
	if cmd == nil {
		t.Fatal("y should fire the kill command")
	}
	if msg, ok := cmd().(killMsg); !ok || msg.err != nil {
		t.Fatalf("kill msg = %#v", cmd())
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
	if m.confirming {
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
	if m.confirming {
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run TestKill -v`
Expected: build failure — `m.kill`, `confirming`, `pendingKillPID`, `killMsg` do not exist.

- [ ] **Step 3: Add Model fields and initialize them in `New`**

In `model.go`, add to the `Model` struct (near the passthrough fields):

```go
	confirming      bool   // a kill confirm is pending
	pendingKillPID  int    // PID to signal on confirm
	pendingKillName string // session name shown in the confirm prompt

	kill func(pid int) error // sends SIGTERM; injectable for tests
```

Update `New` to initialize `kill`:

```go
func New(src source.Source, repos RepoResolver, term terminal.Terminal) Model {
	return Model{src: src, repos: repos, term: term,
		collapsed:   map[string]bool{},
		kill:        defaultKill,
		loading:     true,
		showPreview: term.Capabilities().Has(terminal.CapPreview)}
}
```

- [ ] **Step 4: Add kill command and confirm helpers to `lifecycle.go`**

Add the `syscall` import to `lifecycle.go`, then:

```go
// killMsg reports the result of a kill command.
type killMsg struct{ err error }

// defaultKill sends SIGTERM so the session can shut down cleanly.
func defaultKill(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// killCmd signals pid off the UI goroutine. SIGTERM is instant, so no context.
func killCmd(kill func(int) error, pid int) tea.Cmd {
	return func() tea.Msg { return killMsg{err: kill(pid)} }
}

// enterKillConfirm opens the kill confirm for the selected session, snapshotting
// its PID and name so a later refresh/reorder cannot redirect the kill. No-op on
// repo rows, empty selections, or a session with no addressable PID.
func (m Model) enterKillConfirm() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.Kind != RowSession || r.Item.Session.PID == 0 {
		return m, nil
	}
	s := r.Item.Session
	m.confirming = true
	m.pendingKillPID = s.PID
	m.pendingKillName = s.Name
	if m.pendingKillName == "" {
		m.pendingKillName = short(s.ID)
	}
	return m, nil
}

// handleKillConfirmKey resolves a pending kill confirm: y signals the snapshotted
// PID; any other key cancels. Either way the confirm closes.
func (m Model) handleKillConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.confirming = false
	pid := m.pendingKillPID
	m.pendingKillPID, m.pendingKillName = 0, ""
	if msg.String() == "y" {
		return m, killCmd(m.kill, pid)
	}
	return m, nil
}
```

- [ ] **Step 5: Route `x` and the confirm, and handle `killMsg`, in `model.go`**

In `handleKey`, add the confirm route right after the passthrough check:

```go
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.inPassthrough() {
		return m.handlePassthroughKey(msg)
	}
	if m.confirming {
		return m.handleKillConfirmKey(msg)
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	switch msg.String() {
	// ...existing cases...
```

Add the `x` case to the switch (after the `"n"` case):

```go
	case "x":
		return m.enterKillConfirm()
```

Add the `killMsg` case to `Update` (after the `launchMsg` case):

```go
	case killMsg:
		if msg.err != nil {
			m.statusMsg = "kill: " + msg.err.Error()
		} else {
			m.statusMsg = ""
		}
		return m, nil
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run TestKill -v`
Expected: PASS (all five cases).

- [ ] **Step 7: Write the failing footer-confirm view test**

Add to `internal/tui/view_test.go` (it already imports `strings` and `testing`):

```go
func TestKillConfirmFooter(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.width, m.height = 50, 20
	m.cursor = 1 // s1, name "first"
	next, _ := m.Update(key("x"))
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, "Kill first? (y/n)") {
		t.Fatalf("footer missing kill confirm prompt:\n%s", out)
	}
	if strings.Contains(out, "r refresh") {
		t.Fatalf("normal footer should be hidden during the confirm:\n%s", out)
	}
}
```

- [ ] **Step 8: Run it to verify it fails**

Run: `go test ./internal/tui/ -run TestKillConfirmFooter -v`
Expected: FAIL — the confirm prompt is not rendered.

- [ ] **Step 9: Add the confirm branch to `renderFooter`**

In `view.go`, add a case to the `switch` in `renderFooter` (before `m.filtering`):

```go
	switch {
	case m.inPassthrough():
		lines = append(lines, "", m.passthroughBanner())
	case m.confirming:
		lines = append(lines, "", "Kill "+m.pendingKillName+"? (y/n)")
	case m.filtering:
		lines = append(lines, "", "/"+m.filter)
	default:
		lines = append(lines, "", st.footer.Render(footer))
	}
```

- [ ] **Step 10: Run the view test to verify it passes**

Run: `go test ./internal/tui/ -run TestKillConfirmFooter -v`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/tui/model.go internal/tui/lifecycle.go internal/tui/lifecycle_test.go internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): x kills the selected session after a confirm"
```

---

### Task 4: Sleep / snooze (`s`)

Adds the `s` key: snooze a recently-idle session so it renders as plain idle (no yellow stripe) until it does new work and finishes again. `classify` gains a `slept` flag; a `slept` map on the Model snapshots the acknowledged `UpdatedAt`; `displayClassFor` ties them together; stale entries are pruned on load.

**Files:**
- Modify: `internal/tui/class.go` (`classify` gains `slept bool`)
- Modify: `internal/tui/class_test.go` (update calls, add slept cases)
- Modify: `internal/tui/model.go` (`slept` field, `New` init, `s` case, prune in `applyLoaded`)
- Modify: `internal/tui/lifecycle.go` (`sleepSelected`, `displayClassFor`, `pruneSlept`)
- Modify: `internal/tui/view.go` (`renderSessionRow` uses `displayClassFor`)
- Test: `internal/tui/lifecycle_test.go`

**Interfaces:**
- Consumes: `classify`, `displayClass` constants, `status.Idle`.
- Produces:
  - `classify(k status.Kind, age time.Duration, slept bool) displayClass` (signature change)
  - Model field `slept map[string]time.Time`
  - `func (m Model) sleepSelected() (tea.Model, tea.Cmd)`
  - `func (m Model) displayClassFor(s source.Session) displayClass`
  - `func (m *Model) pruneSlept()`

- [ ] **Step 1: Update `classify` tests to the new signature and add slept cases**

In `internal/tui/class_test.go`, change the table to include a `slept` field and update the call:

```go
func TestClassify(t *testing.T) {
	cases := []struct {
		kind  status.Kind
		age   time.Duration
		slept bool
		want  displayClass
	}{
		{status.Idle, 0, false, classRecentIdle},
		{status.Idle, recentIdleWindow - time.Second, false, classRecentIdle},
		{status.Idle, recentIdleWindow, false, classIdle}, // boundary: exactly 5m is stale
		{status.Idle, 2 * time.Hour, false, classIdle},
		{status.Idle, 0, true, classIdle},                  // slept: recent idle demotes
		{status.Idle, recentIdleWindow - time.Second, true, classIdle},
		{status.Working, 2 * time.Hour, false, classWorking}, // only Idle splits on age
		{status.Blocked, 2 * time.Hour, false, classBlocked},
		{status.Unknown, 0, false, classUnknown},
	}
	for _, c := range cases {
		if got := classify(c.kind, c.age, c.slept); got != c.want {
			t.Errorf("classify(%v, %v, slept=%v) = %v, want %v", c.kind, c.age, c.slept, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tui/ -run TestClassify -v`
Expected: build failure — `classify` takes two args, not three.

- [ ] **Step 3: Add the `slept` parameter to `classify` and fix its one caller**

In `class.go`, update the signature and the Idle branch:

```go
// classify maps a status kind, its age, and whether it has been slept
// (snoozed) to a display class. Only Idle splits on age, and a slept idle is
// demoted to the quiet idle look even within the recent window.
func classify(k status.Kind, age time.Duration, slept bool) displayClass {
	switch k {
	case status.Blocked:
		return classBlocked
	case status.Working:
		return classWorking
	case status.Idle:
		if age < recentIdleWindow && !slept {
			return classRecentIdle
		}
		return classIdle
	default:
		return classUnknown
	}
}
```

In `view.go`, `renderSessionRow` is the only non-test caller. Pass `false` for now so the package compiles; Step 7 replaces this with the snooze-aware `displayClassFor`:

```go
	cls := classify(it.Session.Kind, time.Since(it.Session.UpdatedAt), false)
```

- [ ] **Step 4: Run the classify test to verify it passes**

Run: `go test ./internal/tui/ -run TestClassify -v`
Expected: PASS (the package compiles now that the caller passes three args).

- [ ] **Step 5: Write the failing sleep model test**

Add to `internal/tui/lifecycle_test.go`:

```go
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
```

- [ ] **Step 6: Add the sleep logic to `lifecycle.go`**

Add `"hopper/internal/source"`, `"hopper/internal/status"`, and `"time"` to `lifecycle.go`'s imports, then:

```go
// sleepSelected snoozes the selected session if it is idle: it records the
// session's current UpdatedAt so the recent-idle stripe is suppressed for this
// idle period only. No-op on non-idle sessions and repo rows.
func (m Model) sleepSelected() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.Kind != RowSession || r.Item.Session.Kind != status.Idle {
		return m, nil
	}
	m.slept[r.Item.Session.ID] = r.Item.Session.UpdatedAt
	return m, nil
}

// displayClassFor is the render-time class for a session, applying any snooze:
// the slept suppression holds only while the acknowledged UpdatedAt still
// matches, so new activity (an advanced UpdatedAt) re-raises attention.
func (m Model) displayClassFor(s source.Session) displayClass {
	slept := !m.slept[s.ID].IsZero() && m.slept[s.ID].Equal(s.UpdatedAt)
	return classify(s.Kind, time.Since(s.UpdatedAt), slept)
}

// pruneSlept drops snooze entries whose session is gone or whose UpdatedAt has
// advanced, keeping the map bounded. Correctness does not depend on it:
// displayClassFor already ignores a non-matching entry.
func (m *Model) pruneSlept() {
	for id, t := range m.slept {
		if s, ok := m.sessionByID(id); !ok || !s.UpdatedAt.Equal(t) {
			delete(m.slept, id)
		}
	}
}
```

- [ ] **Step 7: Wire `slept` into `model.go` and the renderer**

In the `Model` struct add:

```go
	slept map[string]time.Time // session ID -> acknowledged UpdatedAt (snooze)
```

In `New`, initialize it:

```go
		collapsed:   map[string]bool{},
		slept:       map[string]time.Time{},
		kill:        defaultKill,
```

Add the `s` case to `handleKey`'s switch (after the `"x"` case):

```go
	case "s":
		return m.sleepSelected()
```

In `applyLoaded`, prune after rebuilding rows (before the passthrough end-check is fine):

```go
	m.groups = BuildGroups(msg.items)
	m.rebuildRows()
	m.pruneSlept()
```

In `view.go`, change `renderSessionRow` to use `displayClassFor`:

```go
	cls := m.displayClassFor(it.Session)
```

(replacing the temporary `cls := classify(it.Session.Kind, time.Since(it.Session.UpdatedAt), false)` from Step 3.)

- [ ] **Step 8: Run the full tui suite to verify it passes**

Run: `go test ./internal/tui/ -v`
Expected: PASS (classify, sleep, and all existing tests).

- [ ] **Step 9: Commit**

```bash
git add internal/tui/class.go internal/tui/class_test.go internal/tui/model.go internal/tui/lifecycle.go internal/tui/lifecycle_test.go internal/tui/view.go
git commit -m "feat(tui): s snoozes a recently-idle session"
```

---

### Task 5: Footer key hints

Adds `x kill · n new · s sleep` to the key-hint footer so the new actions are discoverable.

**Files:**
- Modify: `internal/tui/view.go` (the `footer` constant)
- Test: `internal/tui/view_test.go`

**Interfaces:**
- Consumes: the `footer` constant rendered by `renderFooter`.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/view_test.go`:

```go
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
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tui/ -run TestFooterListsNewKeys -v`
Expected: FAIL — the hints are absent.

- [ ] **Step 3: Update the `footer` constant**

In `view.go`, extend the constant (keep it on one line):

```go
const footer = "j/k move · h/l fold · Enter focus · i send · n new · x kill · s sleep · p preview · / filter · r refresh · q quit"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/tui/ -run TestFooterListsNewKeys -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite and vet**

Run: `go test ./... && go vet ./...`
Expected: PASS, no vet complaints.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): footer hints for new/kill/sleep keys"
```

---

## Self-Review

**Spec coverage:**
- Kill `x`, SIGTERM, confirm, no terminal cap, snapshot PID, self-clearing list → Task 3. ✓
- New `n`, repo-root cwd, `CapLaunch` + `Launch`, kitty `--type=tab`, no-repo fallback/no-op → Tasks 1 + 2. ✓
- Sleep `s`, demote `classRecentIdle`→`classIdle`, snooze semantics, prune → Task 4. ✓
- Footer hints → Task 5; kill confirm prompt → Task 3 Steps 7-10. ✓
- "What does not change" (source/status/session untouched; existing keys) → honored: only `internal/terminal` and `internal/tui` are modified. ✓
- Edge cases: repo-row/PID-0 no-op (T3), snapshot across reorder (T3), no-repo header (T2 `launchCwd`), missing `CapLaunch` (T2), non-idle sleep no-op (T4), re-fire/prune (T4). ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code and an exact command with expected output.

**Type consistency:** `classify(status.Kind, time.Duration, bool)` is used identically in `class.go`, `class_test.go`, and `displayClassFor`. `killMsg`/`launchMsg` are `struct{ err error }` and handled in `Update` with matching field. `kill func(pid int) error` is the field type, `defaultKill` and the test fakes match it. `launchCwd`/`displayClassFor`/`sleepSelected`/`enterKillConfirm`/`handleKillConfirmKey` names are consistent between their definitions (lifecycle.go) and call sites (model.go). `fakeTerm.launched`/`launchErr` defined in Task 1 are consumed in Task 2.
