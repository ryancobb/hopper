# Preview Clip + Horizontal Scroll Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop re-wrapping the preview (which shatters Claude's column layout); render each captured row clipped to the box width, and let the user pan the preview horizontally with `h`/`l`.

**Architecture:** The preview becomes a viewport onto the source screen. `boxLine` already clips a row to the box's inner width with an ellipsis, so the wrapping path (`reflow`) is deleted and rows pass through unchanged. A `previewCol` offset drops leading columns via `ansi.TruncateLeft` (which re-emits the SGR style across the cut, so no manual style carry is needed). Fold moves off `h`/`l` (now scroll) onto a single `z` toggle.

**Tech Stack:** Go, Bubble Tea (`github.com/charmbracelet/bubbletea`), lipgloss, `github.com/charmbracelet/x/ansi`. Terminal backend is kitty via `kitty @` remote control.

## Global Constraints

- Package is `tui` (`internal/tui`) and `terminal` (`internal/terminal`); all new code is in those packages, same-package (no new exported API).
- Go toolchain is managed by mise; run commands as `go test ./...` / `go build ./...` from the repo root (mise activates `go` in the project dir). If `go` is not on PATH, prefix with the mise shim.
- TDD: write the failing test first, watch it fail, implement, watch it pass, commit.
- `min`/`max` are Go builtins (Go 1.21+); the codebase already uses them — do not import a helper.
- ANSI-aware width is `lipgloss.Width`; ANSI-aware cut is `ansi.Truncate` (right) / `ansi.TruncateLeft` (left). Never measure styled strings with `len`.
- Run the full suite (`go test ./...`) before each commit; baseline is green.

---

## File map

- `internal/tui/view.go` — preview rendering. Remove `reflow`/`carryStyle`/`sgrPattern`/`previewBody`; add `clipRow`/`clipRows`/`previewInnerWidth`/`maxPreviewCol`; update the footer string.
- `internal/tui/model.go` — model state + key handling. Add `previewCol` field and `previewScrollStep` const; replace `expand`/`collapse` with `toggleFold`; add `scrollPreview`; remap keys; reset `previewCol` on navigation/toggle.
- `internal/terminal/kitty.go` — doc-comment touch-up on `Preview` (no behavior change).
- `internal/tui/view_test.go` — drop the 5 wrapping tests; add clip tests.
- `internal/tui/model_test.go` — convert the fold test to `z`, add fold + scroll + passthrough-scroll tests.
- `internal/terminal/kitty_test.go` — doc-comment touch-up at the logical-lines test (assertions unchanged).

---

## Task 1: Clip captured rows instead of wrapping

**Files:**
- Modify: `internal/tui/view.go` (remove `reflow`, `carryStyle`, `sgrPattern`, `previewBody`; point `renderPreviewBox`/`renderPreviewPane` at `previewContent`; drop `regexp` import)
- Modify: `internal/terminal/kitty.go` (`Preview` doc comment)
- Test: `internal/tui/view_test.go` (replace wrapping tests with clip tests)
- Test: `internal/terminal/kitty_test.go` (doc comment only)

**Interfaces:**
- Consumes: `m.previewContent() (label string, content []string)` — already exists, returns the captured rows (or a placeholder). `boxLine(ln string, w int, border lipgloss.Style) string` — already clips to `innerWidth(w)` with `"…"` and resets the pen before the border.
- Produces: `renderPreviewBox(w int) []string` and `renderPreviewPane(w, rows int) []string` unchanged in signature; their content is now exactly the captured rows (one source row → one box row), clipped by `boxLine`.

- [ ] **Step 1: Replace the wrapping tests with clip tests**

In `internal/tui/view_test.go`, delete these five functions entirely:
`TestPreviewReflowsLongLine`, `TestReflowCarriesColorAcrossWraps`,
`TestReflowCarriesColonDelimitedSGR`, `TestReflowLeavesPlainTextUnstyled`,
`TestPreviewBoxStaysWithinBudgetWhenReflowed`.

Add these three in their place:

```go
func TestPreviewClipsLongLine(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // session s1
	m.showPreview = true
	// One line wider than the pane's inner width (40 - boxFrameW = 36).
	long := strings.Repeat("x", 60)
	next, _ := m.Update(previewMsg{sid: "s1", text: long})
	m = next.(Model)

	lines := m.renderPreviewPane(40, 5)
	// It clips to a single content row ending in the ellipsis, not wrapped.
	if !strings.Contains(lines[1], "…") {
		t.Errorf("clipped row should carry the ellipsis:\n%q", lines[1])
	}
	if strings.Contains(lines[2], "x") {
		t.Errorf("long line wrapped onto a second row instead of clipping:\n%q", lines[2])
	}
}

func TestPreviewClipPreservesStyle(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	styled := "\x1b[31m" + strings.Repeat("x", 60) + "\x1b[m"
	next, _ := m.Update(previewMsg{sid: "s1", text: styled})
	m = next.(Model)

	lines := m.renderPreviewPane(40, 3)
	// One styled row, not wrapped onto a second.
	if strings.Contains(lines[2], "x") {
		t.Errorf("styled long line wrapped instead of clipping:\n%q", lines[2])
	}
	// The color is kept, and boxLine resets the pen before the right border so
	// it cannot bleed into the frame.
	if !strings.Contains(lines[1], "\x1b[31m") {
		t.Errorf("clipped row lost its color:\n%q", lines[1])
	}
	if !strings.Contains(lines[1], "\x1b[m") {
		t.Errorf("clipped row not reset before the border:\n%q", lines[1])
	}
}

func TestPreviewClipStaysWithinBudget(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 60 // stacked (width < splitMinWidth), tall terminal
	// More captured rows than the stacked budget; each clips to one row, so the
	// box must still trim to previewSize rather than overflow.
	text := strings.TrimRight(strings.Repeat("line\n", 100), "\n")
	next, _ := m.Update(previewMsg{sid: "s1", text: text})
	m = next.(Model)

	content := len(m.renderPreviewBox(m.width)) - 2 // minus top+bottom borders
	if budget := m.previewSize(); content > budget {
		t.Errorf("clipped box = %d content rows, want <= previewSize %d", content, budget)
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestPreviewClip' -v`
Expected: FAIL — under the current `reflow`, the 60-cell line wraps onto a second row, so `TestPreviewClipsLongLine` and `TestPreviewClipPreservesStyle` report "wrapped onto a second row". (`TestPreviewClipStaysWithinBudget` may pass; that is fine.)

- [ ] **Step 3: Point the renderers at `previewContent` and delete the wrapping code**

In `internal/tui/view.go`:

(a) Drop the now-unused `regexp` import. The import block becomes:

```go
import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"hopper/internal/status"
)
```

(b) In `renderPreviewBox`, replace the first line and refresh the stale comment:

```go
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
	return renderBox(label, content, w, 0, false, m.previewBorder())
}
```

(c) In `renderPreviewPane`, replace the `previewBody` call:

```go
func (m Model) renderPreviewPane(w, rows int) []string {
	rows = max(rows, 0)
	label, content := m.previewContent()
	if len(content) > rows {
		content = content[len(content)-rows:]
	}
	return renderBox(label, content, w, rows, true, m.previewBorder())
}
```

(d) Delete the `previewBody` function entirely:

```go
// previewBody returns the box label and the captured content reflowed to the
// box's inner width — the shared input to both the stacked box and split pane.
func (m Model) previewBody(w int) (label string, content []string) {
	label, content = m.previewContent()
	return label, reflow(content, innerWidth(w))
}
```

(e) Delete `sgrPattern` and its comment:

```go
// sgrPattern matches an SGR escape sequence (ESC [ params m) — the ANSI that
// sets the visible style a wrap must carry onto the next row. Params allow ':'
// so colon-delimited forms (truecolor, styled underlines) are tracked too.
var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;:]*m")
```

(f) Delete the `reflow` function (its doc comment through its closing brace) and the `carryStyle` function (its doc comment through its closing brace). After this, `boxLine` is the next function following `innerWidth`.

Then, in `internal/terminal/kitty.go`, replace the `Preview` doc comment:

```go
// Preview returns the last `lines` non-empty logical lines of the window,
// keeping the pane's ANSI colors. Wrap markers are deliberately omitted: kitty
// then rejoins soft-wrapped screen rows into one logical line. The view clips
// each line to the preview box's width (and pans it horizontally) rather than
// re-wrapping, so a full-screen capture keeps its column layout.
```

And in `internal/terminal/kitty_test.go`, replace the comment at the top of `TestKittyPreviewCapturesLogicalLines` (assertions unchanged):

```go
	// The view clips captured lines to the pane width (it no longer re-wraps),
	// so the capture still hands back logical lines: no --add-wrap-markers,
	// letting kitty rejoin soft-wrapped screen rows into one line.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ ./internal/terminal/ -v -run 'TestPreviewClip|TestKittyPreview'`
Expected: PASS for all clip and kitty preview tests.

Then the full suite:

Run: `go test ./...`
Expected: ok (all packages).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go internal/terminal/kitty.go internal/terminal/kitty_test.go
git commit -m "feat(tui): clip preview rows to the box width instead of wrapping"
```

---

## Task 2: Move fold from h/l to a z toggle

**Files:**
- Modify: `internal/tui/model.go` (handleKey: replace `h`/`l` cases with `z`; replace `expand`/`collapse` with `toggleFold`)
- Modify: `internal/tui/view.go` (footer string)
- Test: `internal/tui/model_test.go` (convert fold test to `z`; add fold-from-session and reopen tests)

**Interfaces:**
- Consumes: `m.collapsed map[string]bool`, `m.rebuildRows()`, `m.clampCursor()`, `m.setAnchor()`, `Row{Kind, Group.Key, Item.Repo.Root}`, `RowRepo`/`RowSession` — all existing.
- Produces: `func (m *Model) toggleFold()` — flips the fold of the group under the cursor; on a session row it collapses the parent and moves the cursor to its header. Key `z` invokes it. `h`/`l` are now unmapped (free for Task 3).

- [ ] **Step 1: Convert and add the fold tests**

In `internal/tui/model_test.go`, change `TestCollapseHidesSessions` to drive `z` and rename it:

```go
func TestFoldHidesSessions(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row
	next, _ := m.Update(key("z"))
	m = next.(Model)
	if len(m.rows) != 1 {
		t.Fatalf("after fold want 1 row, got %d", len(m.rows))
	}
}
```

Add two more fold tests right after it:

```go
func TestFoldFromSessionCollapsesParent(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1 // a session row
	next, _ := m.Update(key("z"))
	m = next.(Model)
	if len(m.rows) != 1 {
		t.Fatalf("z on a session should collapse its group, rows=%d", len(m.rows))
	}
	if m.rows[m.cursor].Kind != RowRepo {
		t.Fatalf("cursor should land on the repo header, got kind %v", m.rows[m.cursor].Kind)
	}
}

func TestFoldToggleReopens(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 0 // repo row
	next, _ := m.Update(key("z")) // collapse
	m = next.(Model)
	next, _ = m.Update(key("z")) // expand
	m = next.(Model)
	if len(m.rows) != 3 {
		t.Fatalf("second z should reopen the group, rows=%d", len(m.rows))
	}
}
```

(Leave `TestEnterOnRepoTogglesCollapse` as is — `Enter` on a repo row still toggles fold.)

- [ ] **Step 2: Run the fold tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestFold' -v`
Expected: FAIL — `z` is unmapped (no-op), so groups never collapse; `TestFoldHidesSessions` and `TestFoldFromSessionCollapsesParent` report the wrong row count.

- [ ] **Step 3: Add the z key, the toggleFold function, and update the footer**

In `internal/tui/model.go` `handleKey`, replace the `l`/`h` fold cases:

```go
	case "l":
		m.expand()
		return m, nil
	case "h":
		m.collapse()
		return m, nil
```

with a single fold toggle:

```go
	case "z":
		m.toggleFold()
		return m, nil
```

Replace the `expand` and `collapse` functions with one `toggleFold` (it reuses both former code paths so behavior is preserved):

```go
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
	key := r.Group.Key
	if r.Kind == RowSession {
		key = r.Item.Repo.Root
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
```

In `internal/tui/view.go`, update the footer string (drop `h/l fold`, add `z fold`):

```go
const footer = "j/k move · z fold · Enter focus · i send · n new · x kill · s sleep · p preview · / filter · r refresh · q quit"
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestFold|TestEnterOnRepoTogglesCollapse|TestFooterListsNewKeys' -v`
Expected: PASS.

Then the full suite:

Run: `go test ./...`
Expected: ok (all packages).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/model.go internal/tui/view.go internal/tui/model_test.go
git commit -m "feat(tui): move repo fold from h/l to a single z toggle"
```

---

## Task 3: Horizontal preview scroll on h/l

**Files:**
- Modify: `internal/tui/model.go` (add `previewCol` field + `previewScrollStep` const; `scrollPreview`; `h`/`l`/`left`/`right` key cases; reset `previewCol` in `moveCursor`, `g`/`G`, `togglePreview`)
- Modify: `internal/tui/view.go` (add `clipRow`/`clipRows`/`previewInnerWidth`/`maxPreviewCol`; map `clipRows` in both renderers; footer)
- Test: `internal/tui/model_test.go` (scroll + reset + passthrough-scroll tests)

**Interfaces:**
- Consumes: `m.previewContent()`, `m.contentWidth()`, `m.useSplit(w)`, `sidebarWidth(w)`, `innerWidth(w)`, `boxFrameW`, `lipgloss.Width`, `ansi.TruncateLeft`, `m.moveCursor`, `togglePreview` — all existing or from Task 1/2.
- Produces:
  - `previewCol int` field on `Model` (leftmost visible column, ≥ 0).
  - `const previewScrollStep = 8`.
  - `func (m *Model) scrollPreview(delta int)` — pans and clamps `previewCol`.
  - `func (m Model) maxPreviewCol() int` — `max(0, widest captured row − previewInnerWidth())`.
  - `func (m Model) previewInnerWidth() int` — content cell width of the preview box in the current layout.
  - `func clipRow(ln string, off int) string` / `func clipRows(rows []string, off int) []string` — drop the first `off` columns (leading `"…"`).

- [ ] **Step 1: Write the failing scroll tests**

Add to `internal/tui/model_test.go`:

```go
func TestPreviewScrollPansRight(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30 // stacked
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)

	if m.previewCol != 0 {
		t.Fatalf("previewCol should start at 0, got %d", m.previewCol)
	}
	next, _ = m.Update(key("l"))
	m = next.(Model)
	if m.previewCol != previewScrollStep {
		t.Fatalf("l should pan right by %d, got %d", previewScrollStep, m.previewCol)
	}
	body := strings.Join(m.renderPreviewBox(m.width), "\n")
	if !strings.Contains(body, "…x") {
		t.Errorf("scrolled rows should start with the cut ellipsis:\n%s", body)
	}
	next, _ = m.Update(key("h"))
	m = next.(Model)
	if m.previewCol != 0 {
		t.Fatalf("h should pan back to 0, got %d", m.previewCol)
	}
}

func TestPreviewScrollClampsLow(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30
	next, _ := m.Update(previewMsg{sid: "s1", text: "short"})
	m = next.(Model)
	next, _ = m.Update(key("h")) // pan left from 0
	m = next.(Model)
	if m.previewCol != 0 {
		t.Fatalf("previewCol should clamp at 0, got %d", m.previewCol)
	}
}

func TestPreviewScrollClampsHigh(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30 // stacked, inner = 50 - boxFrameW = 46
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 50)})
	m = next.(Model)
	for i := 0; i < 10; i++ { // many right-pans
		nx, _ := m.Update(key("l"))
		m = nx.(Model)
	}
	want := m.maxPreviewCol()
	if want == 0 {
		t.Fatal("precondition: content is wider than the box, expected a positive max")
	}
	if m.previewCol != want {
		t.Fatalf("previewCol should clamp at maxPreviewCol %d, got %d", want, m.previewCol)
	}
}

func TestPreviewScrollResetsOnCursorMove(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	next, _ = m.Update(key("l"))
	m = next.(Model)
	if m.previewCol == 0 {
		t.Fatal("precondition: expected a non-zero scroll")
	}
	next, _ = m.Update(key("k")) // move cursor
	m = next.(Model)
	if m.previewCol != 0 {
		t.Fatalf("moving the cursor should reset previewCol, got %d", m.previewCol)
	}
}

func TestPreviewScrollSurvivesRefresh(t *testing.T) {
	m := applyLoad(twoSessionModel())
	m.cursor = 1
	m.showPreview = true
	m.width, m.height = 50, 30
	next, _ := m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	next, _ = m.Update(key("l"))
	m = next.(Model)
	col := m.previewCol
	if col == 0 {
		t.Fatal("precondition: expected a non-zero scroll")
	}
	// A fresh capture of the same session must not snap the view back to 0.
	next, _ = m.Update(previewMsg{sid: "s1", text: strings.Repeat("x", 200)})
	m = next.(Model)
	if m.previewCol != col {
		t.Fatalf("refresh should not reset previewCol: was %d, now %d", col, m.previewCol)
	}
}

func TestPassthroughForwardsScrollKeys(t *testing.T) {
	m := enteredPassthrough(t) // pane handle 11 cached
	next, cmd := m.Update(key("l"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("l in passthrough should forward to the pane, not scroll")
	}
	cmd()
	ft := m.term.(*fakeTerm)
	if len(ft.sent) != 1 || ft.sent[0].data != "l" {
		t.Fatalf("expected 'l' forwarded to the pane, got %#v", ft.sent)
	}
	if m.previewCol != 0 {
		t.Fatalf("passthrough must not consume the key as scroll, previewCol=%d", m.previewCol)
	}
}
```

- [ ] **Step 2: Run the scroll tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestPreviewScroll|TestPassthroughForwardsScrollKeys'`
Expected: FAIL to build — `undefined: previewCol`, `undefined: previewScrollStep`, `m.maxPreviewCol`. (A build failure is the red here.)

- [ ] **Step 3: Add the scroll state and clamp**

In `internal/tui/model.go`, add the field to `Model` right after `previewSID`:

```go
	showPreview bool
	preview     string
	previewSID  string // session the preview text was captured from
	previewCol  int    // leftmost visible preview column (horizontal scroll)
```

Add the step constant to the existing const block that holds `previewMinLines`:

```go
	previewMinLines     = 8
	previewDefaultLines = 12
	previewScrollStep   = 8
)
```

Add the scroll mutator (place it near `previewSize`):

```go
// scrollPreview pans the preview horizontally by delta columns, clamped so the
// offset never goes negative or past the widest captured row.
func (m *Model) scrollPreview(delta int) {
	m.previewCol = max(0, min(m.previewCol+delta, m.maxPreviewCol()))
}
```

In `internal/tui/view.go`, add the geometry helpers (place them near `innerWidth`):

```go
// previewInnerWidth is the cell width available for preview content in the
// current layout, so the scroll clamp and the renderer agree on where rows are
// cut. It mirrors renderSplit's main-column math.
func (m Model) previewInnerWidth() int {
	w := m.contentWidth()
	if m.useSplit(w) {
		mainW := max(w-(sidebarWidth(w)+boxFrameW)-1, 1)
		return innerWidth(mainW)
	}
	return innerWidth(w)
}

// maxPreviewCol is the furthest right the preview can scroll: the widest
// captured row minus the visible inner width, never negative. When all rows fit
// it is 0, so h/l are no-ops.
func (m Model) maxPreviewCol() int {
	_, content := m.previewContent()
	widest := 0
	for _, ln := range content {
		widest = max(widest, lipgloss.Width(ln))
	}
	return max(0, widest-m.previewInnerWidth())
}
```

- [ ] **Step 4: Add the clip-with-offset rendering**

In `internal/tui/view.go`, add the row-slicing helpers (place them near `boxLine`):

```go
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

// clipRows applies clipRow to every row at the current horizontal offset.
func clipRows(rows []string, off int) []string {
	if off <= 0 {
		return rows
	}
	out := make([]string, len(rows))
	for i, ln := range rows {
		out[i] = clipRow(ln, off)
	}
	return out
}
```

Wire `clipRows` into both renderers, just before the `renderBox` call.

In `renderPreviewBox`, after the trim block:

```go
	if limit > 0 && len(content) > limit {
		content = content[len(content)-limit:]
	}
	content = clipRows(content, m.previewCol)
	return renderBox(label, content, w, 0, false, m.previewBorder())
```

In `renderPreviewPane`, after its trim:

```go
	if len(content) > rows {
		content = content[len(content)-rows:]
	}
	content = clipRows(content, m.previewCol)
	return renderBox(label, content, w, rows, true, m.previewBorder())
```

- [ ] **Step 5: Wire the keys, the resets, and the footer**

In `internal/tui/model.go` `handleKey`, add the scroll cases right after the `case "z":` block:

```go
	case "z":
		m.toggleFold()
		return m, nil
	case "l", "right":
		m.scrollPreview(previewScrollStep)
		return m, nil
	case "h", "left":
		m.scrollPreview(-previewScrollStep)
		return m, nil
```

Reset `previewCol` to 0 on navigation and on toggling the preview.

`moveCursor`:

```go
func (m *Model) moveCursor(d int) {
	m.cursor += d
	m.clampCursor()
	m.setAnchor()
	m.previewCol = 0
}
```

The `g` and `G` cases (they set the cursor directly, not via `moveCursor`):

```go
	case "g":
		m.cursor = 0
		m.setAnchor()
		m.previewCol = 0
		return m, m.previewIfOpen()
	case "G":
		m.cursor = len(m.rows) - 1
		m.clampCursor()
		m.setAnchor()
		m.previewCol = 0
		return m, m.previewIfOpen()
```

`togglePreview` (reset whether turning on or off):

```go
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
```

In `internal/tui/view.go`, update the footer to advertise scroll:

```go
const footer = "j/k move · h/l scroll · z fold · Enter focus · i send · n new · x kill · s sleep · p preview · / filter · r refresh · q quit"
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestPreviewScroll|TestPassthroughForwardsScrollKeys' -v`
Expected: PASS for all six.

Then the full suite:

Run: `go test ./...`
Expected: ok (all packages).

- [ ] **Step 7: Commit**

```bash
git add internal/tui/model.go internal/tui/view.go internal/tui/model_test.go
git commit -m "feat(tui): pan the preview horizontally with h/l"
```

---

## Self-Review

**Spec coverage:**
- "Stop reflowing / clip" → Task 1 (removes `reflow`/`carryStyle`/`sgrPattern`/`previewBody`; `boxLine` clips). ✓
- "Capture unchanged; only comment reworded" → Task 1 (`kitty.go`/`kitty_test.go` comments, assertions untouched). ✓
- "previewCol state, h/l (+ arrows), step 8, clamp, reset on nav/toggle not on tick" → Task 3. ✓
- "clipRow via ansi.TruncateLeft, no style-carry code" → Task 3 Step 4. ✓
- "previewInnerWidth shared by clamp and renderer" → Task 3 Step 3 (`previewInnerWidth`) mirrors `renderSplit`/stacked math. ✓
- "Fold to z (repo + session row), Enter still toggles" → Task 2 (`toggleFold`; `activate` unchanged). ✓
- "Footer reads j/k move · h/l scroll · z fold · …" → final value set in Task 3 Step 5 (interim `z fold` in Task 2). ✓
- "Passthrough forwards h/l/←/→" → Task 3 `TestPassthroughForwardsScrollKeys` (handleKey routes to `handlePassthroughKey` first). ✓
- Tests: remove 5 wrapping tests, add clip/styled/budget (Task 1); scroll/reset/passthrough (Task 3); fold→z (Task 2). ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code. ✓

**Type consistency:** `previewCol int`, `previewScrollStep` (untyped const 8), `scrollPreview(delta int)`, `maxPreviewCol() int`, `previewInnerWidth() int`, `clipRow(ln string, off int) string`, `clipRows(rows []string, off int) []string`, `toggleFold()` — names/signatures match across the tasks that define and use them. The renderers consistently switch from `previewBody(w)` to `previewContent()` (Task 1) and then add `clipRows(content, m.previewCol)` (Task 3). ✓
