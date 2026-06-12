package tui

import (
	"context"
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

	showPreview bool
	preview     string
	previewSID  string // session the preview text was captured from

	filtering bool
	filter    string

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
		loading:     true,
		showPreview: term.Capabilities().Has(terminal.CapPreview)}
}

const (
	refreshInterval = time.Second
	spinnerInterval = 100 * time.Millisecond
	loadTimeout     = 3 * time.Second
	actionTimeout   = 2 * time.Second

	previewMinLines     = 8
	previewMaxLines     = 24
	previewDefaultLines = 12
)

type tickMsg time.Time
type spinnerTickMsg time.Time
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

// previewSize is the number of pane lines to capture. In the split layout the
// preview fills the main area, so it tracks the body height; stacked, it takes
// roughly a third of the screen. Both keep the session list most of the room
// via the previewMin/Max clamps. A keypress can race the first WindowSizeMsg,
// so an unknown height gets a sane default.
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
	return min(max(n, previewMinLines), previewMaxLines)
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
		if c := m.previewIfOpen(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	case spinnerTickMsg:
		if !m.hasWorking() {
			m.spinning = false // nothing to animate; let the tick lapse
			return m, nil
		}
		m.spinnerFrame++
		return m, spinnerTickCmd()
	case loadedMsg:
		m.loading = false
		m.applyLoaded(msg)
		if !m.spinning && m.hasWorking() {
			m.spinning = true // a working session appeared; (re)start the tick
			return m, spinnerTickCmd()
		}
		return m, nil
	case focusMsg:
		if msg.err != nil {
			m.statusMsg = "focus: " + msg.err.Error()
		} else {
			m.statusMsg = ""
		}
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
		return m, nil
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
	m.rebuildRows()
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
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.cursor = 0
		m.setAnchor()
		return m, m.previewIfOpen()
	case "G":
		m.cursor = len(m.rows) - 1
		m.clampCursor()
		m.setAnchor()
		return m, m.previewIfOpen()
	case "l":
		m.expand()
		return m, nil
	case "h":
		m.collapse()
		return m, nil
	case "enter":
		return m.activate()
	case "o":
		return m.focusSelected()
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

func (m *Model) expand() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	if r := m.rows[m.cursor]; r.Kind == RowRepo {
		m.collapsed[r.Group.Key] = false
		m.rebuildRows()
		m.setAnchor()
	}
}

func (m *Model) collapse() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	key := r.Group.Key
	if r.Kind == RowSession {
		key = r.Item.Repo.Root
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
