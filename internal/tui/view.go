package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"hopper/internal/status"
)

const footer = "j/k move · h/l fold · Enter focus · p preview · / filter · r refresh · q quit"

const defaultWidth = 80

// Status-rail geometry. A session row is icon-only: the glyph's shape and
// color carry the status, so no status word is shown and the name gets the
// freed space. The leftmost cell is the accent column — it holds the status
// stripe (or the selection highlight) — and is a single cell so the repo
// (base) level sits nearly flush with the left edge.
//
//	accent(1) indent(1) glyph(1) gap(1) name(flex) gap(1) age(4)
const (
	gutterW       = 1
	sessionIndent = 1 // session rows sit one cell deeper than repo headers
	glyphW        = 1
	ageW          = 4
	colGap        = 1
)

// Split-layout geometry. Below splitMinWidth the layout stays stacked (the
// narrow fallback). In the split, the session list and the preview each sit in
// their own rounded box. sidebarWidth is the list's inner content width (a
// third of the screen, clamped so it neither starves the names nor crowds out
// the preview); the box frame is added around it in renderSplit.
const (
	splitMinWidth = 60
	sidebarMinW   = 24
	sidebarMaxW   = 40
)

// sidebarWidth is the session-list content width inside the sidebar box: a
// third of the screen, clamped to [sidebarMinW, sidebarMaxW]. The box frame
// (boxFrameW) is added around it in renderSplit.
func sidebarWidth(w int) int {
	return min(max(w/3, sidebarMinW), sidebarMaxW)
}

// sessionLayout computes session-row geometry at total width w: the flexible
// name width and the column where the name starts. Rows are icon-only, so
// there is no status-word column.
func sessionLayout(w int) (nameW, nameStart int) {
	nameStart = gutterW + sessionIndent + glyphW + colGap
	nameW = max(w-nameStart-colGap-ageW, 1)
	return nameW, nameStart
}

// styles holds the structural lipgloss styles for the view. Status colors
// live on displayClass so session rows and repo badges share one mapping.
type styles struct {
	header   lipgloss.Style
	count    lipgloss.Style
	meta     lipgloss.Style
	footer   lipgloss.Style
}

func newStyles() styles {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	return styles{
		header:   lipgloss.NewStyle().Bold(true),
		count:    dim,
		meta:     dim,
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

// useSplit reports whether to render the side-by-side layout: preview on and
// the terminal wide enough. Otherwise the stacked layout is used, which also
// serves as the narrow and no-preview fallback.
func (m Model) useSplit(w int) bool {
	return m.showPreview && w >= splitMinWidth
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

	if m.useSplit(w) {
		return m.renderSplit(w)
	}

	top := []string{m.renderHeader(w), divider(w), ""}
	body, cursorLine := splitRows(m.renderBody(w))
	tail, _ := splitRows(m.renderTail(w), 0)

	if m.height > 0 {
		body = clampToCursor(body, m.height-len(top)-len(tail), cursorLine)
	}

	return strings.Join(slices.Concat(top, body, tail), "\n") + "\n"
}

// renderSplit composes the side-by-side layout: a full-width header on top, the
// footer block on the bottom, and between them the session list and the preview
// each in its own rounded box, separated by a one-cell gap. Both boxes share the
// body height — the list is clamped to keep the cursor visible inside its box,
// and the preview pane is padded to fill the same height.
func (m Model) renderSplit(w int) string {
	listW := sidebarWidth(w) // session-list content width inside the box
	sw := listW + boxFrameW  // sidebar box width, frame included
	mainW := max(w-sw-1, 1)  // remaining width, 1 cell for the gap

	top := []string{m.renderHeader(w), ""}
	// Resolve embedded newlines (e.g. a pasted multi-line filter) so the row
	// budget and the joined output agree, the way the stacked path does its tail.
	foot, _ := splitRows(m.renderFooter(), 0)

	sidebar, cursorLine := splitRows(m.renderBody(listW))
	bodyH := len(sidebar) + 2 // + box top and bottom borders
	if m.height > 0 {
		bodyH = max(m.height-len(top)-len(foot), 1)
	}
	contentH := max(bodyH-2, 0) // rows available inside the box frame
	sidebar = clampToCursor(sidebar, max(contentH, 1), cursorLine)

	// Both boxes render at the same height: renderBox pads the list with blank
	// rows and renderPreviewPane pads the capture, so a short body — "no live
	// sessions", an error, an empty filter — fills its column instead of
	// collapsing it and dragging the gap out from under the header.
	side := renderBox("sessions", sidebar, sw, contentH, true)
	main := m.renderPreviewPane(mainW, contentH)
	gap := make([]string, len(side))
	for i := range gap {
		gap[i] = " "
	}

	cols := lipgloss.JoinHorizontal(lipgloss.Top,
		strings.Join(side, "\n"),
		strings.Join(gap, "\n"),
		strings.Join(main, "\n"))

	// The top spacer and footer lines pad to full width too, so every output
	// row is exactly w cells wide.
	for i, ln := range top {
		top[i] = fitLine(ln, w)
	}
	for i, ln := range foot {
		foot[i] = fitLine(ln, w)
	}

	return strings.Join(slices.Concat(top, strings.Split(cols, "\n"), foot), "\n") + "\n"
}

// fitLine clips and right-pads ln to exactly width cells, measured ANSI-aware
// so styled and wide-rune content stay within the column.
func fitLine(ln string, width int) string {
	ln = ansi.Truncate(ln, width, "…")
	if pad := width - lipgloss.Width(ln); pad > 0 {
		return ln + strings.Repeat(" ", pad)
	}
	return ln
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

// renderTail renders everything below the session list in the stacked layout:
// the preview box (when shown), then the footer block.
func (m Model) renderTail(w int) []string {
	var lines []string
	if m.showPreview {
		lines = append(lines, "")
		lines = append(lines, m.renderPreviewBox(w)...)
	}
	return append(lines, m.renderFooter()...)
}

// renderFooter is the bottom block shared by both layouts: an optional status
// message, then the filter prompt or the key-hint footer.
func (m Model) renderFooter() []string {
	var lines []string
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

// previewContent returns the box label and the pane lines to display. Pane
// lines are present only when the capture belongs to the currently selected
// session; otherwise (repo row, or a capture still in flight) a single dim
// "select a session" placeholder stands in.
func (m Model) previewContent() (label string, content []string) {
	label = "preview"
	if sel := m.selectedItem(); sel != nil {
		label = fmt.Sprintf("preview · %s (%s)", short(sel.Session.ID), sel.Repo.Name)
		if m.preview != "" && m.previewSID == sel.Session.ID {
			content = strings.Split(m.preview, "\n")
		}
	}
	if len(content) == 0 {
		content = []string{st.meta.Render("select a session")}
	}
	return label, content
}

// renderPreviewBox frames the captured pane in a rounded box: the capture
// keeps its ANSI colors, and without a frame it reads like live terminal
// output. Pane content renders only when the capture is for the selected
// session — on a repo row, or right after the cursor moves, the previous
// session's pane would otherwise sit under the wrong label until the next
// capture lands. With no pane content, previewContent supplies a dim
// "select a session" placeholder.
func (m Model) renderPreviewBox(w int) []string {
	label, content := m.previewBody(w)
	// Reflow can fan one captured line into several rows, so bound the box to
	// the smaller of two positive limits, keeping the newest rows: the capture
	// budget (so the list keeps most of the screen) and the short-terminal
	// safety (room for the list and footer). A non-positive safety limit means
	// no room to trim, so a lone placeholder line is never silently dropped.
	limit := m.previewSize()
	if keep := m.height - previewReservedRows; keep > 0 && keep < limit {
		limit = keep
	}
	if limit > 0 && len(content) > limit {
		content = content[len(content)-limit:]
	}
	return renderBox(label, content, w, 0, false)
}

// renderPreviewPane renders the preview box at width w with exactly rows
// content lines, so it fills a fixed height in the split layout: the newest
// pane lines are kept and short content is padded with blank rows.
func (m Model) renderPreviewPane(w, rows int) []string {
	rows = max(rows, 0)
	label, content := m.previewBody(w)
	if len(content) > rows {
		content = content[len(content)-rows:]
	}
	return renderBox(label, content, w, rows, true)
}

// previewBody returns the box label and the captured content reflowed to the
// box's inner width — the shared input to both the stacked box and split pane.
func (m Model) previewBody(w int) (label string, content []string) {
	label, content = m.previewContent()
	return label, reflow(content, innerWidth(w))
}

// renderBox wraps content in a labeled rounded box at width w, the shared frame
// for both the preview pane and the session sidebar. When pad is true the body
// is exactly rows lines — content padded with blank rows (the fixed-height
// split columns); when false it sizes to len(content) (the stacked box).
func renderBox(label string, content []string, w, rows int, pad bool) []string {
	n := len(content)
	if pad {
		n = rows
	}
	lines := make([]string, 0, n+2)
	lines = append(lines, boxTop(label, w))
	for i := 0; i < n; i++ {
		ln := ""
		if i < len(content) {
			ln = content[i]
		}
		lines = append(lines, boxLine(ln, w))
	}
	return append(lines, boxBottom(w))
}

// boxTop renders the box's top border with the label embedded:
// "╭─ preview · a1b2 (repo) ────╮". Border dim, label plain. The label is
// clipped by cell width (not runes) so wide characters cannot push the
// border past w.
func boxTop(label string, w int) string {
	label = ansi.Truncate(label, max(w-boxLabelAffixW-1, 1), "…")
	fill := max(w-boxLabelAffixW-lipgloss.Width(label), 0)
	return st.meta.Render("╭─ ") + label + st.meta.Render(" "+strings.Repeat("─", fill)+"╮")
}

func boxBottom(w int) string {
	return st.meta.Render("╰" + strings.Repeat("─", max(w-2, 0)) + "╯")
}

// innerWidth is the cell width available for content inside the preview box,
// once its frame is subtracted.
func innerWidth(w int) int { return max(w-boxFrameW, 1) }

// sgrPattern matches an SGR escape sequence (ESC [ params m) — the ANSI that
// sets the visible style a wrap must carry onto the next row. Params allow ':'
// so colon-delimited forms (truecolor, styled underlines) are tracked too.
var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// reflow re-wraps each captured logical line to the pane's inner width, so the
// preview fills the box rather than keeping the source window's wrapping: long
// lines wrap onto the next row instead of being truncated, short ones are left
// as is. Hardwrap drops the active color on continuation rows, so the SGR style
// open at each wrap point is re-emitted at the start of the next row. Wrapping
// is cell-aware (wide runes count as two). The cost of filling the width is that
// a fullscreen-app capture, whose rows sit at absolute columns, will not reflow
// cleanly — but hopper's typical scrolling output does.
func reflow(lines []string, width int) []string {
	width = max(width, 1)
	var out []string
	for _, ln := range lines {
		rows := strings.Split(ansi.Hardwrap(ln, width, false), "\n")
		style := ""
		for i, row := range rows {
			carried := style
			style = carryStyle(style, row)
			if i > 0 && carried != "" {
				row = carried + row
			}
			out = append(out, row)
		}
	}
	return out
}

// carryStyle returns the SGR style in effect at the end of row, given the style
// active at its start. A bare reset clears it; any other SGR sequence is
// appended, so replaying the result reproduces the terminal's pen state — an
// embedded reset in a compound sequence still takes effect on replay.
func carryStyle(prev, row string) string {
	style := prev
	for _, seq := range sgrPattern.FindAllString(row, -1) {
		if seq == "\x1b[m" || seq == "\x1b[0m" {
			style = ""
		} else {
			style += seq
		}
	}
	return style
}

// boxLine renders one content line between the box borders. Captured pane
// lines can carry unterminated ANSI colors, so truncation and padding must be
// ANSI-aware, and the color is reset before the right border to keep it from
// tinting the frame or the rest of the UI.
func boxLine(ln string, w int) string {
	inner := innerWidth(w)
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

	base := lipgloss.NewStyle()
	if selected {
		base = base.Background(selectHighlight)
	}
	// A red stripe marks a repo whose group holds a blocked session, visible even
	// when collapsed; it stays (over the highlight) on the cursor row too.
	gutterCell := base.Render(" ")
	if groupHasBlocked(*r.Group) {
		gutterCell = accentBar(base, classBlocked.style().GetForeground())
	}

	nameMax := max(w-gutterW-2, 1) // gutter cell + "▾ " (caret + space)
	label = fmt.Sprintf("%-*s", nameMax, truncate(label, nameMax))
	return gutterCell + base.Render(caret+" ") + base.Bold(true).Render(label)
}

// spinnerFrames is the braille spinner used for the working glyph — the same
// rotating dots Homebrew and npm use. It advances on the spinner tick.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// sessionGlyph is a session row's status glyph: working sessions animate
// through the spinner frames, every other class uses its static icon.
func (m Model) sessionGlyph(c displayClass) string {
	if c == classWorking {
		return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	}
	return c.icon()
}

// accentBar is the 1-cell left status stripe: a half-block in color c, painted
// over base so it keeps the selection-highlight background behind it on the
// cursor row instead of leaving a default-bg hole in the highlighted row.
func accentBar(base lipgloss.Style, c lipgloss.TerminalColor) string {
	return base.Foreground(c).Render("▌")
}

func (m Model) renderSessionRow(r Row, selected bool, w int) string {
	it := r.Item
	nameW, _ := sessionLayout(w)
	cls := classify(it.Session.Kind, time.Since(it.Session.UpdatedAt))

	hasStripe, dim := cls.accent()
	if selected {
		dim = false // the cursor row lights up rather than fading
	}

	// The selection highlight is the only full-row background; on the cursor row
	// it spans every cell. Otherwise status lives in the left accent stripe plus
	// the glyph color, leaving the row body on the normal background.
	base := lipgloss.NewStyle()
	if selected {
		base = base.Background(selectHighlight)
	}

	// Gutter cell: the status stripe, or a blank. The stripe is painted over base,
	// so on the cursor row it stays visible with the selection highlight behind it.
	gutterCell := base.Render(" ")
	if hasStripe {
		gutterCell = accentBar(base, cls.style().GetForeground())
	}

	glyphFg := cls.style().GetForeground()
	nameStyle := base
	if dim {
		glyphFg = dimColor
		nameStyle = base.Foreground(dimColor)
	}
	if selected || cls == classBlocked {
		nameStyle = nameStyle.Bold(true)
	}

	name := fmt.Sprintf("%-*s", nameW, truncate(it.Session.Name, nameW))
	age := fmt.Sprintf("%*s", ageW, shortAge(time.Since(it.Session.UpdatedAt)))

	var b strings.Builder
	b.WriteString(gutterCell)
	b.WriteString(base.Render(strings.Repeat(" ", sessionIndent)))
	b.WriteString(base.Foreground(glyphFg).Render(m.sessionGlyph(cls)))
	b.WriteString(base.Render(strings.Repeat(" ", colGap)))
	b.WriteString(nameStyle.Render(name))
	b.WriteString(base.Render(strings.Repeat(" ", colGap)))
	b.WriteString(base.Foreground(dimColor).Render(age))
	return b.String()
}

// renderReasonRow is the continuation line carrying a blocked session's reason,
// indented to the name column. It is display-only: it is not a Row, so the
// cursor never lands on it.
func (m Model) renderReasonRow(it *Item, w int) string {
	_, nameStart := sessionLayout(w)
	// A red stripe ties the reason line to the blocked row above it; the text
	// stays dim. The stripe occupies the gutter cell, so the reason still aligns
	// under the session-name column.
	indent := strings.Repeat(" ", nameStart-1)
	text := st.meta.Render(truncate("↳ "+it.Session.WaitingFor, w-nameStart))
	return accentBar(lipgloss.NewStyle(), classBlocked.style().GetForeground()) + indent + text
}

// groupHasBlocked reports whether any session in g is blocked, marking the
// repo header with the red accent stripe even when the group is collapsed.
func groupHasBlocked(g Group) bool {
	for _, it := range g.Items {
		if it.Session.Kind == status.Blocked {
			return true
		}
	}
	return false
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
