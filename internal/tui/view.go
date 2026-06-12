package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

// styles holds the structural lipgloss styles for the view. Status colors
// live on displayClass so session rows and repo badges share one mapping.
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

// View renders the model: a fixed top (header), a fixed tail (preview,
// status, footer), and the session list between them. In altscreen mode
// anything past the terminal height is silently clipped, so the list is the
// one region that gives way: it is clamped to the remaining height, scrolled
// to keep the cursor row visible.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	w := m.contentWidth()

	top := []string{m.renderHeader(w), divider(w), ""}
	body, cursorLine := splitRows(m.renderBody(w))
	tail, _ := splitRows(m.renderTail(w), 0)

	if m.height > 0 {
		body = clampToCursor(body, m.height-len(top)-len(tail), cursorLine)
	}

	return strings.Join(slices.Concat(top, body, tail), "\n") + "\n"
}

// splitRows resolves embedded newlines so each element is exactly one
// terminal row — the height budget above counts one row per element, and
// error strings, blocked reasons, and pasted filter text can all carry
// newlines. The cursor element index is remapped to its first row. Width
// needs no such guard: bubbletea truncates overwide lines instead of
// letting them wrap.
func splitRows(lines []string, cursor int) ([]string, int) {
	out := make([]string, 0, len(lines))
	row := 0
	for i, ln := range lines {
		if i == cursor {
			row = len(out)
		}
		out = append(out, strings.Split(ln, "\n")...)
	}
	return out, row
}

func divider(w int) string {
	return st.meta.Render(strings.Repeat("─", w))
}

// renderBody renders the session list and reports which line carries the
// cursor, so View can keep it visible when the list is clamped.
func (m Model) renderBody(w int) (lines []string, cursorLine int) {
	switch {
	case m.loadErr != nil:
		return []string{fmt.Sprintf("error: %v", m.loadErr)}, 0
	case len(m.rows) == 0:
		return []string{st.meta.Render("no live sessions")}, 0
	}
	for i, r := range m.rows {
		if r.Kind == RowRepo && i > 0 {
			lines = append(lines, "") // blank line between repo groups
		}
		if i == m.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, m.renderRow(i, r, w))
		if r.Kind == RowSession && r.Item.Session.Kind == status.Blocked && r.Item.Session.WaitingFor != "" {
			lines = append(lines, m.renderReasonRow(r.Item, w))
		}
	}
	return lines, cursorLine
}

// renderTail renders everything below the session list: the preview panel,
// the status message, and the filter prompt or footer.
func (m Model) renderTail(w int) []string {
	var lines []string
	if m.showPreview {
		lines = append(lines, "")
		lines = append(lines, m.renderPreviewBox(w)...)
	}
	if m.statusMsg != "" {
		lines = append(lines, "", m.statusMsg)
	}
	if m.filtering {
		lines = append(lines, "", "/"+m.filter)
	} else {
		lines = append(lines, "", st.footer.Render(footer))
	}
	return lines
}

// Preview-box geometry. A content row is "│ <content> │" (boxFrameW cells of
// frame); the top border embeds the label as "╭─ <label> ─…─╮"
// (boxLabelAffixW cells around the label). previewReservedRows is everything
// that must share the screen with the box's content rows: the three header
// rows, the box's spacer + top + bottom borders, the blank + footer pair, and
// one list row so the cursor stays visible.
const (
	boxFrameW           = 4
	boxLabelAffixW      = 5
	previewReservedRows = 9
)

// renderPreviewBox frames the captured pane in a rounded box: the capture
// keeps its ANSI colors, and without a frame it reads like live terminal
// output. Pane content renders only when the capture is for the selected
// session — on a repo row, or right after the cursor moves, the previous
// session's pane would otherwise sit under the wrong label until the next
// capture lands.
func (m Model) renderPreviewBox(w int) []string {
	label := "preview"
	var content []string
	if sel := m.selectedItem(); sel != nil {
		label = fmt.Sprintf("preview · %s (%s)", short(sel.Session.ID), sel.Repo.Name)
		if m.preview != "" && m.previewSID == sel.Session.ID {
			content = strings.Split(m.preview, "\n")
		}
	}
	// The preview gives way before the list and footer do: on a short
	// terminal keep only the newest rows, the tail of the pane.
	if keep := m.height - previewReservedRows; m.height > 0 && len(content) > keep {
		content = content[len(content)-max(keep, 0):]
	}
	lines := make([]string, 0, len(content)+2)
	lines = append(lines, previewTop(label, w))
	for _, ln := range content {
		lines = append(lines, previewLine(ln, w))
	}
	return append(lines, previewBottom(w))
}

// previewTop renders the box's top border with the label embedded:
// "╭─ preview · a1b2 (repo) ────╮". Border dim, label plain. The label is
// clipped by cell width (not runes) so wide characters cannot push the
// border past w.
func previewTop(label string, w int) string {
	label = ansi.Truncate(label, max(w-boxLabelAffixW-1, 1), "…")
	fill := max(w-boxLabelAffixW-lipgloss.Width(label), 0)
	return st.meta.Render("╭─ ") + label + st.meta.Render(" "+strings.Repeat("─", fill)+"╮")
}

func previewBottom(w int) string {
	return st.meta.Render("╰" + strings.Repeat("─", max(w-2, 0)) + "╯")
}

// previewLine renders one captured pane line between the box borders.
// Captured lines can carry unterminated ANSI colors, so truncation and
// padding must be ANSI-aware, and the color is reset before the right
// border to keep it from tinting the frame or the rest of the UI.
func previewLine(ln string, w int) string {
	inner := max(w-boxFrameW, 1)
	ln = ansi.Truncate(ln, inner, "…")
	pad := max(inner-lipgloss.Width(ln), 0)
	return st.meta.Render("│") + " " + ln + ansi.ResetStyle +
		strings.Repeat(" ", pad) + " " + st.meta.Render("│")
}

// clampToCursor trims lines to at most budget, sliding the window only as
// far as needed to keep the cursor line in view. It is total: any budget or
// cursor, including out-of-range ones, yields a valid window.
func clampToCursor(lines []string, budget, cursor int) []string {
	budget = max(budget, 1)
	if len(lines) <= budget {
		return lines
	}
	off := min(max(0, cursor-budget+1), len(lines)-budget)
	return lines[off : off+budget]
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
	counts := breakdownCounts(*r.Group)
	breakdown := renderBreakdown(counts)
	worst := classUnknown
	if len(counts) > 0 {
		worst = counts[0].class
	}
	bw := lipgloss.Width(breakdown)
	nameMax := max(w-gutterW-2-colGap-bw, 1) // "▾ " takes 2 cells
	name := st.repoName.Render(truncate(label, nameMax))
	left := gutter(selected, worst) + caret + " " + name
	gap := max(w-lipgloss.Width(left)-bw, 1)
	return left + strings.Repeat(" ", gap) + breakdown
}

func (m Model) renderSessionRow(r Row, selected bool, w int) string {
	it := r.Item
	nameW, _, showWord := sessionLayout(w)
	cls := classify(it.Session.Kind, time.Since(it.Session.UpdatedAt))
	sty := cls.style()

	var b strings.Builder
	b.WriteString(gutter(selected, cls))
	b.WriteString(strings.Repeat(" ", sessionIndent))
	b.WriteString(sty.Render(cls.icon()))
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

type classCount struct {
	class displayClass
	n     int
}

// breakdownCounts tallies the display classes present in g, worst (highest)
// first. Classes with no sessions are omitted.
func breakdownCounts(g Group) []classCount {
	counts := map[displayClass]int{}
	for _, it := range g.Items {
		counts[classify(it.Session.Kind, time.Since(it.Session.UpdatedAt))]++
	}
	out := make([]classCount, 0, len(counts))
	for c, n := range counts {
		out = append(out, classCount{c, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].class > out[j].class })
	return out
}

// renderBreakdown renders a group's status counts, e.g.
// "⚠ 1 blocked · ○ 2 recent", each segment in its class color.
func renderBreakdown(counts []classCount) string {
	parts := make([]string, 0, len(counts))
	for _, cc := range counts {
		seg := fmt.Sprintf("%s %d %s", cc.class.icon(), cc.n, cc.class.label())
		parts = append(parts, cc.class.style().Render(seg))
	}
	return strings.Join(parts, st.meta.Render(" · "))
}

// statusText shows the raw status verbatim for unknown kinds, so new provider
// states aren't hidden behind "unknown".
func statusText(it *Item) string {
	if it.Session.Kind == status.Unknown && it.Session.RawStatus != "" {
		return it.Session.RawStatus
	}
	return it.Session.Kind.Label()
}

// gutter renders the 2-cell selection column: a class-colored bar on the
// selected row, spaces otherwise.
func gutter(selected bool, c displayClass) string {
	if !selected {
		return "  "
	}
	return c.style().Render("▌") + " "
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
