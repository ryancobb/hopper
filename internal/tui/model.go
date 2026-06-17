package tui

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

// RepoResolver resolves a working directory to its repo info.
type RepoResolver interface {
	Resolve(ctx context.Context, cwd string) repo.Info
}

// Model is the Bubble Tea model.
type Model struct {
	src   source.Source
	repos RepoResolver
	term  terminal.Terminal

	groups    []Group
	rows      []Row
	cursor    int
	anchor    rowAnchor
	collapsed map[string]bool
	slept     map[string]time.Time // session ID -> acknowledged UpdatedAt (snooze)

	showPreview bool
	preview     string
	previewSID  string // session the preview text was captured from
	previewCol  int    // leftmost visible preview column (horizontal scroll)

	filtering bool
	filter    string

	passthroughID     string          // pinned session ID; "" when not in passthrough
	passthroughName   string          // pinned session's display name, for the banner
	passthroughHandle terminal.Handle // resolved pane handle, nil until located on enter
	passthroughTick   bool            // whether the passthrough preview tick is scheduled

	pendingKill *pendingKill // a kill awaiting y/n confirmation; nil when none

	kill func(pid int) error // sends SIGTERM; injectable for tests

	pendingLaunch *pendingLaunch // a just-launched session to focus once it appears

	statusMsg string
	loadErr   error
	loading   bool
	width     int
	height    int
	quitting  bool

	spinnerFrame int  // advances on spinnerTickMsg to animate working glyphs
	spinning     bool // whether the spinner tick is currently scheduled
}

// hasWorking reports whether any session is working — the only state the
// spinner animates, so the spinner tick runs only while one exists.
func (m Model) hasWorking() bool {
	for _, g := range m.groups {
		for _, it := range g.Items {
			if it.Session.Kind == status.Working {
				return true
			}
		}
	}
	return false
}

// rowAnchor identifies the row the cursor is on so a refresh can restore it.
type rowAnchor struct {
	set  bool
	kind RowKind
	key  string // Group.Key for RowRepo, Session.ID for RowSession
}

// New builds a Model from a session source, repo resolver, and terminal backend.
// Preview starts on when the terminal can capture panes, so the split layout is
// the default experience there; other terminals start on the plain list.
func New(src source.Source, repos RepoResolver, term terminal.Terminal) Model {
	return Model{src: src, repos: repos, term: term,
		collapsed:   map[string]bool{},
		slept:       map[string]time.Time{},
		kill:        defaultKill,
		loading:     true,
		showPreview: term.Capabilities().Has(terminal.CapPreview)}
}

const (
	refreshInterval     = time.Second
	spinnerInterval     = 100 * time.Millisecond
	passthroughInterval = 200 * time.Millisecond
	loadTimeout         = 3 * time.Second
	actionTimeout       = 2 * time.Second

	previewMinLines     = 8
	previewDefaultLines = 12
	previewScrollStep   = 8
)

type tickMsg time.Time
type spinnerTickMsg time.Time
type passthroughTickMsg time.Time
type loadedMsg struct {
	items []Item
	err   error
}
type focusMsg struct{ err error }
type previewMsg struct {
	sid  string // session the capture is for
	text string
	err  error
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// spinnerTickCmd drives the working-glyph animation. It runs faster than the
// data tick and only advances the frame counter, so it never reloads sessions
// or recaptures previews. It runs only while a session is working: a load that
// reveals one starts it, and it lapses once none remain, so a fully idle screen
// does no periodic redraw at all.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg { return spinnerTickMsg(t) })
}

// passthroughTickCmd drives the live preview while relaying keys to a pane. Like
// the spinner tick it runs faster than the data tick and recaptures only the
// pinned pane (never reloading sessions); it runs only while in passthrough and
// lapses on exit, so an idle screen does no extra periodic work.
func passthroughTickCmd() tea.Cmd {
	return tea.Tick(passthroughInterval, func(t time.Time) tea.Msg { return passthroughTickMsg(t) })
}

// loadCmd fetches and enriches sessions off the UI goroutine, bounded by a timeout.
func (m Model) loadCmd() tea.Cmd {
	src, repos := m.src, m.repos
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		sessions, err := src.Sessions(ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		items := make([]Item, 0, len(sessions))
		for _, s := range sessions {
			items = append(items, Item{Session: s, Repo: repos.Resolve(ctx, s.CWD)})
		}
		return loadedMsg{items: items}
	}
}

func focusCmd(term terminal.Terminal, pid int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		h, ok := term.Locate(ctx, pid)
		if !ok {
			return focusMsg{err: terminal.ErrNotFound}
		}
		return focusMsg{err: term.Focus(ctx, h)}
	}
}

func previewCmd(term terminal.Terminal, sid string, pid, lines int) tea.Cmd {
	return func() tea.Msg {
		if !term.Capabilities().Has(terminal.CapPreview) {
			return previewMsg{sid: sid, err: terminal.ErrUnsupported}
		}
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		h, ok := term.Locate(ctx, pid)
		if !ok {
			return previewMsg{sid: sid, err: terminal.ErrNotFound}
		}
		text, err := term.Preview(ctx, h, lines)
		return previewMsg{sid: sid, text: text, err: err}
	}
}

// previewHandleCmd captures a pane by an already-resolved handle, skipping the
// locate step previewCmd does. The passthrough tick uses it with the handle
// cached on enter, so the live preview costs no per-tick locate.
func previewHandleCmd(term terminal.Terminal, sid string, h terminal.Handle, lines int) tea.Cmd {
	return func() tea.Msg {
		if !term.Capabilities().Has(terminal.CapPreview) {
			return previewMsg{sid: sid, err: terminal.ErrUnsupported}
		}
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		text, err := term.Preview(ctx, h, lines)
		return previewMsg{sid: sid, text: text, err: err}
	}
}

// previewSize is the number of pane lines to capture. In the split layout the
// preview fills the main area, so it tracks the body height; stacked, it takes
// roughly a third of the screen. A previewMinLines floor keeps a sliver even on
// a short screen; there is no upper cap, so a taller window shows more of the
// captured pane. A keypress can race the first WindowSizeMsg, so an unknown
// height gets a sane default.
func (m Model) previewSize() int {
	if m.height <= 0 {
		return previewDefaultLines
	}
	n := m.height / 3
	if m.useSplit(m.contentWidth()) {
		// Body height is height - header(2) - footer(2); the pane's two
		// borders take two more, leaving height-6 content rows. A status
		// message or the filter prompt grows the footer by 2, so this can
		// over-capture by up to two lines, which renderPreviewPane discards.
		n = m.height - 6
	}
	return max(n, previewMinLines)
}

// scrollPreview pans the preview horizontally by delta columns, clamped so the
// offset never goes negative or past the widest captured row.
func (m *Model) scrollPreview(delta int) {
	m.previewCol = max(0, min(m.previewCol+delta, m.maxPreviewCol()))
}

func (m Model) previewIfOpen() tea.Cmd {
	if !m.showPreview || m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	if r.Kind != RowSession {
		return nil
	}
	return previewCmd(m.term, r.Item.Session.ID, r.Item.Session.PID, m.previewSize())
}

// passthroughPreviewCmd captures the pinned pane during passthrough, by its
// cached handle, so the live preview tracks the session receiving keystrokes
// rather than whatever the cursor is on. It is a no-op until the handle resolves.
func (m Model) passthroughPreviewCmd() tea.Cmd {
	if m.passthroughID == "" || m.passthroughHandle == nil {
		return nil
	}
	return previewHandleCmd(m.term, m.passthroughID, m.passthroughHandle, m.previewSize())
}

// Init kicks off the first load and the refresh tick. The spinner tick starts
// on demand, once a load reveals a working session.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadCmd(), tickCmd())
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		cmds := []tea.Cmd{tickCmd()}
		if !m.loading {
			m.loading = true
			cmds = append(cmds, m.loadCmd())
		}
		// In passthrough the faster passthrough tick drives the preview.
		if !m.inPassthrough() {
			if c := m.previewIfOpen(); c != nil {
				cmds = append(cmds, c)
			}
		}
		return m, tea.Batch(cmds...)
	case spinnerTickMsg:
		if !m.hasWorking() {
			m.spinning = false // nothing to animate; let the tick lapse
			return m, nil
		}
		m.spinnerFrame++
		return m, spinnerTickCmd()
	case passthroughTickMsg:
		if !m.inPassthrough() {
			m.passthroughTick = false // exited; let the tick lapse
			return m, nil
		}
		return m, tea.Batch(passthroughTickCmd(), m.passthroughPreviewCmd())
	case passthroughLocatedMsg:
		if msg.id != m.passthroughID {
			return m, nil // stale: exited or re-entered on another session
		}
		if !msg.ok {
			return m.exitPassthrough("passthrough: pane not found"), nil
		}
		m.passthroughHandle = msg.handle
		return m, m.passthroughPreviewCmd() // first live capture, now that we can
	case loadedMsg:
		m.loading = false
		m.applyLoaded(msg)
		if !m.spinning && m.hasWorking() {
			m.spinning = true // a working session appeared; (re)start the tick
			return m, spinnerTickCmd()
		}
		return m, nil
	case focusMsg:
		m.setActionStatus("focus", msg.err)
		return m, nil
	case launchMsg:
		if msg.err != nil {
			m.pendingLaunch = nil // the launch failed; nothing will appear to adopt
		}
		m.setActionStatus("new", msg.err)
		return m, nil
	case killMsg:
		m.setActionStatus("kill", msg.err)
		return m, nil
	case previewMsg:
		if !m.showPreview {
			return m, nil // closed while the fetch was in flight
		}
		m.previewSID = msg.sid
		if msg.err != nil {
			m.preview = "(preview unavailable)"
		} else {
			m.preview = msg.text
		}
		m.previewCol = min(m.previewCol, m.maxPreviewCol())
		return m, nil
	case sendMsg:
		// A vanished pane ends passthrough; other errors stay in the mode.
		if errors.Is(msg.err, terminal.ErrNotFound) {
			return m.exitPassthrough("send: " + msg.err.Error()), nil
		}
		m.setActionStatus("send", msg.err)
		if msg.err != nil {
			return m, nil
		}
		// Re-capture right after the send so the keystroke echoes immediately
		// rather than waiting up to one passthrough-tick interval. The tick still
		// covers passive pane updates (e.g. streamed output while you watch).
		return m, m.passthroughPreviewCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) applyLoaded(msg loadedMsg) {
	m.loadErr = msg.err
	if msg.err != nil {
		return
	}
	m.groups = BuildGroups(msg.items)
	// A refresh that drops the kill target dismisses the confirm: pressing y then
	// would signal a PID the OS may have recycled to an unrelated process. This
	// runs before adoption so a refresh that both ends the kill target and reveals
	// the new session can adopt the same tick rather than deferring one.
	if m.pendingKill != nil {
		if _, ok := m.sessionByID(m.pendingKill.id); !ok {
			m.pendingKill = nil
		}
	}
	// Anchor the cursor onto a just-launched session before rebuilding rows, so
	// reanchor lands on it.
	m.adoptLaunchedSession()
	m.rebuildRows()
	m.pruneSlept()
	// A refresh that drops the pinned session ends passthrough: no pane remains
	// to relay to.
	if m.inPassthrough() {
		if _, ok := m.sessionByID(m.passthroughID); !ok {
			*m = m.exitPassthrough("passthrough: session ended")
		}
	}
}

// setActionStatus reports a one-off action's outcome in the status line: an
// error under a prefix (e.g. "kill: ...") or a cleared line on success.
func (m *Model) setActionStatus(prefix string, err error) {
	if err != nil {
		m.statusMsg = prefix + ": " + err.Error()
	} else {
		m.statusMsg = ""
	}
}

func (m *Model) rebuildRows() {
	m.rows = Flatten(m.groups, m.collapsed, m.filter)
	m.reanchor()
}

func (m *Model) reanchor() {
	if m.anchor.set {
		for i, r := range m.rows {
			if r.Kind != m.anchor.kind {
				continue
			}
			if (r.Kind == RowRepo && r.Group.Key == m.anchor.key) ||
				(r.Kind == RowSession && r.Item.Session.ID == m.anchor.key) {
				m.cursor = i
				return
			}
		}
	}
	m.clampCursor()
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// setAnchor records the row under the cursor so a later rebuild can restore it.
func (m *Model) setAnchor() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		m.anchor = rowAnchor{}
		return
	}
	r := m.rows[m.cursor]
	if r.Kind == RowRepo {
		m.anchor = rowAnchor{set: true, kind: RowRepo, key: r.Group.Key}
		return
	}
	m.anchor = rowAnchor{set: true, kind: RowSession, key: r.Item.Session.ID}
}

func (m *Model) moveCursor(d int) {
	m.cursor += d
	m.clampCursor()
	m.setAnchor()
	m.previewCol = 0
}

// jumpCursor moves the cursor to an absolute row, clamping to range, re-anchoring
// for the next rebuild, and resetting the horizontal scroll — the shared tail of
// the g/G jumps so a new cursor target can't forget to zero previewCol.
func (m *Model) jumpCursor(i int) {
	m.cursor = i
	m.clampCursor()
	m.setAnchor()
	m.previewCol = 0
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.inPassthrough() {
		return m.handlePassthroughKey(msg)
	}
	if m.pendingKill != nil {
		return m.handleKillConfirmKey(msg)
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "j", "down":
		m.moveCursor(1)
		return m, m.previewIfOpen()
	case "k", "up":
		m.moveCursor(-1)
		return m, m.previewIfOpen()
	case "g":
		m.jumpCursor(0)
		return m, m.previewIfOpen()
	case "G":
		m.jumpCursor(len(m.rows) - 1)
		return m, m.previewIfOpen()
	case "z":
		m.toggleFold()
		return m, nil
	case "l", "right":
		m.scrollPreview(previewScrollStep)
		return m, nil
	case "h", "left":
		m.scrollPreview(-previewScrollStep)
		return m, nil
	case "enter":
		return m.activate()
	case "i":
		return m.enterPassthrough()
	case "o":
		return m.focusSelected()
	case "n":
		return m.newSession()
	case "x":
		return m.enterKillConfirm()
	case "s":
		return m.sleepSelected()
	case "p":
		return m.togglePreview()
	case "r":
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, m.loadCmd()
	case "/":
		m.filtering = true
		return m, nil
	}
	return m, nil
}

func (m Model) activate() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	if r := m.rows[m.cursor]; r.Kind == RowRepo {
		m.collapsed[r.Group.Key] = !m.collapsed[r.Group.Key]
		m.rebuildRows()
		m.setAnchor()
		return m, nil
	}
	return m.focusSelected()
}

func (m Model) focusSelected() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.Kind != RowSession {
		return m, nil
	}
	if !m.term.Capabilities().Has(terminal.CapFocus) {
		m.statusMsg = "focus unavailable in this terminal"
		return m, nil
	}
	return m, focusCmd(m.term, r.Item.Session.PID)
}

func (m Model) togglePreview() (tea.Model, tea.Cmd) {
	if !m.term.Capabilities().Has(terminal.CapPreview) {
		m.statusMsg = "preview unavailable in this terminal"
		return m, nil
	}
	m.showPreview = !m.showPreview
	m.previewCol = 0
	if !m.showPreview {
		m.preview, m.previewSID = "", ""
		return m, nil
	}
	return m, m.previewIfOpen()
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filtering = false
		return m, nil
	case "esc":
		m.filtering = false
		m.filter = ""
		m.rebuildRows()
		return m, nil
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
			m.rebuildRows()
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.filter += string(msg.Runes)
			m.rebuildRows()
		}
		return m, nil
	}
}

// toggleFold flips the fold of the group under the cursor. On a repo row it
// toggles that group; on a session row it collapses the parent group and moves
// the cursor to its header, so folding from inside a group lands on the header
// that now stands in for it. Enter on a repo row toggles fold too (activate),
// so z is the explicit key and Enter the incidental one.
func (m *Model) toggleFold() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	var key string
	if r.Kind == RowSession {
		key = r.Item.Repo.Root
	} else {
		key = r.Group.Key
	}
	if m.collapsed[key] {
		m.collapsed[key] = false
		m.rebuildRows()
		m.setAnchor()
		return
	}
	m.collapsed[key] = true
	m.rebuildRows()
	for i, rr := range m.rows {
		if rr.Kind == RowRepo && rr.Group.Key == key {
			m.cursor = i
			break
		}
	}
	m.clampCursor()
	m.setAnchor()
}
