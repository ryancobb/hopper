package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const footer = "j/k move · h/l fold · Enter focus · p preview · / filter · r refresh · q quit"

// View renders the model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Claude Code Sessions   %s · %d sessions · %d repos\n\n",
		m.term.Name(), m.countSessions(), len(m.groups))

	switch {
	case m.loadErr != nil:
		fmt.Fprintf(&b, "error: %v\n", m.loadErr)
	case len(m.rows) == 0:
		b.WriteString("no live sessions\n")
	default:
		for i, r := range m.rows {
			b.WriteString(m.renderRow(i, r))
			b.WriteByte('\n')
		}
	}

	if m.showPreview {
		b.WriteString(strings.Repeat("─", 60) + "\n")
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
		b.WriteString("\n" + footer + "\n")
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

func (m Model) renderRow(i int, r Row) string {
	cursor := "  "
	if i == m.cursor {
		cursor = "> "
	}
	if r.Kind == RowRepo {
		caret := "▾"
		if m.collapsed[r.Group.Key] {
			caret = "▸"
		}
		st := statusStyle(r.Group.Kind)
		return fmt.Sprintf("%s%s %-30s %s %s",
			cursor, caret, r.Group.Label,
			st.Render(icon(r.Group.Kind)), st.Render(r.Group.Kind.Label()))
	}
	it := r.Item
	statusField := statusStyle(it.Kind).Render(fmt.Sprintf("%-8s", statusText(it)))
	line := fmt.Sprintf("%s    %-4s  %-32s %-14s %s %s",
		cursor, short(it.Session.ID), it.Name, it.Repo.Branch,
		statusField, shortAge(time.Since(it.Session.StatusUpdatedAt)))
	if it.Kind == StatusBlocked && it.Session.WaitingFor != "" {
		line += " (" + it.Session.WaitingFor + ")"
	}
	return line
}

func statusStyle(k StatusKind) lipgloss.Style {
	switch k {
	case StatusWorking:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case StatusBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	case StatusIdle:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	default:
		return lipgloss.NewStyle()
	}
}

// statusText shows the raw status verbatim for unknown kinds, so new Claude Code
// states aren't hidden behind "unknown".
func statusText(it *Item) string {
	if it.Kind == StatusUnknown && it.Session.Status != "" {
		return it.Session.Status
	}
	return it.Kind.Label()
}

func icon(k StatusKind) string {
	switch k {
	case StatusWorking:
		return "●"
	case StatusBlocked:
		return "⚠"
	case StatusIdle:
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
