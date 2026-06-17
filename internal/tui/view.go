package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"hopper/internal/status"
)

const footer = "j/k move · h/l scroll · z fold · Enter focus · i send · n new · x kill · s sleep · p preview · / filter · r refresh · q quit"

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
	header lipgloss.Style
	count  lipgloss.Style
	meta   lipgloss.Style
	footer lipgloss.Style
}

func newStyles() styles {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	return styles{
		header: lipgloss.NewStyle().Bold(true),
		count:  dim,
		meta:   dim,
		footer: dim,
	}
}

var st = newStyles()

// passthroughStyle marks the passthrough banner tag: keystrokes are being
// relayed to a pane, so it reads as a distinct, attention-colored mode line.
var passthroughStyle = lipgloss.NewStyle().Bold(true).
	Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))

// passthroughBorder colors the preview box frame while relaying keys, matching
// the passthrough footer banner's accent so the whole pane reads as the live
// send target rather than a passive capture.
var passthroughBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

// killStyle marks the kill-confirm banner tag in red: the action is destructive
// and waiting on the user, so it reads as a distinct, alarming mode line rather
// than a quiet status message.
var killStyle = lipgloss.NewStyle().Bold(true).
	Foreground(lipgloss.Color("0")).Background(lipgloss.Color("1"))

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
	mainW := splitMainWidth(w)

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
	side := renderBox("sessions", sidebar, sw, contentH, true, st.meta)
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
// error strings and pasted filter text can both carry newlines. The cursor
// element index is remapped to its first row. Width
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
	switch {
	case m.inPassthrough():
		lines = append(lines, "", m.passthroughBanner())
	case m.pendingKill != nil:
		lines = append(lines, "", m.killConfirmBanner())
	case m.filtering:
		lines = append(lines, "", "/"+m.filter)
	default:
		lines = append(lines, "", st.footer.Render(footer))
	}
	return lines
}

// bannerLine renders a footer mode banner: a colored tag, the subject it acts
// on, and a dim hint. Both the passthrough and kill-confirm banners use it so a
// distinct mode line reads consistently.
func bannerLine(tag lipgloss.Style, tagText, subject, hint string) string {
	return tag.Render(tagText) + " " + subject + "   " + st.footer.Render(hint)
}

// passthroughBanner is the footer line shown while relaying keys: a tag, the
// pinned session's name (cached on enter), and the exit chord. Until the pane
// handle resolves, keystrokes are not yet relayed, so the name carries a
// "connecting" hint rather than dropping keys silently.
func (m Model) passthroughBanner() string {
	name := m.passthroughName
	if m.passthroughHandle == nil {
		name += " (connecting…)"
	}
	return bannerLine(passthroughStyle, " PASSTHROUGH ", "→ "+name, "Ctrl-] to exit")
}

// killConfirmBanner is the footer line shown while a kill is awaiting
// confirmation: a red KILL tag, the target session's name, and what the keys do.
// It mirrors the passthrough banner so a destructive prompt is unmistakable.
func (m Model) killConfirmBanner() string {
	return bannerLine(killStyle, " KILL ", m.pendingKill.name, "y to confirm · any other key cancels")
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
// lines are present only when the capture belongs to the session being shown;
// otherwise (repo row, or a capture still in flight) a single dim "select a
// session" placeholder stands in. During passthrough the preview is locked to
// the pinned session, independent of the cursor.
func (m Model) previewContent() (label string, content []string) {
	label = "preview"
	sid := ""
	switch {
	case m.inPassthrough():
		sid = m.passthroughID
		label = "preview · " + m.passthroughName
	default:
		if sel := m.selectedItem(); sel != nil {
			sid = sel.Session.ID
			label = fmt.Sprintf("preview · %s (%s)", short(sel.Session.ID), sel.Repo.Name)
		}
	}
	if sid != "" && m.preview != "" && m.previewSID == sid {
		content = strings.Split(m.preview, "\n")
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
	label, content := m.previewContent()
	// Each captured line clips to one row, so bound the box to the smaller of
	// two positive limits, keeping the newest rows: the capture budget (so the
	// list keeps most of the screen) and the short-terminal safety (room for the
	// list and footer). A non-positive safety limit means no room to trim, so a
	// lone placeholder line is never silently dropped.
	limit := m.previewSize()
	if keep := m.height - previewReservedRows; keep > 0 && keep < limit {
		limit = keep
	}
	if limit > 0 && len(content) > limit {
		content = content[len(content)-limit:]
	}
	content = clipRows(content, m.previewCol, innerWidth(w))
	return renderBox(label, content, w, 0, false, m.previewBorder())
}

// previewBorder is the preview box's frame color. While relaying keys it turns
// the passthrough accent, matching the footer banner so the whole pane reads as
// the live send target; otherwise it is the dim meta frame the sidebar shares.
func (m Model) previewBorder() lipgloss.Style {
	if m.inPassthrough() {
		return passthroughBorder
	}
	return st.meta
}

// renderPreviewPane renders the preview box at width w with exactly rows
// content lines, so it fills a fixed height in the split layout: the newest
// pane lines are kept and short content is padded with blank rows.
func (m Model) renderPreviewPane(w, rows int) []string {
	rows = max(rows, 0)
	label, content := m.previewContent()
	if len(content) > rows {
		content = content[len(content)-rows:]
	}
	content = clipRows(content, m.previewCol, innerWidth(w))
	return renderBox(label, content, w, rows, true, m.previewBorder())
}

// renderBox wraps content in a labeled rounded box at width w, the shared frame
// for both the preview pane and the session sidebar. When pad is true the body
// is exactly rows lines — content padded with blank rows (the fixed-height
// split columns); when false it sizes to len(content) (the stacked box). border
// styles the frame, so the preview can switch to the passthrough accent.
func renderBox(label string, content []string, w, rows int, pad bool, border lipgloss.Style) []string {
	n := len(content)
	if pad {
		n = rows
	}
	lines := make([]string, 0, n+2)
	lines = append(lines, boxTop(label, w, border))
	for i := 0; i < n; i++ {
		ln := ""
		if i < len(content) {
			ln = content[i]
		}
		lines = append(lines, boxLine(ln, w, border))
	}
	return append(lines, boxBottom(w, border))
}

// boxTop renders the box's top border with the label embedded:
// "╭─ preview · a1b2 (repo) ────╮". Frame in the border style, label plain. The
// label is clipped by cell width (not runes) so wide characters cannot push the
// border past w.
func boxTop(label string, w int, border lipgloss.Style) string {
	label = ansi.Truncate(label, max(w-boxLabelAffixW-1, 1), "…")
	fill := max(w-boxLabelAffixW-lipgloss.Width(label), 0)
	return border.Render("╭─ ") + label + border.Render(" "+strings.Repeat("─", fill)+"╮")
}

func boxBottom(w int, border lipgloss.Style) string {
	return border.Render("╰" + strings.Repeat("─", max(w-2, 0)) + "╯")
}

// innerWidth is the cell width available for content inside the preview box,
// once its frame is subtracted.
func innerWidth(w int) int { return max(w-boxFrameW, 1) }

// previewInnerWidth is the cell width available for preview content in the
// current layout, so the scroll clamp and the renderer agree on where rows are
// cut. It mirrors renderSplit's main-column math.
func (m Model) previewInnerWidth() int {
	w := m.contentWidth()
	if m.useSplit(w) {
		return innerWidth(splitMainWidth(w))
	}
	return innerWidth(w)
}

// splitMainWidth is the preview column's box width in the side-by-side layout:
// the terminal minus the sidebar box and the one-cell gap. renderSplit and
// previewInnerWidth share it so the clamp and the renderer use one formula.
func splitMainWidth(w int) int {
	return max(w-(sidebarWidth(w)+boxFrameW)-1, 1)
}

// maxPreviewCol is the furthest right the preview can scroll, used to bound the
// stored scroll offset. The renderer re-clamps against the rows it actually
// shows, so this is only the keypress ceiling.
func (m Model) maxPreviewCol() int {
	_, content := m.previewContent()
	return maxOffset(content, m.previewInnerWidth())
}

// maxOffset is the furthest these rows can scroll right: the widest row minus
// the visible width, plus one cell for the leading "…" marker so the rightmost
// column stays reachable at full scroll. Rows that already fit yield 0.
func maxOffset(rows []string, inner int) int {
	widest := 0
	for _, ln := range rows {
		widest = max(widest, lipgloss.Width(ln))
	}
	if widest <= inner {
		return 0
	}
	return widest - inner + 1
}

// boxLine renders one content line between the box borders. Captured pane
// lines can carry unterminated ANSI colors, so truncation and padding must be
// ANSI-aware, and the color is reset before the right border to keep it from
// tinting the frame or the rest of the UI.
func boxLine(ln string, w int, border lipgloss.Style) string {
	inner := innerWidth(w)
	ln = ansi.Truncate(ln, inner, "…")
	pad := max(inner-lipgloss.Width(ln), 0)
	return border.Render("│") + " " + ln + ansi.ResetStyle +
		strings.Repeat(" ", pad) + " " + border.Render("│")
}

// clipRow drops the first off columns of a captured row so the preview can pan
// right, marking the cut with a leading "…". boxLine clips the right edge, so
// together they bound the row to the visible window. ansi.TruncateLeft re-emits
// the SGR style open at the cut, so color survives the slice. off <= 0 is a
// no-op (the unscrolled, plain-clip case).
func clipRow(ln string, off int) string {
	if off <= 0 {
		return ln
	}
	return ansi.TruncateLeft(ln, off, "…")
}

// clipRows clips every row to the horizontal window, with off clamped to these
// rows' own maxOffset (inner = the box's content width) so the offset can never
// exceed the visible content — which would otherwise blank every row, e.g. when
// a stale scroll position outlives the wide capture that justified it.
func clipRows(rows []string, off, inner int) []string {
	off = min(off, maxOffset(rows, inner))
	if off <= 0 {
		return rows
	}
	out := make([]string, len(rows))
	for i, ln := range rows {
		out[i] = clipRow(ln, off)
	}
	return out
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
	cls := m.displayClassFor(it.Session)

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
