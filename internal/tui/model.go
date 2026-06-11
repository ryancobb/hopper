package tui

import (
	"time"

	"cctop/internal/repo"
	"cctop/internal/session"
	"cctop/internal/terminal"
	tea "github.com/charmbracelet/bubbletea"
)

// Loader, RepoResolver, Namer are the model's dependencies (interfaces for testing).
type Loader interface {
	Load() ([]session.Session, error)
}
type RepoResolver interface {
	Resolve(cwd string) repo.Info
}
type Namer interface {
	Name(sessionID string) string
}

// Model is the Bubble Tea model.
type Model struct {
	loader Loader
	repos  RepoResolver
	names  Namer
	term   terminal.Terminal

	groups    []Group
	rows      []Row
	cursor    int
	selID     string
	collapsed map[string]bool

	showPreview bool
	preview     string

	filtering bool
	filter    string

	statusMsg string
	loadErr   error
	width     int
	height    int
	quitting  bool
}

// New builds a Model from its dependencies.
func New(loader Loader, repos RepoResolver, names Namer, term terminal.Terminal) Model {
	return Model{loader: loader, repos: repos, names: names, term: term,
		collapsed: map[string]bool{}}
}

const refreshInterval = time.Second

type tickMsg time.Time
type loadedMsg struct {
	items []Item
	err   error
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// loadCmd loads sessions and enriches them off the UI goroutine.
func (m Model) loadCmd() tea.Cmd {
	loader, repos, names := m.loader, m.repos, m.names
	return func() tea.Msg {
		sessions, err := loader.Load()
		if err != nil {
			return loadedMsg{err: err}
		}
		items := make([]Item, 0, len(sessions))
		for _, s := range sessions {
			items = append(items, Item{
				Session: s,
				Repo:    repos.Resolve(s.CWD),
				Name:    names.Name(s.ID),
				Kind:    KindOf(s.Status),
			})
		}
		return loadedMsg{items: items}
	}
}

// Init kicks off the first load and the refresh tick.
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
		return m, tea.Batch(m.loadCmd(), tickCmd())
	case loadedMsg:
		m.applyLoaded(msg)
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
	if m.selID != "" {
		for i, r := range m.rows {
			if r.Kind == RowSession && r.Item.Session.ID == m.selID {
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

func (m *Model) syncSel() {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].Kind == RowSession {
		m.selID = m.rows[m.cursor].Item.Session.ID
	}
}

func (m *Model) moveCursor(d int) {
	m.cursor += d
	m.clampCursor()
	m.syncSel()
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "j", "down":
		m.moveCursor(1)
		return m, nil
	case "k", "up":
		m.moveCursor(-1)
		return m, nil
	case "g":
		m.cursor = 0
		m.syncSel()
		return m, nil
	case "G":
		m.cursor = len(m.rows) - 1
		m.clampCursor()
		m.syncSel()
		return m, nil
	case "l":
		m.expand()
		return m, nil
	case "h":
		m.collapse()
		return m, nil
	case "r":
		return m, m.loadCmd()
	}
	return m, nil
}

func (m *Model) expand() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	if r := m.rows[m.cursor]; r.Kind == RowRepo {
		m.collapsed[r.Group.Key] = false
		m.rebuildRows()
	}
}

// View renders the model. A full implementation is provided in Task 10 (view.go).
func (m Model) View() string { return "" }

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
	m.selID = ""
	m.rebuildRows()
	for i, rr := range m.rows {
		if rr.Kind == RowRepo && rr.Group.Key == key {
			m.cursor = i
			break
		}
	}
	m.clampCursor()
}
