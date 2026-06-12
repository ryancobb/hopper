# Recent-Idle Highlighting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Idle sessions render yellow for their first 5 minutes (glyph, status word, selection gutter, and a `recent` repo-breakdown segment), then fade to the existing dim gray.

**Architecture:** A new render-time `displayClass` enum in `internal/tui` splits `status.Kind`'s `Idle` into recent/stale via a pure `classify(kind, age)` function. All status-colored rendering in `view.go` (session rows, gutter, repo breakdown, repo gutter) keys off `displayClass` instead of `status.Kind`. The provider-neutral `status` package is untouched; the existing 1-second tick re-renders, so rows fade on their own. Spec: `docs/superpowers/specs/2026-06-12-recent-idle-highlight-design.md`.

**Tech Stack:** Go, lipgloss (16-color ANSI palette), standard `go test`.

---

## Pre-flight

The working tree has uncommitted changes to `internal/tui/view.go`, `internal/tui/model_test.go`, `internal/terminal/kitty.go`, and `internal/terminal/kitty_test.go`. **Stop and ask the user to commit or stash these before starting** — Tasks 2–3 modify and commit `internal/tui/view.go` and test files, and must not sweep unrelated work into their commits.

Run all tests from the repo root: `/Users/ryancobb/Projects/hopper`.

---

### Task 1: `displayClass` and `classify`

A new file holding the display-class enum and its appearance methods. Declaration order is rank order (worst = highest), so classes compare directly with `>`.

**Files:**
- Create: `internal/tui/class.go`
- Create: `internal/tui/class_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/class_test.go`:

```go
package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"hopper/internal/status"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		kind status.Kind
		age  time.Duration
		want displayClass
	}{
		{status.Idle, 0, classRecentIdle},
		{status.Idle, recentIdleWindow - time.Second, classRecentIdle},
		{status.Idle, recentIdleWindow, classIdle}, // boundary: exactly 5m is stale
		{status.Idle, 2 * time.Hour, classIdle},
		{status.Working, 2 * time.Hour, classWorking}, // only Idle splits on age
		{status.Blocked, 2 * time.Hour, classBlocked},
		{status.Unknown, 0, classUnknown},
	}
	for _, c := range cases {
		if got := classify(c.kind, c.age); got != c.want {
			t.Errorf("classify(%v, %v) = %v, want %v", c.kind, c.age, got, c.want)
		}
	}
}

func TestClassRecentIdleAppearance(t *testing.T) {
	if got := classRecentIdle.label(); got != "recent" {
		t.Errorf("label = %q, want %q", got, "recent")
	}
	if got := classRecentIdle.icon(); got != "○" {
		t.Errorf("icon = %q, want %q", got, "○")
	}
	if got := classRecentIdle.style().GetForeground(); got != lipgloss.Color("3") {
		t.Errorf("foreground = %v, want yellow (3)", got)
	}
}

func TestClassRankOrder(t *testing.T) {
	if !(classBlocked > classWorking && classWorking > classRecentIdle &&
		classRecentIdle > classIdle && classIdle > classUnknown) {
		t.Error("classes must rank worst-highest: blocked > working > recent > idle > unknown")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestClass' -v`
Expected: compile error — `displayClass`, `classify`, `recentIdleWindow` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/class.go`:

```go
package tui

import (
	"time"

	"github.com/charmbracelet/lipgloss"
	"hopper/internal/status"
)

// displayClass is the render-time status category: status.Kind plus a
// recency split of Idle. Declaration order is rank order, worst highest,
// so classes compare directly.
type displayClass int

const (
	classUnknown displayClass = iota
	classIdle
	classRecentIdle
	classWorking
	classBlocked
)

// recentIdleWindow is how long an idle session stays "recently idle"
// (rendered yellow) before fading to the dim idle look.
const recentIdleWindow = 5 * time.Minute

// classify maps a status kind and its age (time since the status last
// changed) to a display class. Only Idle splits on age: a session idle for
// less than recentIdleWindow likely just finished and is waiting on the user.
func classify(k status.Kind, age time.Duration) displayClass {
	switch k {
	case status.Blocked:
		return classBlocked
	case status.Working:
		return classWorking
	case status.Idle:
		if age < recentIdleWindow {
			return classRecentIdle
		}
		return classIdle
	default:
		return classUnknown
	}
}

// label is the repo-breakdown segment word. Session rows keep their status
// word from status.Kind, so "recent" appears only in repo headers.
func (c displayClass) label() string {
	switch c {
	case classBlocked:
		return "blocked"
	case classWorking:
		return "working"
	case classRecentIdle:
		return "recent"
	case classIdle:
		return "idle"
	default:
		return "unknown"
	}
}

func (c displayClass) icon() string {
	switch c {
	case classBlocked:
		return "⚠"
	case classWorking:
		return "●"
	case classRecentIdle, classIdle:
		return "○"
	default:
		return "·"
	}
}

func (c displayClass) style() lipgloss.Style {
	switch c {
	case classBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	case classWorking:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case classRecentIdle:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	case classIdle:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	default:
		return lipgloss.NewStyle()
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestClass' -v`
Expected: PASS (3 tests). Note `go vet`/the compiler will not complain about the as-yet-unused functions because they are methods/package-level.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/class.go internal/tui/class_test.go
git commit -m "feat(tui): displayClass with recency split of idle"
```

---

### Task 2: Render by display class

Switch all status-colored rendering in `view.go` to `displayClass`: session rows (glyph, word color, gutter), the repo breakdown (new `recent` segment), and the repo header's selected-gutter color (worst class, taken from the breakdown's first segment). Delete the now-unused `statusStyle` and `icon(status.Kind)` functions.

This task is one atomic change because `gutter` is shared by session and repo rows — changing its signature requires updating every call site in the same commit.

**Files:**
- Modify: `internal/tui/view.go` (functions `renderSessionRow`, `renderRepoRow`, `gutter`, `breakdownCounts`, `renderBreakdown`, type `kindCount`; delete `statusStyle`, `icon`)
- Modify: `internal/tui/view_test.go` (update `TestBreakdownCounts`, `TestRepoRowBreakdownRightAligned`; add `TestRecentIdleRowStyling`)

- [ ] **Step 1: Add the failing rendering test**

Add to `internal/tui/view_test.go`:

```go
func TestRecentIdleRowStyling(t *testing.T) {
	// Force a color profile so styles emit ANSI; restore afterward.
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "fresh", Kind: status.Idle, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "s2", PID: 2, CWD: "/a", Name: "stale", Kind: status.Idle, UpdatedAt: now.Add(-18 * time.Minute)},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))

	var fresh, stale Row
	for _, r := range m.rows {
		if r.Kind != RowSession {
			continue
		}
		if r.Item.Session.Name == "fresh" {
			fresh = r
		} else {
			stale = r
		}
	}

	freshLine := m.renderSessionRow(fresh, false, 60)
	if !strings.Contains(freshLine, classRecentIdle.style().Render("○")) {
		t.Errorf("fresh idle row not in recent style: %q", freshLine)
	}
	staleLine := m.renderSessionRow(stale, false, 60)
	if !strings.Contains(staleLine, classIdle.style().Render("○")) {
		t.Errorf("stale idle row not in idle style: %q", staleLine)
	}
	if strings.Contains(staleLine, classRecentIdle.style().Render("○")) {
		t.Errorf("stale idle row styled as recent: %q", staleLine)
	}
	// The status word still reads "idle" on both rows; only color differs.
	if !strings.Contains(freshLine, "idle") || !strings.Contains(staleLine, "idle") {
		t.Errorf("status word should stay \"idle\":\n%q\n%q", freshLine, staleLine)
	}
}
```

- [ ] **Step 2: Update the two existing tests that the recency split changes**

In `internal/tui/view_test.go`, `twoSessionModel()`'s idle session has `UpdatedAt: now`, so it now classifies as *recent*.

Replace `TestBreakdownCounts` entirely (the `kindCount` type is being replaced by `classCount`, and the test gains a recent/stale idle pair):

```go
func TestBreakdownCounts(t *testing.T) {
	now := time.Now()
	g := Group{Items: []Item{
		{Session: source.Session{Kind: status.Idle, UpdatedAt: now.Add(-time.Hour)}},
		{Session: source.Session{Kind: status.Idle, UpdatedAt: now.Add(-2 * time.Minute)}},
		{Session: source.Session{Kind: status.Blocked, UpdatedAt: now}},
		{Session: source.Session{Kind: status.Working, UpdatedAt: now}},
		{Session: source.Session{Kind: status.Working, UpdatedAt: now}},
	}}
	got := breakdownCounts(g)
	want := []classCount{{classBlocked, 1}, {classWorking, 2}, {classRecentIdle, 1}, {classIdle, 1}}
	if len(got) != len(want) {
		t.Fatalf("breakdownCounts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("breakdownCounts = %v, want %v", got, want)
		}
	}
}
```

In `TestRepoRowBreakdownRightAligned`, change the expected suffix:

```go
	if !strings.HasSuffix(line, "● 1 working · ○ 1 recent") {
		t.Errorf("breakdown not right-aligned, worst-first: %q", line)
	}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestRecentIdleRowStyling|TestBreakdownCounts|TestRepoRowBreakdownRightAligned' -v`
Expected: compile error — `classCount` undefined.

- [ ] **Step 4: Implement the view changes**

All changes in `internal/tui/view.go`.

(a) Update the comment on the `styles` struct (it references `statusStyle`, which is going away):

```go
// styles holds the structural lipgloss styles for the view. Status colors
// live on displayClass so session rows and repo badges share one mapping.
type styles struct {
```

(b) Replace `renderSessionRow` — classify once, use the class for gutter, glyph, and word color (`statusText` stays `Kind`-based, so the word still reads `idle`):

```go
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
```

(c) Replace the `kindCount` type and `breakdownCounts`/`renderBreakdown`:

```go
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
```

(d) Replace `renderRepoRow` — the gutter color comes from the worst class present, i.e. the breakdown's first segment (this replaces the old `r.Group.Kind` usage):

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
```

(e) Replace `gutter`:

```go
// gutter renders the 2-cell selection column: a class-colored bar on the
// selected row, spaces otherwise.
func gutter(selected bool, c displayClass) string {
	if !selected {
		return "  "
	}
	return c.style().Render("▌") + " "
}
```

(f) Delete the `statusStyle(k status.Kind)` and `icon(k status.Kind)` functions — `displayClass.style()` and `displayClass.icon()` replace them. `statusText` is unchanged.

- [ ] **Step 5: Run the full package tests**

Run: `go test ./internal/tui/ -v`
Expected: all PASS. If anything else asserted on breakdown text or `kindCount`, fix it to match the class-based forms above.

- [ ] **Step 6: Run the whole suite and vet**

Run: `go vet ./... && go test ./...`
Expected: clean — `status.Kind` consumers outside `internal/tui` are untouched.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): yellow highlight for recently idle sessions"
```

---

### Task 3: Remove the now-unused `Group.Kind`

`renderRepoRow` was `Group.Kind`'s only consumer; it now derives the worst display class from the breakdown. Drop the field and its aggregation.

**Files:**
- Modify: `internal/tui/tree.go` (type `Group`, function `BuildGroups`)
- Modify: `internal/tui/tree_test.go` (remove the aggregate assertion)

- [ ] **Step 1: Remove the field and aggregation**

In `internal/tui/tree.go`, change the `Group` type:

```go
// Group is a repo with its sessions.
type Group struct {
	Key   string // repo root, or "" for the no-repo bucket
	Label string
	Items []Item
}
```

In `BuildGroups`, update the doc comment to `// BuildGroups groups items by repo root and sorts.` and delete:

```go
		if it.Session.Kind.Rank() > g.Kind.Rank() {
			g.Kind = it.Session.Kind
		}
```

- [ ] **Step 2: Remove the aggregate assertion from the test**

In `internal/tui/tree_test.go`, delete:

```go
	if groups[0].Kind != status.Blocked {
		t.Fatalf("aaa aggregate = %v", groups[0].Kind)
	}
```

(The rest of that test still verifies grouping and sort order. If `status` becomes an unused import after this deletion, remove it from the import block.)

- [ ] **Step 3: Build and test**

Run: `go vet ./... && go test ./...`
Expected: clean compile (proves no other `Group.Kind` consumer existed), all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/tree.go internal/tui/tree_test.go
git commit -m "refactor(tui): drop unused Group.Kind aggregate"
```

---

### Task 4: Eyeball it

- [ ] **Step 1: Run hopper against live sessions**

Run: `go run . ` from the repo root in a terminal with live Claude sessions (or just confirm the test suite covers it if none are live).
Expected: a session that went idle within 5 minutes shows a yellow `○ idle`; older idle sessions stay gray; repo headers show `○ N recent` segments; after 5 minutes a yellow row fades to gray on its own (1s tick, no input needed).
