# Status Visual Hierarchy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Revision (2026-06-12, after implementation):** Tasks 1–3 below build a
> full-row background *tint* for urgent rows. On review that read as too heavy,
> so a follow-up commit (`refactor(tui): left accent stripe instead of full-row
> status tint`) replaced the tint with a 1-cell left accent stripe (`▌` in the
> status color), reserving the full-row background for the Vim selection. The
> `rowTint()` method became `accent() (stripe, dim bool)`; `blockedTint`/
> `workingTint` were dropped. The task steps below are kept as the historical
> build record — see the updated design doc and the as-built code for the final
> stripe design.

**Goal:** Give the session list a clear attention hierarchy — urgent statuses get a full-row background tint, quiet statuses fully fade, and selection reads like Vim visual mode instead of a thin gutter bar.

**Architecture:** Each row is painted through a single lipgloss base style that carries the row background, so every segment (gaps and name padding included) fills continuously. `displayClass` gains a `rowTint()` method describing each class's background tint and whether its whole row dims. `view.go` rebuilds `renderSessionRow`/`renderRepoRow`/`renderReasonRow` around that base style, drops the `▌` gutter bar, and routes selection through a neutral highlight background that overrides the status tint on the cursor row.

**Tech Stack:** Go, bubbletea/lipgloss, `github.com/muesli/termenv` (test color profile), `github.com/charmbracelet/x/ansi`.

---

## Background the implementer needs

- Package under change: `internal/tui` (module path `hopper`). Run tests with
  `go test ./internal/tui/ -run <Name> -v` from the repo root.
- **Color profile in tests:** `go test` runs with no TTY, so lipgloss defaults to
  the `Ascii` profile and `Style.Render` emits **plain text** (no escapes, bold
  stripped). Tests that assert on color/background **must** force a profile:

  ```go
  old := lipgloss.ColorProfile()
  defer lipgloss.SetColorProfile(old)
  lipgloss.SetColorProfile(termenv.ANSI256)
  ```

  Under `ANSI256`, a 256-color **background** renders as `\x1b[48;5;Nm`. So a
  background tint of `lipgloss.Color("52")` appears in the output as the
  substring `48;5;52`, and "the row has a background" can be checked with
  `strings.Contains(line, "48;5;")`. Basic ANSI **foregrounds** (`"1"`,`"2"`,
  `"3"`,`"8"`) render as `31`/`32`/`33`/`90` and never contain `48;5;`.
- **Geometry is preserved:** the new row keeps the existing column layout
  (`gutter 1 + indent 1 + glyph 1 + gap 1 + name + gap 1 + age 4 = width`), so
  the plain-text geometry tests (`TestSessionRowGeometry`, etc.) keep passing
  unchanged because under `Ascii` the styled segments are plain.
- Test helpers already exist in `internal/tui/model_test.go`: `twoSessionModel()`
  (s1 "first" Working, s2 "second" Idle@now → recent-idle), `applyLoad(m)`,
  `New(src, repos, term)`, `fakeSource`, `fakeRepos`, `fakeTerm`. Reuse them.

## File structure

- **Modify** `internal/tui/class.go`: add the row-background color palette
  (`blockedTint`, `workingTint`, `selectHighlight`, `dimColor`) and the
  `displayClass.rowTint()` method. Keep `icon()`/`style()`/`classify()`.
- **Modify** `internal/tui/view.go`: rewrite `renderSessionRow`,
  `renderRepoRow`, `renderReasonRow`; delete the `gutter()` function. No new
  imports (all of `fmt`, `strings`, `time`, `lipgloss` already present).
- **Modify** `internal/tui/class_test.go`: add `TestRowTint`.
- **Modify** `internal/tui/view_test.go`: add tint/dim/selection tests; replace
  `TestSelectedRowBar`. No new imports.

---

## Task 1: Row-background palette and `rowTint`

**Files:**
- Modify: `internal/tui/class.go`
- Test: `internal/tui/class_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/class_test.go` (after `TestClassRankOrder`):

```go
func TestRowTint(t *testing.T) {
	cases := []struct {
		c       displayClass
		wantBg  lipgloss.Color
		wantDim bool
	}{
		{classBlocked, blockedTint, false},
		{classWorking, workingTint, false},
		{classRecentIdle, "", false},
		{classIdle, "", true},
		{classUnknown, "", true},
	}
	for _, tc := range cases {
		bg, dim := tc.c.rowTint()
		if bg != tc.wantBg || dim != tc.wantDim {
			t.Errorf("%v.rowTint() = (%q,%v), want (%q,%v)", tc.c, bg, dim, tc.wantBg, tc.wantDim)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestRowTint`
Expected: build failure — `undefined: blockedTint`, `undefined: workingTint`, `c.rowTint undefined`.

- [ ] **Step 3: Add the palette and method to `class.go`**

Add these constants directly below the `recentIdleWindow` const block (the
package already imports `lipgloss`):

```go
// Row-background palette. A class tint fills an urgent session's whole row;
// selectHighlight overrides it on the cursor row (Vim visual-mode selection);
// dimColor greys quiet rows and secondary text. 256-color indices, tunable.
const (
	blockedTint     lipgloss.Color = "52"  // dark red row tint
	workingTint     lipgloss.Color = "22"  // dark green row tint
	selectHighlight lipgloss.Color = "238" // neutral grey selection highlight
	dimColor        lipgloss.Color = "8"   // dim grey (matches the existing idle/meta grey)
)
```

Add this method below `style()`:

```go
// rowTint is the whole-row treatment for this class: the background tint
// (zero value = no tint) and whether the entire row is dimmed. Blocked and
// working tint; idle and unknown dim; recent-idle does neither (its yellow
// glyph carries the signal). The selected row overrides the tint with
// selectHighlight and is never dimmed.
func (c displayClass) rowTint() (bg lipgloss.Color, dim bool) {
	switch c {
	case classBlocked:
		return blockedTint, false
	case classWorking:
		return workingTint, false
	case classIdle, classUnknown:
		return "", true
	default: // classRecentIdle (and any future non-urgent, non-quiet class)
		return "", false
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestRowTint`
Expected: PASS.

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/tui/`
Expected: PASS (nothing else changed yet; `selectHighlight`/`dimColor` are
defined-but-unused package constants, which Go allows).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/class.go internal/tui/class_test.go
git commit -m "feat(tui): row-background palette and rowTint per class"
```

---

## Task 2: Full-row tint, dimmed quiet rows, Vim-visual session selection

Rewrites `renderSessionRow`. `renderRepoRow` keeps calling `gutter()` for now
(removed in Task 3), so the package still compiles. This task also replaces the
old `TestSelectedRowBar` (the `▌` bar is gone from session rows).

**Files:**
- Modify: `internal/tui/view.go:524-543` (`renderSessionRow`)
- Test: `internal/tui/view_test.go`

- [ ] **Step 1: Replace `TestSelectedRowBar` with the new selection test**

In `internal/tui/view_test.go`, delete the entire `TestSelectedRowBar` function
(currently lines 140-178) and replace it with:

```go
func TestSelectionHighlight(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := applyLoad(twoSessionModel())
	var sessionRow Row
	for _, r := range m.rows {
		if r.Kind == RowSession {
			sessionRow = r
			break
		}
	}

	sel := m.renderSessionRow(sessionRow, true, 60)
	if strings.Contains(sel, "▌") {
		t.Errorf("selection should no longer use the ▌ gutter bar: %q", sel)
	}
	if !strings.Contains(sel, "48;5;238") {
		t.Errorf("selected row missing the highlight background: %q", sel)
	}
	unsel := m.renderSessionRow(sessionRow, false, 60)
	if strings.Contains(unsel, "▌") {
		t.Errorf("unselected row has a ▌ bar: %q", unsel)
	}
}
```

- [ ] **Step 2: Add the tint and dim tests**

Append to `internal/tui/view_test.go`:

```go
func TestSessionRowTints(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "b", PID: 1, CWD: "/a", Name: "blocked", Kind: status.Blocked, UpdatedAt: now},
		{ID: "w", PID: 2, CWD: "/a", Name: "working", Kind: status.Working, UpdatedAt: now},
		{ID: "r", PID: 3, CWD: "/a", Name: "recent", Kind: status.Idle, UpdatedAt: now.Add(-time.Minute)},
		{ID: "i", PID: 4, CWD: "/a", Name: "stale", Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	byName := map[string]Row{}
	for _, r := range m.rows {
		if r.Kind == RowSession {
			byName[r.Item.Session.Name] = r
		}
	}

	if line := m.renderSessionRow(byName["blocked"], false, 60); !strings.Contains(line, "48;5;52") {
		t.Errorf("blocked row missing red tint: %q", line)
	}
	if line := m.renderSessionRow(byName["working"], false, 60); !strings.Contains(line, "48;5;22") {
		t.Errorf("working row missing green tint: %q", line)
	}
	if line := m.renderSessionRow(byName["recent"], false, 60); strings.Contains(line, "48;5;") {
		t.Errorf("recent-idle row should have no background tint: %q", line)
	}
	if line := m.renderSessionRow(byName["stale"], false, 60); strings.Contains(line, "48;5;") {
		t.Errorf("idle row should have no background tint: %q", line)
	}
}

func TestIdleRowDimsWholeRow(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "i", PID: 1, CWD: "/a", Name: "stale", Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var row Row
	for _, r := range m.rows {
		if r.Kind == RowSession {
			row = r
		}
	}
	line := m.renderSessionRow(row, false, 60)

	// The whole row dims: the name (not just the glyph) renders in dimColor.
	nameW, _ := sessionLayout(60)
	padded := "stale" + strings.Repeat(" ", nameW-len("stale"))
	dimName := lipgloss.NewStyle().Foreground(dimColor).Render(padded)
	if !strings.Contains(line, dimName) {
		t.Errorf("idle row name should be dimmed: %q", line)
	}
}

func TestSelectionOverridesBlockedTint(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "b", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var row Row
	for _, r := range m.rows {
		if r.Kind == RowSession {
			row = r
		}
	}

	if unsel := m.renderSessionRow(row, false, 60); !strings.Contains(unsel, "48;5;52") {
		t.Errorf("unselected blocked row missing red tint: %q", unsel)
	}
	sel := m.renderSessionRow(row, true, 60)
	if strings.Contains(sel, "48;5;52") {
		t.Errorf("selection highlight should override the red tint: %q", sel)
	}
	if !strings.Contains(sel, "48;5;238") {
		t.Errorf("selected blocked row missing the highlight: %q", sel)
	}
	// The glyph keeps its red foreground on the highlight background.
	glyph := lipgloss.NewStyle().Background(selectHighlight).Foreground(lipgloss.Color("1")).Render("⚠")
	if !strings.Contains(sel, glyph) {
		t.Errorf("selected blocked glyph lost its red color: %q", sel)
	}
}
```

- [ ] **Step 3: Run the new tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestSelectionHighlight|TestSessionRowTints|TestIdleRowDimsWholeRow|TestSelectionOverridesBlockedTint'`
Expected: FAIL — `renderSessionRow` still emits the `▌` bar and no background, so
`TestSelectionHighlight` (missing `48;5;238`, still has `▌`), `TestSessionRowTints`
(missing `48;5;52`/`48;5;22`), `TestIdleRowDimsWholeRow` (name not dim), and
`TestSelectionOverridesBlockedTint` all fail on their assertions.

- [ ] **Step 4: Rewrite `renderSessionRow` in `view.go`**

Replace the whole function (currently `internal/tui/view.go:524-543`) with:

```go
func (m Model) renderSessionRow(r Row, selected bool, w int) string {
	it := r.Item
	nameW, _ := sessionLayout(w)
	cls := classify(it.Session.Kind, time.Since(it.Session.UpdatedAt))

	// The whole row paints through one base style carrying its background, so
	// the tint (or the selection highlight) fills every cell continuously —
	// gaps and the name's trailing pad included.
	bg, dim := cls.rowTint()
	if selected {
		bg = selectHighlight // selection overrides any status tint…
		dim = false          // …and the cursor row lights up rather than fading
	}
	base := lipgloss.NewStyle()
	if bg != "" {
		base = base.Background(bg)
	}

	// The glyph keeps its class color; a dim row greys the glyph and name too.
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
	b.WriteString(base.Render(" ")) // gutter cell (no more ▌ bar)
	b.WriteString(base.Render(strings.Repeat(" ", sessionIndent)))
	b.WriteString(base.Foreground(glyphFg).Render(m.sessionGlyph(cls)))
	b.WriteString(base.Render(strings.Repeat(" ", colGap)))
	b.WriteString(nameStyle.Render(name))
	b.WriteString(base.Render(strings.Repeat(" ", colGap)))
	b.WriteString(base.Foreground(dimColor).Render(age))
	return b.String()
}
```

- [ ] **Step 5: Run the new tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestSelectionHighlight|TestSessionRowTints|TestIdleRowDimsWholeRow|TestSelectionOverridesBlockedTint'`
Expected: PASS.

- [ ] **Step 6: Run the whole package to confirm nothing regressed**

Run: `go test ./internal/tui/`
Expected: PASS. In particular `TestSessionRowGeometry`, `TestSessionRowOmitsStatusWord`,
`TestRecentIdleRowStyling`, and `TestWorkingRowAnimates` still pass: under the
default Ascii profile the styled segments are plain text, so widths and glyph
positions are unchanged, and the recent/stale glyph fragments still equal
`classRecentIdle.style().Render("○")` / `classIdle.style().Render("○")`.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): full-row tint and Vim-visual session selection"
```

---

## Task 3: Repo-header tint, tinted reason row, remove the gutter bar

Finishes the bar removal: rewrites `renderRepoRow` (tint when the group holds a
blocked session, Vim-visual selection, no bar), tints `renderReasonRow` so a
blocked entry reads as one block, and deletes the now-unused `gutter()`.

**Files:**
- Modify: `internal/tui/view.go` — `renderRepoRow` (currently lines 491-509),
  `renderReasonRow` (currently lines 548-551), delete `gutter()` (currently
  lines 573-580).
- Test: `internal/tui/view_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/view_test.go`:

```go
func TestRepoHeaderTintsWhenBlocked(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked, UpdatedAt: now},
		{ID: "s2", PID: 2, CWD: "/b", Name: "calm", Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{
		"/a": {Root: "/a", Name: "aaa"},
		"/b": {Root: "/b", Name: "bbb"},
	}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var blockedRepo, calmRepo Row
	for _, r := range m.rows {
		if r.Kind != RowRepo {
			continue
		}
		if r.Group.Label == "aaa" {
			blockedRepo = r
		} else {
			calmRepo = r
		}
	}
	if line := m.renderRepoRow(blockedRepo, false, 60); !strings.Contains(line, "48;5;52") {
		t.Errorf("repo with a blocked child should tint red: %q", line)
	}
	if line := m.renderRepoRow(calmRepo, false, 60); strings.Contains(line, "48;5;52") {
		t.Errorf("repo without a blocked child should not tint: %q", line)
	}
}

func TestRepoSelectionHighlight(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := applyLoad(twoSessionModel())
	var repoRow Row
	for _, r := range m.rows {
		if r.Kind == RowRepo {
			repoRow = r
			break
		}
	}
	sel := m.renderRepoRow(repoRow, true, 60)
	if strings.Contains(sel, "▌") {
		t.Errorf("repo selection should not use a ▌ bar: %q", sel)
	}
	if !strings.Contains(sel, "48;5;238") {
		t.Errorf("selected repo row missing the highlight: %q", sel)
	}
}

func TestReasonRowTinted(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := applyLoad(twoSessionModel())
	it := &Item{Session: source.Session{Kind: status.Blocked, WaitingFor: "permission: rm"}}
	line := m.renderReasonRow(it, 60)
	if !strings.Contains(line, "48;5;52") {
		t.Errorf("reason row missing the blocked tint: %q", line)
	}
	if !strings.Contains(line, "↳ permission: rm") {
		t.Errorf("reason row missing its text: %q", line)
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestRepoHeaderTintsWhenBlocked|TestRepoSelectionHighlight|TestReasonRowTinted'`
Expected: FAIL — `renderRepoRow` still renders the `▌` bar and no tint
(`TestRepoSelectionHighlight` finds `▌`/missing `48;5;238`; `TestRepoHeaderTintsWhenBlocked`
missing `48;5;52`), and `renderReasonRow` has no background (`TestReasonRowTinted`
missing `48;5;52`).

- [ ] **Step 3: Rewrite `renderRepoRow` in `view.go`**

Replace the whole function (currently `internal/tui/view.go:491-509`) with:

```go
func (m Model) renderRepoRow(r Row, selected bool, w int) string {
	caret := "▾"
	if m.collapsed[r.Group.Key] {
		caret = "▸"
	}
	label := r.Group.Label
	if label == "" {
		label = "(no repo)"
	}

	// A repo header tints faint red when the group holds a blocked session, so a
	// problem group draws the eye even when collapsed. Selection overrides it.
	bg := lipgloss.Color("")
	if c := breakdownCounts(*r.Group); len(c) > 0 && c[0].class == classBlocked {
		bg = blockedTint
	}
	if selected {
		bg = selectHighlight
	}
	base := lipgloss.NewStyle()
	if bg != "" {
		base = base.Background(bg)
	}

	nameMax := max(w-gutterW-2, 1) // gutter cell + "▾ " (caret + space)
	label = fmt.Sprintf("%-*s", nameMax, truncate(label, nameMax))
	return base.Render(" ") + base.Render(caret+" ") + base.Bold(true).Render(label)
}
```

- [ ] **Step 4: Rewrite `renderReasonRow` in `view.go`**

Replace the whole function (currently `internal/tui/view.go:548-551`) with:

```go
func (m Model) renderReasonRow(it *Item, w int) string {
	_, nameStart := sessionLayout(w)
	base := lipgloss.NewStyle().Background(blockedTint)
	text := truncate("↳ "+it.Session.WaitingFor, w-nameStart)
	lead := base.Render(strings.Repeat(" ", nameStart))
	body := base.Foreground(dimColor).Render(fmt.Sprintf("%-*s", w-nameStart, text))
	return lead + body
}
```

- [ ] **Step 5: Delete the now-unused `gutter()` function**

Remove this function from `view.go` (currently lines 573-580); nothing calls it
anymore (the `▌` rune appears only here):

```go
// gutter renders the 1-cell selection column: a class-colored bar on the
// selected row, a space otherwise.
func gutter(selected bool, c displayClass) string {
	if !selected {
		return " "
	}
	return c.style().Render("▌")
}
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestRepoHeaderTintsWhenBlocked|TestRepoSelectionHighlight|TestReasonRowTinted'`
Expected: PASS.

- [ ] **Step 7: Run the whole package**

Run: `go test ./internal/tui/`
Expected: PASS. `TestRepoRowNoBreakdown` still passes (under Ascii the repo row
is plain `" ▾ aaa…"`, so it still contains `▾ aaa` and none of the forbidden
substrings), and `TestBlockedReasonContinuationLine` still passes (the tinted
reason row is plain under Ascii and still contains `↳ permission: rm` exactly once).

- [ ] **Step 8: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): repo header tint and tinted blocked reason row"
```

---

## Task 4: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 2: Vet and build**

Run: `go vet ./... && go build ./...`
Expected: no output, exit 0 (no unused-import/dead-code complaints; `gutter()`
is gone and `st.bold` is an unused struct field, which Go permits).

- [ ] **Step 3: Eyeball it in a real terminal**

Run the TUI against your live sessions and confirm by eye:
- Blocked rows show a red row tint with a bold name; a blocked session's `↳`
  reason line is tinted red too.
- Working rows show a faint green row tint; the spinner still animates.
- Recently-idle rows show a yellow `○` with a normal name; long-idle and unknown
  rows are dimmed across the whole row (name included), receding into the
  background.
- Moving the cursor highlights the whole row (Vim visual style) with no `▌` bar;
  selecting a blocked/working row replaces its tint with the highlight while the
  glyph keeps its color.
- A collapsed repo header whose group contains a blocked session is tinted red.
- The split (sidebar + preview) layout still aligns: tints fill each row inside
  the box without bleeding onto the `│` borders.

Use the `run` skill or `go run ./...` (check `cmd/` for the entrypoint) to launch.
This step is a manual confirmation; there is nothing to commit.
