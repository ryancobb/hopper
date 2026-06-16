package tui

import (
	"context"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"hopper/internal/source"
	"hopper/internal/status"
	"hopper/internal/terminal"
)

// killMsg reports the result of a kill command.
type killMsg struct{ err error }

// pendingKill is a kill awaiting y/n confirmation. id/pid/name are snapshotted so
// a refresh or reorder cannot redirect the kill; id also lets a refresh that
// drops the session dismiss the confirm (so y can't signal a recycled PID).
type pendingKill struct {
	id   string
	pid  int
	name string
}

// defaultKill sends SIGTERM so the session can shut down cleanly.
func defaultKill(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// killCmd signals pid off the UI goroutine. SIGTERM is instant, so no context.
func killCmd(kill func(int) error, pid int) tea.Cmd {
	return func() tea.Msg { return killMsg{err: kill(pid)} }
}

// enterKillConfirm opens the kill confirm for the selected session, snapshotting
// its id/PID/name so a later refresh/reorder cannot redirect the kill. No-op on
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
	name := s.Name
	if name == "" {
		name = short(s.ID)
	}
	m.pendingKill = &pendingKill{id: s.ID, pid: s.PID, name: name}
	return m, nil
}

// handleKillConfirmKey resolves a pending kill confirm: y signals the snapshotted
// PID; any other key cancels. Either way the confirm closes.
func (m Model) handleKillConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pk := m.pendingKill
	m.pendingKill = nil
	if pk != nil && msg.String() == "y" {
		return m, killCmd(m.kill, pk.pid)
	}
	return m, nil
}

// launchMsg reports the result of a Launch command.
type launchMsg struct{ err error }

// launchAdoptWindow is how long after launching hopper keeps watching refreshes
// for the new session to appear so it can move the cursor onto it. A new claude
// process writes its session file within a refresh tick or two; the window is
// generous so a slow start still gets adopted, then gives up rather than
// hijacking the cursor much later.
const launchAdoptWindow = 15 * time.Second

// pendingLaunch tracks a just-launched session so the cursor can land on it once
// it appears. The new session is the one in cwd whose ID was not present at
// launch time (before); deadline bounds the wait.
type pendingLaunch struct {
	cwd      string
	before   map[string]bool
	deadline time.Time
}

// sessionIDSet is the set of all currently-loaded session IDs.
func (m Model) sessionIDSet() map[string]bool {
	ids := map[string]bool{}
	for _, g := range m.groups {
		for _, it := range g.Items {
			ids[it.Session.ID] = true
		}
	}
	return ids
}

// adoptLaunchedSession moves the cursor onto a just-launched session once it
// shows up: a session in the launch cwd whose ID is new since the launch. It
// expands that session's repo group so the row is visible and anchors the
// cursor to it, then clears the pending state. Past the deadline it gives up.
//
// It defers while a filter or kill confirm is active: moving the cursor then
// would either anchor a row the filter hides (a surprise jump when the filter
// clears) or shift the highlight away from the row the confirm names. The wait
// resumes on the next refresh and still ends at the deadline.
func (m *Model) adoptLaunchedSession() {
	if m.pendingLaunch == nil {
		return
	}
	if time.Now().After(m.pendingLaunch.deadline) {
		m.pendingLaunch = nil
		return
	}
	if m.filtering || m.filter != "" || m.pendingKill != nil {
		return
	}
	for _, g := range m.groups {
		for _, it := range g.Items {
			s := it.Session
			if s.CWD == m.pendingLaunch.cwd && !m.pendingLaunch.before[s.ID] {
				m.collapsed[it.Repo.Root] = false // ensure the new row is visible
				m.anchor = rowAnchor{set: true, kind: RowSession, key: s.ID}
				m.pendingLaunch = nil
				return
			}
		}
	}
}

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
	// Remember the launch so the cursor can move onto the new session once it
	// appears, distinguishing it from sessions already running in this cwd. When a
	// launch is still pending (rapid double-n), keep the earlier snapshot so a
	// session from the first launch still counts as new and can be adopted.
	before := m.sessionIDSet()
	if m.pendingLaunch != nil && time.Now().Before(m.pendingLaunch.deadline) {
		before = m.pendingLaunch.before
	}
	m.pendingLaunch = &pendingLaunch{
		cwd:      cwd,
		before:   before,
		deadline: time.Now().Add(launchAdoptWindow),
	}
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
