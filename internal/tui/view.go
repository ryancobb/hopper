package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"hopper/internal/status"
)

const footer = "j/k move · h/l fold · Enter focus · p preview · / filter · r refresh · q quit"

const defaultWidth = 80

const (
	repoNameCol   = 20
	repoBranchCol = 20
	// statusCol is the column where the status badge/word begins on both repo and
	// session rows, so the two line up. It follows from the repo row layout:
	//   "  " + caret + " " + name + " " + branch + "  " + status
	statusCol = 2 + 1 + 1 + repoNameCol + 1 + repoBranchCol + 2
	// sessionNameCol sizes the session name so its status lands at statusCol:
	//   "      " + name + "  " + status
	sessionNameCol = statusCol - (6 + 2)
)

// styles holds the structural lipgloss styles for the view. Status colors stay in
// statusStyle so the status glyph/word and the repo badge share one mapping.
type styles struct {
	header   lipgloss.Style
	count    lipgloss.Style
	repoName lipgloss.Style
	meta     lipgloss.Style
	selected lipgloss.Style
	footer   lipgloss.Style
}

func newStyles() styles {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	return styles{
		header:   lipgloss.NewStyle().Bold(true),
		count:    dim,
		repoName: lipgloss.NewStyle().Bold(true),
		meta:     dim,
		selected: lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15")),
		footer:   dim,
	}
}

var st = newStyles()

func (m Model) contentWidth() int {
	if m.width > 0 {
		return m.width
	}
	return defaultWidth
}

// truncate shortens s to at most max runes, adding an ellipsis when cut.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// worstKindCount counts the sessions in g whose status equals the group's aggregate.
func worstKindCount(g Group) int {
	n := 0
	for _, it := range g.Items {
		if it.Session.Kind == g.Kind {
			n++
		}
	}
	return n
}

// View renders the model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	w := m.contentWidth()
	var b strings.Builder

	b.WriteString(m.renderHeader(w))
	b.WriteByte('\n')
	b.WriteString(st.meta.Render(strings.Repeat("─", w)))
	b.WriteString("\n\n")

	switch {
	case m.loadErr != nil:
		fmt.Fprintf(&b, "error: %v\n", m.loadErr)
	case len(m.rows) == 0:
		b.WriteString(st.meta.Render("no live sessions") + "\n")
	default:
		for i, r := range m.rows {
			if r.Kind == RowRepo && i > 0 {
				b.WriteByte('\n') // blank line between repo groups
			}
			b.WriteString(m.renderRow(i, r, w))
			b.WriteByte('\n')
		}
	}

	if m.showPreview {
		b.WriteString(st.meta.Render(strings.Repeat("─", w)) + "\n")
		sel := m.selectedItem()
		if sel != nil {
			fmt.Fprintf(&b, "preview · %s (%s)\n", short(sel.Session.ID), sel.Repo.Name)
		} else {
			b.WriteString("preview\n")
		}
		if m.preview != "" {
			for _, ln := range strings.Split(m.preview, "\n") {
				b.WriteString("  " + ln + "\n")
			}
		}
	}

	if m.statusMsg != "" {
		b.WriteString("\n" + m.statusMsg + "\n")
	}
	if m.filtering {
		b.WriteString("\n/" + m.filter + "\n")
	} else {
		b.WriteString("\n" + st.footer.Render(footer) + "\n")
	}
	return b.String()
}

func (m Model) countSessions() int {
	n := 0
	for _, g := range m.groups {
		n += len(g.Items)
	}
	return n
}

func (m Model) selectedItem() *Item {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].Kind == RowSession {
		return m.rows[m.cursor].Item
	}
	return nil
}

func (m Model) renderRow(i int, r Row, w int) string {
	selected := i == m.cursor
	var content string
	if r.Kind == RowRepo {
		content = m.renderRepoRow(r, selected)
	} else {
		content = m.renderSessionRow(r, selected)
	}
	if selected {
		return st.selected.Width(w).Render(content)
	}
	return content
}

func (m Model) renderHeader(w int) string {
	left := st.header.Render(m.src.Label())
	right := st.count.Render(fmt.Sprintf("%s · %d sessions · %d repos",
		m.term.Name(), m.countSessions(), len(m.groups)))
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) renderRepoRow(r Row, selected bool) string {
	caret := "▾"
	if m.collapsed[r.Group.Key] {
		caret = "▸"
	}
	label := r.Group.Label
	if label == "" {
		label = "(no repo)"
	}
	branch := ""
	if len(r.Group.Items) > 0 {
		branch = r.Group.Items[0].Repo.Branch
	}
	name := fmt.Sprintf("%-*s", repoNameCol, truncate(label, repoNameCol))
	branchCol := fmt.Sprintf("%-*s", repoBranchCol, truncate(branch, repoBranchCol))
	badge := fmt.Sprintf("%s %d %s",
		icon(r.Group.Kind), worstKindCount(*r.Group), r.Group.Kind.Label())
	// The selected row gets a full-width background highlight; styling its segments
	// would emit ANSI resets that break the background mid-line, so leave it plain.
	if !selected {
		name = st.repoName.Render(name)
		branchCol = st.meta.Render(branchCol)
		badge = statusStyle(r.Group.Kind).Render(badge)
	}
	return fmt.Sprintf("  %s %s %s  %s", caret, name, branchCol, badge)
}

func (m Model) renderSessionRow(r Row, selected bool) string {
	it := r.Item
	name := fmt.Sprintf("%-*s", sessionNameCol, truncate(it.Session.Name, sessionNameCol))
	statusField := fmt.Sprintf("%s %-8s", icon(it.Session.Kind), statusText(it))
	age := shortAge(time.Since(it.Session.UpdatedAt))
	reason := ""
	if it.Session.Kind == status.Blocked && it.Session.WaitingFor != "" {
		reason = " · " + it.Session.WaitingFor
	}
	// Leave the selected row plain so its full-width highlight is not broken by
	// inner ANSI resets (see renderRepoRow).
	if !selected {
		statusField = statusStyle(it.Session.Kind).Render(statusField)
		age = st.meta.Render(age)
		if reason != "" {
			reason = st.meta.Render(reason)
		}
	}
	return fmt.Sprintf("      %s  %s  %s%s", name, statusField, age, reason)
}

func statusStyle(k status.Kind) lipgloss.Style {
	switch k {
	case status.Working:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case status.Blocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	case status.Idle:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	default:
		return lipgloss.NewStyle()
	}
}

// statusText shows the raw status verbatim for unknown kinds, so new provider
// states aren't hidden behind "unknown".
func statusText(it *Item) string {
	if it.Session.Kind == status.Unknown && it.Session.RawStatus != "" {
		return it.Session.RawStatus
	}
	return it.Session.Kind.Label()
}

func icon(k status.Kind) string {
	switch k {
	case status.Working:
		return "●"
	case status.Blocked:
		return "⚠"
	case status.Idle:
		return "○"
	default:
		return "·"
	}
}

func short(id string) string {
	if len(id) >= 4 {
		return id[:4]
	}
	return id
}

func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
