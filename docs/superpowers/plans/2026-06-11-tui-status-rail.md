# TUI Status-Rail Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-render hopper's session list as a left status rail (glyph + word), flexible name column, right-aligned ages, per-group status breakdowns, and a `▌` selection bar — per `docs/superpowers/specs/2026-06-11-tui-status-rail-design.md`.

**Architecture:** All changes live in `internal/tui/view.go` (pure rendering) and its tests. The Bubble Tea model, tree building, sources, and terminal backends are untouched. Each task converts one visual element and keeps the whole test suite green at its commit.

**Tech Stack:** Go 1.24, lipgloss v1.1.0, bubbletea v1.3.10. Tests run with the default no-TTY termenv profile (renders are plain ASCII unless a test forces `termenv.ANSI256`).

**Verification commands used throughout:**
- Single test: `go test ./internal/tui -run <Name> -v`
- Full suite: `go test ./...`

---

### Task 1: Selection bar replaces the background highlight

Selected rows currently render colorless under a full-width background (inner ANSI
resets would break it). Replace that with a status-colored `▌` bar in a 2-cell
gutter plus a bold name, so rows keep their colors when selected. Layout columns
do not move: the gutter replaces the 2 leading spaces both row types already have.

**Files:**
- Modify: `internal/tui/view.go`
- Test: `internal/tui/view_test.go`

- [ ] **Step 1: Replace `TestSelectedRowContentIsPlain` with the failing `TestSelectedRowBar`**

In `internal/tui/view_test.go`, delete the entire `TestSelectedRowContentIsPlain`
function (lines ~80-110) and add in its place:

```go
func TestSelectedRowBar(t *testing.T) {
	// Force a color profile so styles emit ANSI; restore afterward.
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

	sel := m.renderSessionRow(sessionRow, true)
	if !strings.Contains(sel, "▌") {
		t.Errorf("selected row missing ▌ bar: %q", sel)
	}
	if !strings.ContainsRune(sel, '\x1b') {
		t.Errorf("selected row should keep ANSI styling: %q", sel)
	}
	unsel := m.renderSessionRow(sessionRow, false)
	if strings.Contains(unsel, "▌") {
		t.Errorf("unselected row has ▌ bar: %q", unsel)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui -run TestSelectedRowBar -v`
Expected: FAIL — selected rows have no `▌` and (being rendered plain) no ANSI.

- [ ] **Step 3: Implement gutter + bold selection in view.go**

In `internal/tui/view.go`:

3a. In the `styles` struct, replace the `selected` field with `bold`:

```go
type styles struct {
	header   lipgloss.Style
	count    lipgloss.Style
	repoName lipgloss.Style
	meta     lipgloss.Style
	bold     lipgloss.Style
	footer   lipgloss.Style
}
```

and in `newStyles()` replace the `selected:` line with:

```go
		bold:     lipgloss.NewStyle().Bold(true),
```

3b. Add the gutter helper (near `icon`):

```go
// gutter renders the 2-cell selection column: a status-colored bar on the
// selected row, spaces otherwise.
func gutter(selected bool, k status.Kind) string {
	if !selected {
		return "  "
	}
	return statusStyle(k).Render("▌") + " "
}
```

3c. Replace `renderRow` (the background wrap goes away):

```go
func (m Model) renderRow(i int, r Row, w int) string {
	selected := i == m.cursor
	if r.Kind == RowRepo {
		return m.renderRepoRow(r, selected)
	}
	return m.renderSessionRow(r, selected)
}
```

3d. Replace `renderRepoRow` — always styled, gutter replaces the leading two
spaces, no other layout change:

```go
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
	name := st.repoName.Render(fmt.Sprintf("%-*s", repoNameCol, truncate(label, repoNameCol)))
	branchCol := st.meta.Render(fmt.Sprintf("%-*s", repoBranchCol, truncate(branch, repoBranchCol)))
	badge := statusStyle(r.Group.Kind).Render(fmt.Sprintf("%s %d %s",
		icon(r.Group.Kind), worstKindCount(*r.Group), r.Group.Kind.Label()))
	return gutter(selected, r.Group.Kind) + fmt.Sprintf("%s %s %s  %s", caret, name, branchCol, badge)
}
```

3e. Replace `renderSessionRow` — always styled, gutter + 4 spaces keeps the
original 6-space indent, name goes bold when selected:

```go
func (m Model) renderSessionRow(r Row, selected bool) string {
	it := r.Item
	name := fmt.Sprintf("%-*s", sessionNameCol, truncate(it.Session.Name, sessionNameCol))
	if selected {
		name = st.bold.Render(name)
	}
	statusField := statusStyle(it.Session.Kind).Render(
		fmt.Sprintf("%s %-8s", icon(it.Session.Kind), statusText(it)))
	age := st.meta.Render(shortAge(time.Since(it.Session.UpdatedAt)))
	reason := ""
	if it.Session.Kind == status.Blocked && it.Session.WaitingFor != "" {
		reason = st.meta.Render(" · " + it.Session.WaitingFor)
	}
	return gutter(selected, it.Session.Kind) + fmt.Sprintf("    %s  %s  %s%s", name, statusField, age, reason)
}
```

Also delete the two stale comments inside the old functions about leaving the
selected row plain — that constraint no longer exists.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/tui -v`
Expected: all PASS, including the untouched `TestStatusColumnsAlign` (columns
did not move) and `TestSelectedRowBar`.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): selection bar + bold instead of background highlight"
```

---

### Task 2: Session rows become a left status rail

Session row anatomy becomes
`gutter(2) indent(2) glyph(1) ' ' word(8) gap(2) name(flex) gap(2) age(4, right-aligned)`.
When the flexible name width would drop below 12 cells, the status word column is
dropped (`gutter(2) indent(2) glyph(1) gap(2) name gap(2) age(4)`). The trailing
`· reason` is removed here and returns as a continuation line in Task 3.

**Files:**
- Modify: `internal/tui/view.go`
- Test: `internal/tui/view_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/tui/view_test.go`:

1a. Delete `TestStatusColumnsAlign` and the `statusColumn` helper — the design
property they checked (session status aligned with the repo badge mid-line) no
longer exists. Remove `unicode/utf8` from the imports ONLY if nothing else uses
it (the new test below uses it, so keep it).

1b. Add:

```go
func TestSessionLayout(t *testing.T) {
	nameW, nameStart, showWord := sessionLayout(80)
	if nameW != 58 || nameStart != 16 || !showWord {
		t.Errorf("sessionLayout(80) = %d,%d,%v want 58,16,true", nameW, nameStart, showWord)
	}
	// 32-22 = 10 < minNameW, so the status word drops out.
	nameW, nameStart, showWord = sessionLayout(32)
	if nameW != 19 || nameStart != 7 || showWord {
		t.Errorf("sessionLayout(32) = %d,%d,%v want 19,7,false", nameW, nameStart, showWord)
	}
}

func TestSessionRowGeometry(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowSession && rr.Item.Session.Kind == status.Working {
			r = rr
		}
	}
	line := m.renderSessionRow(r, false, 60)
	if got := utf8.RuneCountInString(line); got != 60 {
		t.Fatalf("row width = %d, want 60: %q", got, line)
	}
	runes := []rune(line)
	if string(runes[4]) != "●" {
		t.Errorf("glyph at col 4 = %q: %q", string(runes[4]), line)
	}
	if got := string(runes[6:13]); got != "working" {
		t.Errorf("status word = %q: %q", got, line)
	}
	if got := string(runes[16:21]); got != "first" {
		t.Errorf("name at col 16 = %q: %q", got, line)
	}
	if !strings.HasSuffix(line, "0m") {
		t.Errorf("age not right-aligned at edge: %q", line)
	}
}

func TestSessionRowNarrowDropsStatusWord(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowSession && rr.Item.Session.Kind == status.Working {
			r = rr
		}
	}
	line := m.renderSessionRow(r, false, 32)
	if strings.Contains(line, "working") {
		t.Errorf("status word should be dropped at width 32: %q", line)
	}
	if got := utf8.RuneCountInString(line); got != 32 {
		t.Errorf("row width = %d, want 32: %q", got, line)
	}
	runes := []rune(line)
	if string(runes[4]) != "●" {
		t.Errorf("glyph at col 4 = %q: %q", string(runes[4]), line)
	}
}
```

1c. Update `TestSelectedRowBar` for the new signature: both `m.renderSessionRow(sessionRow, true)`
and `m.renderSessionRow(sessionRow, false)` gain a width argument → `m.renderSessionRow(sessionRow, true, 60)`
and `m.renderSessionRow(sessionRow, false, 60)`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui -run 'TestSessionLayout|TestSessionRowGeometry|TestSessionRowNarrowDropsStatusWord' -v`
Expected: compile FAILURE — `sessionLayout` undefined, `renderSessionRow` has the wrong arity.

- [ ] **Step 3: Implement the new session row**

In `internal/tui/view.go`:

3a. Replace the old layout constants block (`repoNameCol`, `repoBranchCol`,
`statusCol`, `sessionNameCol` stay for now — the repo row still uses the first
three until Task 4) by ADDING below it:

```go
// Status-rail geometry. A session row is:
//   gutter(2) indent(2) glyph(1) ' ' word(8) gap(2) name(flex) gap(2) age(4)
// When the name would drop below minNameW, the word column is dropped:
//   gutter(2) indent(2) glyph(1) gap(2) name(flex) gap(2) age(4)
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
```

3b. Replace `renderSessionRow` entirely:

```go
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
```

Note the `· reason` suffix is intentionally gone; Task 3 brings the reason back
as a continuation line.

3c. In `renderRow`, pass the width through:

```go
	return m.renderSessionRow(r, selected, w)
```

- [ ] **Step 4: Run the full package tests**

Run: `go test ./internal/tui -v`
Expected: all PASS (including `TestViewContents`, which only checks substrings).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): status-rail session rows with right-aligned age"
```

---

### Task 3: Blocked reason gets a dimmed continuation line

A blocked session's `WaitingFor` renders on its own dimmed line below the row,
indented to the name column: `↳ permission: Bash(rm -rf …)`. The line is
display-only — it is not a `Row`, so the cursor never lands on it.

**Files:**
- Modify: `internal/tui/view.go`
- Test: `internal/tui/view_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/view_test.go` (add `"time"` and `"hopper/internal/repo"` to
this file's imports):

```go
func TestBlockedReasonContinuationLine(t *testing.T) {
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked, WaitingFor: "permission: rm", UpdatedAt: now},
		{ID: "s2", PID: 2, CWD: "/a", Name: "fine", Kind: status.Working, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))
	m.width = 60

	out := m.View()
	if !strings.Contains(out, "↳ permission: rm") {
		t.Errorf("missing reason continuation line:\n%s", out)
	}
	if got := strings.Count(out, "↳"); got != 1 {
		t.Errorf("continuation lines = %d, want 1 (only the blocked session):\n%s", got, out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui -run TestBlockedReasonContinuationLine -v`
Expected: FAIL — no `↳` in the output.

- [ ] **Step 3: Implement the continuation line**

In `internal/tui/view.go`:

3a. Add below `renderSessionRow`:

```go
// renderReasonRow is the dimmed continuation line carrying a blocked session's
// reason, indented to the name column. It is display-only: it is not a Row, so
// the cursor never lands on it.
func (m Model) renderReasonRow(it *Item, w int) string {
	_, nameStart, _ := sessionLayout(w)
	return strings.Repeat(" ", nameStart) + st.meta.Render(truncate("↳ "+it.Session.WaitingFor, w-nameStart))
}
```

3b. In `View`, extend the row loop to emit it after blocked session rows:

```go
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
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/tui -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): blocked reason on dimmed continuation line"
```

---

### Task 4: Repo headers show a full status breakdown, branch removed

Repo row anatomy becomes `gutter(2) caret ' ' bold-name … breakdown` with the
breakdown right-aligned to the terminal edge: `⚠ 1 blocked · ● 1 working · ○ 1 idle`,
worst first, zero counts omitted, each segment in its status color. The branch
column and the worst-only badge are deleted.

**Files:**
- Modify: `internal/tui/view.go`
- Test: `internal/tui/view_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/tui/view_test.go`, delete `TestWorstKindCount` and add:

```go
func TestBreakdownCounts(t *testing.T) {
	g := Group{Items: []Item{
		{Session: source.Session{Kind: status.Idle}},
		{Session: source.Session{Kind: status.Blocked}},
		{Session: source.Session{Kind: status.Working}},
		{Session: source.Session{Kind: status.Working}},
	}}
	got := breakdownCounts(g)
	want := []kindCount{{status.Blocked, 1}, {status.Working, 2}, {status.Idle, 1}}
	if len(got) != len(want) {
		t.Fatalf("breakdownCounts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("breakdownCounts = %v, want %v", got, want)
		}
	}
}

func TestRepoRowBreakdownRightAligned(t *testing.T) {
	m := applyLoad(twoSessionModel())
	var r Row
	for _, rr := range m.rows {
		if rr.Kind == RowRepo {
			r = rr
		}
	}
	line := m.renderRepoRow(r, false, 60)
	if got := lipgloss.Width(line); got != 60 {
		t.Errorf("repo row width = %d, want 60: %q", got, line)
	}
	if !strings.HasSuffix(line, "● 1 working · ○ 1 idle") {
		t.Errorf("breakdown not right-aligned, worst-first: %q", line)
	}
	if !strings.Contains(line, "▾ aaa") {
		t.Errorf("missing caret+name: %q", line)
	}
	if strings.Contains(line, "main") {
		t.Errorf("branch should be removed: %q", line)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui -run 'TestBreakdownCounts|TestRepoRowBreakdownRightAligned' -v`
Expected: compile FAILURE — `breakdownCounts`, `kindCount` undefined; `renderRepoRow` has the wrong arity.

- [ ] **Step 3: Implement the new repo row**

In `internal/tui/view.go`:

3a. Add `"sort"` to the imports.

3b. Delete the old constants `repoNameCol`, `repoBranchCol`, `statusCol`,
`sessionNameCol` (and their doc comments) — nothing references them after this
step. Delete `worstKindCount`.

3c. Add the breakdown helpers (near `statusStyle`):

```go
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
```

3d. Replace `renderRepoRow`:

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
	breakdown := renderBreakdown(*r.Group)
	bw := lipgloss.Width(breakdown)
	nameMax := max(w-gutterW-2-colGap-bw, 1) // "▾ " takes 2 cells
	name := st.repoName.Render(truncate(label, nameMax))
	left := gutter(selected, r.Group.Kind) + caret + " " + name
	gap := max(w-lipgloss.Width(left)-bw, 1)
	return left + strings.Repeat(" ", gap) + breakdown
}
```

3e. In `renderRow`, pass the width through:

```go
		return m.renderRepoRow(r, selected, w)
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/tui -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): repo headers with right-aligned status breakdown, drop branch"
```

---

### Task 5: Visual check and cleanup

**Files:**
- Delete: `internal/tui/render_demo_test.go`

- [ ] **Step 1: Eyeball the final rendering**

Run: `go test ./internal/tui -run RenderDemo -v`

Compare against the spec's target rendering: status rail left with colors,
ages right-aligned, breakdown right-aligned on repo headers, no branch text,
blocked reason on its own `↳` line, selected row marked with `▌` and bold name.
If anything looks off, fix it before proceeding (and add a regression test for
whatever was wrong).

- [ ] **Step 2: Delete the demo test**

```bash
git rm internal/tui/render_demo_test.go
```

(The demo was a throwaway visual aid from brainstorming; it prints noise into
test output and asserts nothing.)

- [ ] **Step 3: Full verification**

Run, expecting clean output from each:

```bash
gofmt -l .            # expected: no output
go vet ./...          # expected: no output
go test ./...         # expected: all packages ok
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(tui): remove render demo test"
```

---

## Notes for the implementer

- Tests run without a TTY, so the default lipgloss color profile renders plain
  text (no ANSI). Tests that assert on column positions rely on this; tests that
  assert on ANSI presence force `termenv.ANSI256` and restore the old profile.
- `twoSessionModel()` (in `model_test.go`) yields rows `[repo "aaa", session
  "first" (working), session "second" (idle)]` with `UpdatedAt: now` → age `0m`.
- Between Tasks 2 and 3 the blocked reason is not displayed at all; that is
  expected and resolved by Task 3. Don't "fix" it early.
- `max` is the Go 1.21+ builtin; the module is on Go 1.24.
