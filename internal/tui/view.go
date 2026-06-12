package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"hopper/internal/status"
)

const footer = "j/k move · h/l fold · Enter focus · p preview · / filter · r refresh · q quit"

const defaultWidth = 80

// Status-rail geometry. A session row is:
//
//	gutter(2) indent(2) glyph(1) ' ' word(8) gap(2) name(flex) gap(2) age(4)
//
// When the name would drop below minNameW, the word column is dropped:
//
//	gutter(2) indent(2) glyph(1) gap(2) name(flex) gap(2) age(4)
const (
	gutterW       = 2
	sessionIndent = 2 // session rows sit two cells deeper than repo headers
	glyphW        = 1
	statusWordW   = 8 // fits "working"/"blocked"; raw statuses truncate
	ageW          = 4
	colGap        = 2
	minNameW      = 12
)

// sessionLayout computes session-row geometry at total width w: the flexible
// name width, the column where the name starts, and whether the status word fits.
func sessionLayout(w int) (nameW, nameStart int, showWord bool) {
	nameStart = gutterW + sessionIndent + glyphW + 1 + statusWordW + colGap
	nameW = w - nameStart - colGap - ageW
	if nameW >= minNameW {
		return nameW, nameStart, true
	}
	nameStart = gutterW + sessionIndent + glyphW + colGap
	nameW = max(w-nameStart-colGap-ageW, 1)
	return nameW, nameStart, false
}

// styles holds the structural lipgloss styles for the view. Status colors stay in
// statusStyle so the status glyph/word and the repo badge share one mapping.
type styles struct {
	header   lipgloss.Style
	count    lipgloss.Style
	repoName lipgloss.Style
	meta     lipgloss.Style
	bold     lipgloss.Style
	footer   lipgloss.Style
}

func newStyles() styles {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	return styles{
		header:   lipgloss.NewStyle().Bold(true),
		count:    dim,
		repoName: lipgloss.NewStyle().Bold(true),
		meta:     dim,
		bold:     lipgloss.NewStyle().Bold(true),
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
			if r.Kind == RowSession && r.Item.Session.Kind == status.Blocked && r.Item.Session.WaitingFor != "" {
				b.WriteString(m.renderReasonRow(r.Item, w))
				b.WriteByte('\n')
			}
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
	if r.Kind == RowRepo {
		return m.renderRepoRow(r, selected, w)
	}
	return m.renderSessionRow(r, selected, w)
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

func (m Model) renderRepoRow(r Row, selected bool, w int) string {
	caret := "▾"
	if m.collapsed[r.Group.Key] {
		caret = "▸"
	}
	label := r.Group.Label
	if label == "" {
		label = "(no repo)"
	}
	breakdown := renderBreakdown(*r.Group)
	bw := lipgloss.Width(breakdown)
	nameMax := max(w-gutterW-2-colGap-bw, 1) // "▾ " takes 2 cells
	name := st.repoName.Render(truncate(label, nameMax))
	left := gutter(selected, r.Group.Kind) + caret + " " + name
	gap := max(w-lipgloss.Width(left)-bw, 1)
	return left + strings.Repeat(" ", gap) + breakdown
}

func (m Model) renderSessionRow(r Row, selected bool, w int) string {
	it := r.Item
	nameW, _, showWord := sessionLayout(w)
	sty := statusStyle(it.Session.Kind)

	var b strings.Builder
	b.WriteString(gutter(selected, it.Session.Kind))
	b.WriteString(strings.Repeat(" ", sessionIndent))
	b.WriteString(sty.Render(icon(it.Session.Kind)))
	if showWord {
		b.WriteByte(' ')
		b.WriteString(sty.Render(fmt.Sprintf("%-*s", statusWordW, truncate(statusText(it), statusWordW))))
	}
	b.WriteString(strings.Repeat(" ", colGap))
	name := fmt.Sprintf("%-*s", nameW, truncate(it.Session.Name, nameW))
	if selected {
		name = st.bold.Render(name)
	}
	b.WriteString(name)
	b.WriteString(strings.Repeat(" ", colGap))
	b.WriteString(st.meta.Render(fmt.Sprintf("%*s", ageW, shortAge(time.Since(it.Session.UpdatedAt)))))
	return b.String()
}

// renderReasonRow is the dimmed continuation line carrying a blocked session's
// reason, indented to the name column. It is display-only: it is not a Row, so
// the cursor never lands on it.
func (m Model) renderReasonRow(it *Item, w int) string {
	_, nameStart, _ := sessionLayout(w)
	return strings.Repeat(" ", nameStart) + st.meta.Render(truncate("↳ "+it.Session.WaitingFor, w-nameStart))
}

type kindCount struct {
	kind status.Kind
	n    int
}

// breakdownCounts tallies the statuses present in g, worst (highest rank)
// first. Statuses with no sessions are omitted.
func breakdownCounts(g Group) []kindCount {
	counts := map[status.Kind]int{}
	for _, it := range g.Items {
		counts[it.Session.Kind]++
	}
	out := make([]kindCount, 0, len(counts))
	for k, n := range counts {
		out = append(out, kindCount{k, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].kind.Rank() > out[j].kind.Rank() })
	return out
}

// renderBreakdown renders a group's status counts, e.g.
// "⚠ 1 blocked · ● 2 working", each segment in its status color.
func renderBreakdown(g Group) string {
	parts := make([]string, 0, 4)
	for _, kc := range breakdownCounts(g) {
		seg := fmt.Sprintf("%s %d %s", icon(kc.kind), kc.n, kc.kind.Label())
		parts = append(parts, statusStyle(kc.kind).Render(seg))
	}
	return strings.Join(parts, st.meta.Render(" · "))
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

// gutter renders the 2-cell selection column: a status-colored bar on the
// selected row, spaces otherwise.
func gutter(selected bool, k status.Kind) string {
	if !selected {
		return "  "
	}
	return statusStyle(k).Render("▌") + " "
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
