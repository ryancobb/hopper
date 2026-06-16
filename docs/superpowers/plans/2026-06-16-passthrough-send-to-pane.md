# Passthrough send-to-pane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user answer permission prompts and send free-form text to a Claude Code session from inside hopper, by relaying raw keystrokes to the session's terminal pane.

**Architecture:** Add a `SendText` capability to the `terminal` backend (kitty implements it via `kitty @ send-text --stdin`). Add a passthrough mode to the TUI: `i` on a selected session relays every keystroke to that pane until `Ctrl-]` exits. A pure `keyToBytes` function translates Bubble Tea key events to the bytes to send. No prompt parsing — the preview is the user's view of the live menu.

**Tech Stack:** Go, Bubble Tea (`github.com/charmbracelet/bubbletea`), lipgloss, kitty `@` remote control.

## Global Constraints

- No changes to `internal/source`, `internal/claude`, `internal/session`, `internal/transcript`, or `internal/status`. The feature carries no provider-specific logic.
- `SendText` transmits its `data` argument to the pane **verbatim** — no escape re-interpretation. The kitty backend pipes bytes on stdin to guarantee this.
- The `none` backend does not advertise `CapSendText` and returns `ErrUnsupported` from `SendText`, matching `Focus`/`Preview`.
- Tests run with `go test ./...`; the binary builds with `go build -o hopper .`.

---

### Task 1: `SendText` terminal capability across all backends

Adds the capability to the interface and implements it on `none` and `Kitty`. Also updates the TUI test fake so the `tui` package still compiles (adding a method to the interface breaks every implementer until each has it).

**Files:**
- Modify: `internal/terminal/terminal.go` (add `CapSendText`, interface method, `none.SendText`)
- Modify: `internal/terminal/kitty.go` (add `runIn`, `SendText`, extend `Capabilities`)
- Test: `internal/terminal/terminal_test.go`, `internal/terminal/kitty_test.go`
- Modify: `internal/tui/model_test.go` (make `fakeTerm` satisfy the interface)

**Interfaces:**
- Produces: `terminal.CapSendText terminal.Capability`; `Terminal.SendText(ctx context.Context, h Handle, data string) error`. On success returns `nil`; `ErrNotFound`/`ErrBadHandle`/`ErrUnsupported` on failure.

- [ ] **Step 1: Write the failing tests**

Add to `internal/terminal/terminal_test.go` — extend `TestNoneBackend` to assert `SendText` is unsupported, and add a capability test:

```go
func TestNoneSendTextUnsupported(t *testing.T) {
	n := none{}
	if n.Capabilities().Has(CapSendText) {
		t.Fatal("none should not advertise CapSendText")
	}
	if err := n.SendText(context.Background(), nil, "x"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}
```

Add to `internal/terminal/kitty_test.go`:

```go
func TestKittySendTextCommand(t *testing.T) {
	var stdin string
	var args []string
	k := &Kitty{runIn: func(_ context.Context, in string, a ...string) ([]byte, error) {
		stdin, args = in, a
		return nil, nil
	}}
	if err := k.SendText(context.Background(), 10, "2\r"); err != nil {
		t.Fatal(err)
	}
	if stdin != "2\r" {
		t.Fatalf("stdin = %q, want %q", stdin, "2\r")
	}
	if got := strings.Join(args, " "); got != "send-text --match id:10 --stdin" {
		t.Fatalf("send-text args: %q", got)
	}
	if !k.Capabilities().Has(CapSendText) {
		t.Fatal("kitty should advertise CapSendText")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/terminal/`
Expected: FAIL — build errors, `n.SendText undefined`, `k.runIn undefined`, `CapSendText` not declared.

- [ ] **Step 3: Add the capability and interface method**

In `internal/terminal/terminal.go`, extend the capability bitmask and the interface, and implement `none.SendText`:

```go
const (
	CapFocus    Capability = 1 << iota // bring a session's pane to the foreground
	CapPreview                         // read a session's pane contents
	CapSendText                        // write keystrokes/text to a session's pane
)
```

```go
type Terminal interface {
	Name() string
	Capabilities() Capability
	Locate(ctx context.Context, pid int) (Handle, bool)
	Focus(ctx context.Context, h Handle) error
	Preview(ctx context.Context, h Handle, lines int) (string, error)
	// SendText transmits data to the pane verbatim, including control bytes and
	// escape sequences. Callers pre-encode keys to bytes; the backend must not
	// re-interpret or alter them.
	SendText(ctx context.Context, h Handle, data string) error
}
```

Add the `none` implementation beside its other methods:

```go
func (none) SendText(context.Context, Handle, string) error { return ErrUnsupported }
```

- [ ] **Step 4: Implement kitty `SendText`**

In `internal/terminal/kitty.go`, add a stdin-capable runner to the struct and `NewKitty`, extend `Capabilities`, and add `SendText`:

```go
// Kitty controls kitty via `kitty @`.
type Kitty struct {
	run   func(ctx context.Context, args ...string) ([]byte, error)
	runIn func(ctx context.Context, stdin string, args ...string) ([]byte, error)
}

// NewKitty returns a kitty backend talking to `kitty @` over KITTY_LISTEN_ON.
func NewKitty() *Kitty {
	return &Kitty{
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "kitty", append([]string{"@"}, args...)...).Output()
		},
		runIn: func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "kitty", append([]string{"@"}, args...)...)
			cmd.Stdin = strings.NewReader(stdin)
			return cmd.Output()
		},
	}
}
```

```go
func (k *Kitty) Capabilities() Capability { return CapFocus | CapPreview | CapSendText }
```

```go
// SendText sends data to the window verbatim. The bytes are piped on stdin
// (`send-text --stdin`) so kitty performs no escape interpretation: control
// bytes and escape sequences the caller built reach the pane unchanged.
func (k *Kitty) SendText(ctx context.Context, h Handle, data string) error {
	id, ok := h.(int)
	if !ok {
		return ErrBadHandle
	}
	_, err := k.runIn(ctx, data, "send-text", "--match", fmt.Sprintf("id:%d", id), "--stdin")
	return err
}
```

- [ ] **Step 5: Make the TUI test fake satisfy the interface**

In `internal/tui/model_test.go`, add a record type, two fields to `fakeTerm`, and the `SendText` method so the `tui` package compiles against the widened interface:

```go
type sentText struct {
	handle terminal.Handle
	data   string
}
```

Add fields to the `fakeTerm` struct:

```go
type fakeTerm struct {
	caps    terminal.Capability
	located map[int]terminal.Handle
	focused []terminal.Handle
	preview string
	sent    []sentText
	sendErr error
}
```

Add the method beside the other `fakeTerm` methods:

```go
func (f *fakeTerm) SendText(_ context.Context, h terminal.Handle, data string) error {
	f.sent = append(f.sent, sentText{handle: h, data: data})
	return f.sendErr
}
```

- [ ] **Step 6: Run tests and build**

Run: `go test ./internal/terminal/ && go build ./...`
Expected: PASS, and the whole module compiles (the `tui` package builds against the new interface).

- [ ] **Step 7: Commit**

```bash
git add internal/terminal/terminal.go internal/terminal/kitty.go internal/terminal/terminal_test.go internal/terminal/kitty_test.go internal/tui/model_test.go
git commit -m "feat(terminal): SendText capability for writing to a pane"
```

---

### Task 2: `keyToBytes` key translator

A pure function mapping a Bubble Tea key event to the bytes to send to a pane. No Model state, fully table-testable.

**Files:**
- Create: `internal/tui/passthrough.go`
- Test: `internal/tui/passthrough_test.go`

**Interfaces:**
- Produces: `keyToBytes(msg tea.KeyMsg) (string, bool)` — returns the bytes to send and `true`, or `"", false` for keys that should be dropped. The exit chord (`Ctrl-]`) is dropped here too (it returns `false`), though the caller intercepts it before translating.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/passthrough_test.go`:

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f", true},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, "\x1b", true},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A", true},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, "\x1b[B", true},
		{"right", tea.KeyMsg{Type: tea.KeyRight}, "\x1b[C", true},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}, "\x1b[D", true},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03", true},
		{"ctrl+u", tea.KeyMsg{Type: tea.KeyCtrlU}, "\x15", true},
		{"f1 dropped", tea.KeyMsg{Type: tea.KeyF1}, "", false},
		{"ctrl+] dropped", tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}, "", false},
	}
	for _, c := range cases {
		got, ok := keyToBytes(c.msg)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: keyToBytes = %q,%v; want %q,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run TestKeyToBytes`
Expected: FAIL — `undefined: keyToBytes`.

- [ ] **Step 3: Implement `keyToBytes`**

Create `internal/tui/passthrough.go`:

```go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// keyToBytes encodes a key event as the bytes to send to a pane. The second
// return is false for unmapped keys, which the caller drops. The exit chord
// (Ctrl-]) never reaches here in normal flow: the caller intercepts it before
// translating; it returns false defensively if it does.
func keyToBytes(msg tea.KeyMsg) (string, bool) {
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			return string(msg.Runes), true
		}
		return "", false
	case tea.KeySpace:
		return " ", true
	case tea.KeyEnter:
		return "\r", true
	case tea.KeyTab:
		return "\t", true
	case tea.KeyBackspace:
		return "\x7f", true
	case tea.KeyEsc:
		return "\x1b", true
	case tea.KeyUp:
		return "\x1b[A", true
	case tea.KeyDown:
		return "\x1b[B", true
	case tea.KeyRight:
		return "\x1b[C", true
	case tea.KeyLeft:
		return "\x1b[D", true
	case tea.KeyHome:
		return "\x1b[H", true
	case tea.KeyEnd:
		return "\x1b[F", true
	case tea.KeyPgUp:
		return "\x1b[5~", true
	case tea.KeyPgDown:
		return "\x1b[6~", true
	}
	// Ctrl-letter combos map to control bytes 0x01..0x1a. Bubble Tea's KeyType
	// values for KeyCtrlA..KeyCtrlZ are exactly those bytes, so cast directly.
	// Enter (0x0d) and Tab (0x09) fall in that range but are handled above.
	if msg.Type >= tea.KeyCtrlA && msg.Type <= tea.KeyCtrlZ {
		return string([]byte{byte(msg.Type)}), true
	}
	return "", false
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/tui/ -run TestKeyToBytes`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/passthrough.go internal/tui/passthrough_test.go
git commit -m "feat(tui): keyToBytes translator for pane passthrough"
```

---

### Task 3: Passthrough mode in the Model

Wires the capability and translator into the TUI: `i` enters passthrough on a selected session, keystrokes forward to that pane, `Ctrl-]` exits, and a vanished pane drops out of the mode.

**Files:**
- Modify: `internal/tui/model.go` (struct fields, `sendMsg` handling in `Update`, `handleKey` routing + `i` case, `import "errors"`)
- Modify: `internal/tui/passthrough.go` (add `sendMsg`, `sendCmd`, `sessionByID`, `enterPassthrough`, `handlePassthroughKey`)
- Test: `internal/tui/passthrough_test.go`

**Interfaces:**
- Consumes: `keyToBytes` (Task 2); `terminal.CapSendText`, `Terminal.SendText`, `terminal.ErrNotFound` (Task 1); existing `Model.previewIfOpen`, `Model.groups`, `source.Session`.
- Produces: `Model.passthrough bool`, `Model.passthroughID string`; `sendMsg struct{ err error }`; `sendCmd(term terminal.Terminal, pid int, data string) tea.Cmd`; `(Model).sessionByID(id string) (source.Session, bool)`; `(Model).enterPassthrough() (tea.Model, tea.Cmd)`; `(Model).handlePassthroughKey(msg tea.KeyMsg) (tea.Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/passthrough_test.go`. First extend its imports so the file has what these tests use:

```go
import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"hopper/internal/source"
	"hopper/internal/terminal"
)
```

Add a helper and the tests (relies on `twoSessionModel`, `applyLoad`, `key`, `fakeTerm` from `model_test.go`; in `twoSessionModel` the rows are `[0]=repo, [1]=s1 (PID 1→handle 11), [2]=s2 (PID 2→handle 22)`):

```go
// enteredPassthrough returns a loaded model in passthrough on s1 (cursor row 1).
func enteredPassthrough(t *testing.T) Model {
	t.Helper()
	m := twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapSendText
	m = applyLoad(m)
	m.cursor = 1 // s1
	next, _ := m.Update(key("i"))
	m = next.(Model)
	if !m.passthrough || m.passthroughID != "s1" {
		t.Fatalf("setup: expected passthrough on s1, got pt=%v id=%q", m.passthrough, m.passthroughID)
	}
	return m
}

func TestPassthroughEnterNeedsSessionAndCapability(t *testing.T) {
	// Without CapSendText: i warns and does not enter.
	m := applyLoad(twoSessionModel()) // caps = CapFocus|CapPreview
	m.cursor = 1
	next, _ := m.Update(key("i"))
	m = next.(Model)
	if m.passthrough || m.statusMsg == "" {
		t.Fatal("i without CapSendText should warn, not enter")
	}
	// With the capability but on a repo row: no-op.
	m = twoSessionModel()
	m.term.(*fakeTerm).caps |= terminal.CapSendText
	m = applyLoad(m)
	m.cursor = 0 // repo row
	next, _ = m.Update(key("i"))
	m = next.(Model)
	if m.passthrough {
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
		if msg, ok := cmd().(sendMsg); !ok || msg.err != nil {
			t.Fatalf("send msg = %#v", cmd())
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
	if m.passthrough || m.passthroughID != "" {
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

func TestPassthroughExitsWhenPaneGone(t *testing.T) {
	m := enteredPassthrough(t)
	delete(m.term.(*fakeTerm).located, 1) // s1's pane can no longer be located
	next, cmd := m.Update(key("x"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected a send command")
	}
	next, _ = m.Update(cmd()) // process sendMsg{ErrNotFound}
	m = next.(Model)
	if m.passthrough {
		t.Fatal("ErrNotFound should drop out of passthrough")
	}
	if m.statusMsg == "" {
		t.Fatal("a vanished pane should report in the status line")
	}
}
```

This test file needs `repo` and `status` imports too (used in the reorder test). Add them:

```go
	"hopper/internal/repo"
	"hopper/internal/status"
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run TestPassthrough`
Expected: FAIL — `m.passthrough undefined`, `sendMsg undefined`, etc.

- [ ] **Step 3: Add Model fields and the `sendMsg` message**

In `internal/tui/model.go`, add the import:

```go
import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"hopper/internal/repo"
	"hopper/internal/source"
	"hopper/internal/status"
	"hopper/internal/terminal"
)
```

Add two fields to the `Model` struct, beside the filter fields:

```go
	passthrough   bool
	passthroughID string // session ID keystrokes are pinned to while in passthrough
```

- [ ] **Step 4: Add the passthrough command, message, and Model methods**

In `internal/tui/passthrough.go`, extend the imports and add the rest of the feature:

```go
import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"hopper/internal/source"
	"hopper/internal/terminal"
)
```

```go
// sendMsg reports the result of a SendText command.
type sendMsg struct{ err error }

// sendCmd locates the pane for pid and sends data to it, off the UI goroutine.
func sendCmd(term terminal.Terminal, pid int, data string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		h, ok := term.Locate(ctx, pid)
		if !ok {
			return sendMsg{err: terminal.ErrNotFound}
		}
		return sendMsg{err: term.SendText(ctx, h, data)}
	}
}

// sessionByID finds a loaded session by ID across all groups.
func (m Model) sessionByID(id string) (source.Session, bool) {
	for _, g := range m.groups {
		for _, it := range g.Items {
			if it.Session.ID == id {
				return it.Session, true
			}
		}
	}
	return source.Session{}, false
}

// enterPassthrough starts relaying keystrokes to the selected session's pane.
// It requires a session row and a terminal that can send; otherwise it is a
// no-op (with a status message when the capability is missing). The preview is
// forced on so the user can see what they are typing into.
func (m Model) enterPassthrough() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.Kind != RowSession {
		return m, nil
	}
	if !m.term.Capabilities().Has(terminal.CapSendText) {
		m.statusMsg = "send unavailable in this terminal"
		return m, nil
	}
	m.passthrough = true
	m.passthroughID = r.Item.Session.ID
	m.showPreview = true
	m.statusMsg = ""
	return m, m.previewIfOpen()
}

// handlePassthroughKey relays one key to the pinned pane. Ctrl-] exits the
// mode; unmapped keys are ignored; if the pinned session has ended, the mode
// exits with a notice. Every other key is translated and sent.
func (m Model) handlePassthroughKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+]" {
		m.passthrough = false
		m.passthroughID = ""
		m.statusMsg = ""
		return m, nil
	}
	data, ok := keyToBytes(msg)
	if !ok {
		return m, nil
	}
	sess, ok := m.sessionByID(m.passthroughID)
	if !ok {
		m.passthrough = false
		m.passthroughID = ""
		m.statusMsg = "passthrough: session ended"
		return m, nil
	}
	return m, sendCmd(m.term, sess.PID, data)
}
```

- [ ] **Step 5: Route keys and handle `sendMsg`**

In `internal/tui/model.go`, add the `sendMsg` case to `Update`'s type switch (next to `previewMsg`):

```go
	case sendMsg:
		if msg.err != nil {
			m.statusMsg = "send: " + msg.err.Error()
			if errors.Is(msg.err, terminal.ErrNotFound) {
				m.passthrough = false
				m.passthroughID = ""
			}
			return m, nil
		}
		m.statusMsg = ""
		return m, m.previewIfOpen() // re-capture so the pane shows the result
```

In `handleKey`, route to passthrough before anything else, and add the `i` binding in the normal-mode switch:

```go
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.passthrough {
		return m.handlePassthroughKey(msg)
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	switch msg.String() {
	// ...existing cases...
	case "i":
		return m.enterPassthrough()
	// ...
	}
	return m, nil
}
```

- [ ] **Step 6: Run the tests and the full suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/model.go internal/tui/passthrough.go internal/tui/passthrough_test.go
git commit -m "feat(tui): passthrough mode relays keys to a session pane"
```

---

### Task 4: Passthrough footer banner and key hints

Surfaces the mode in the UI: a banner replaces the key-hint footer while passthrough is active, and the static hints (footer + README) gain the `i` binding.

**Files:**
- Modify: `internal/tui/view.go` (`renderFooter` switch, `passthroughBanner`, `passthroughStyle`, `footer` const)
- Modify: `README.md` (Keys line)
- Test: `internal/tui/passthrough_test.go`

**Interfaces:**
- Consumes: `Model.passthrough`, `Model.passthroughID`, `Model.sessionByID` (Task 3); existing `short`, `st.footer`.

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/passthrough_test.go`:

```go
func TestPassthroughBannerInFooter(t *testing.T) {
	m := enteredPassthrough(t) // pinned to s1, name "first"
	m.width, m.height = 50, 20  // < splitMinWidth → stacked, footer rendered
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
```

Add `"strings"` to the test file's imports if not already present.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run TestPassthroughBannerInFooter`
Expected: FAIL — the output shows the normal footer, not the banner.

- [ ] **Step 3: Add the banner and route the footer to it**

In `internal/tui/view.go`, add the `i` hint to the footer constant:

```go
const footer = "j/k move · h/l fold · Enter focus · i send · p preview · / filter · r refresh · q quit"
```

Replace the body of `renderFooter` so passthrough takes precedence over the filter prompt and the key hints:

```go
func (m Model) renderFooter() []string {
	var lines []string
	if m.statusMsg != "" {
		lines = append(lines, "", m.statusMsg)
	}
	switch {
	case m.passthrough:
		lines = append(lines, "", m.passthroughBanner())
	case m.filtering:
		lines = append(lines, "", "/"+m.filter)
	default:
		lines = append(lines, "", st.footer.Render(footer))
	}
	return lines
}
```

Add the banner style near `newStyles`/`st` and the method (place the method beside `renderFooter`):

```go
// passthroughStyle marks the passthrough banner tag: keystrokes are being
// relayed to a pane, so it reads as a distinct, attention-colored mode line.
var passthroughStyle = lipgloss.NewStyle().Bold(true).
	Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))

// passthroughBanner is the footer line shown while relaying keys: a tag, the
// pinned session's name, and the exit chord.
func (m Model) passthroughBanner() string {
	name := short(m.passthroughID)
	if sess, ok := m.sessionByID(m.passthroughID); ok && sess.Name != "" {
		name = sess.Name
	}
	return passthroughStyle.Render(" PASSTHROUGH ") + " → " + name +
		"   " + st.footer.Render("Ctrl-] to exit")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/tui/ -run TestPassthroughBannerInFooter`
Expected: PASS.

- [ ] **Step 5: Update the README keys**

In `README.md`, change the Keys block to include the new binding:

```
## Keys

    j/k move · h/l fold · Enter focus · i send · p preview · / filter · r refresh · q quit
```

- [ ] **Step 6: Run the full suite and build**

Run: `go test ./... && go build -o hopper .`
Expected: PASS, binary builds.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/view.go internal/tui/passthrough_test.go README.md
git commit -m "feat(tui): passthrough banner and i key hint"
```

---

## Self-Review

**Spec coverage:**

- Terminal `CapSendText` + `SendText`, kitty via `send-text --stdin`, `none` unsupported → Task 1. ✓
- Key translation table (runes, Enter/Tab/Backspace/Esc, arrows, Ctrl-combos, unmapped dropped), Esc forwarded → Task 2. ✓
- Enter passthrough with `i`, session-row + capability gating, force preview on → Task 3 (`enterPassthrough`). ✓
- Forward keys to the pinned pane, pin by ID across reorder, `Ctrl-]` exits without sending, `ErrNotFound`/session-ended exits, transient error stays (via `sendErr`/status) → Task 3. ✓
- Re-capture preview after each send (`previewIfOpen` on `sendMsg` success) → Task 3. ✓
- Footer banner with session name + exit hint, replaces key hints; key-hint/README updated → Task 4. ✓
- No changes to `source`/`claude`/`session`/`transcript`/`status` → honored across all tasks. ✓

**Placeholder scan:** No TBD/TODO/"handle edge cases" — every code and test step shows full content.

**Type consistency:** `SendText(ctx, h, data string) error`, `CapSendText`, `sendMsg{err error}`, `sendCmd(term, pid, data)`, `sessionByID(id) (source.Session, bool)`, `enterPassthrough`, `handlePassthroughKey`, `passthroughBanner`, fields `passthrough`/`passthroughID` are used identically wherever they appear. `fakeTerm.sent []sentText`/`sendErr` introduced in Task 1 are consumed in Task 3.

**Implementation note to verify during Task 1:** confirm `kitty @ send-text --stdin` sends bytes verbatim against a real kitty; if stdin escaping misbehaves, fall back to `kitty @ send-key`. This does not change any interface or test in this plan.
