# Status visual hierarchy

**Date:** 2026-06-12
**Status:** approved (implemented)
**Scope:** `internal/tui/class.go` and `internal/tui/view.go` plus tests. No
changes to `internal/status`, sources, or session layers.

> **Revision (2026-06-12):** the first cut used a full-row background *tint* for
> urgent rows. In practice that read as too heavy and competed with the
> selection highlight (also a full-row background). It was replaced with a 1-cell
> **left accent stripe** in the status color, reserving the full-row background
> for selection alone. This document describes the final, stripe-based design;
> the as-built code is the authoritative reference.

## Problem

Status is carried almost entirely by glyph color on an icon-only row, with the
selection cue a thin class-colored `▌` gutter bar. The five display classes are
all roughly equal visual weight, so nothing jumps out: a blocked session that
needs the user reads about as loud as a session that has been idle for an hour.
Two specific weaknesses:

- **Quiet rows don't recede.** Idle/unknown rows dim only the *glyph*; the
  session name stays full brightness, so stale rows still compete for the eye.
- **No attention hierarchy.** There is no "loud vs quiet" split, so scanning a
  busy list for the rows that need action is slow.

## Goal

A clear attention hierarchy. Urgent statuses (blocked, working) catch the eye
via a 1-cell left accent stripe in the status color; quiet statuses (idle,
unknown) fully recede; the selected row reads like a Vim visual-mode selection.
The full-row background is reserved for selection alone.

## Decisions

- **Left accent stripe for urgent classes.** Blocked and working get a 1-cell
  `▌` half-block in the leftmost (gutter) cell, drawn in the status color (red /
  green — the same color as the glyph). The row body stays on the normal
  background.
- **Fade the whole quiet row.** Idle and unknown dim the entire row (glyph,
  name, and age, not just the glyph), so the quiet band genuinely recedes.
- **Recent-idle is a glyph signal only**, no stripe: `○` in yellow, normal-weight
  name. It is the middle tier between the striped and the dim bands.
- **Selection = Vim visual mode.** The cursor row gets a full-width neutral
  highlight background plus a bold name — the only full-row background in the
  list. The accent stripe and the glyph keep their status color, painted *over*
  the highlight (the stripe must not punch a default-background hole in the
  highlighted row), so status stays fully readable on the cursor row. The old
  selection cue — a class-colored `▌` gutter bar shown only on the selected row —
  is gone; the `▌` glyph is now reused for the always-on status stripe.
- **Repo headers stripe by worst child.** A repo header whose group contains a
  blocked session gets the red accent stripe, visible even when the group is
  collapsed. Blocked sessions' `↳ reason` continuation rows get the same red
  stripe so a blocked entry reads as one cohesive block.
- **Colors stay render-time** in `internal/tui`; `status.Kind` is untouched.

## Target rendering

```
▌⚠ fix-billing-bug        2m     ← blocked: red stripe, bold name
▌⠙ add-search-index       0m     ← working: green stripe
 ○ refactor-auth          1m     ← recent-idle: yellow glyph, no stripe
 ○ old-experiment        47m     ← idle: whole row dim, no stripe
 · unknown-session        3m     ← unknown: whole row dim
```

Selected row (cursor on the blocked one), Vim-visual style — neutral highlight
across the full width, bold name; the red stripe and the `⚠` glyph stay, painted
over the highlight:

```
▌⚠ fix-billing-bug        2m     ← selected: highlight bg, stripe + glyph kept
▌⠙ add-search-index       0m
 ○ refactor-auth          1m
 ○ old-experiment        47m
 · unknown-session        3m
```

Collapsed repo header whose group contains a blocked session:

```
▌▸ payments-api                   ← red accent stripe (has a blocked child)
```

## Design

### `class.go`: per-class row treatment

`displayClass` keeps `icon()` and `style()` (the glyph foreground). Add the
row-level treatment as data on the class:

```go
// accent reports whether this class shows a left status stripe and whether its
// whole row is dimmed. The selected row overrides both; the glyph foreground
// (style()) is layered on top and carries the status even when selected.
func (c displayClass) accent() (stripe, dim bool)
```

| Class | accent stripe | dim row | glyph fg (`style()`) | name |
|---|---|---|---|---|
| `classBlocked` | yes (red) | no | red `1` | bold |
| `classWorking` | yes (green) | no | green `2` | normal |
| `classRecentIdle` | no | no | yellow `3` | normal |
| `classIdle` | no | yes | gray `8` | dim |
| `classUnknown` | no | yes | default | dim |

The stripe color is the class's own glyph color (`style().GetForeground()`), so
there is no separate status-color constant. Two render-time constants remain:
`selectHighlight` (the selection background, 256-color `238`) and `dimColor`
(`8`); both tunable, and movable to `lipgloss.AdaptiveColor` later if
light-terminal support is wanted.

### `view.go`: stripe in the gutter, highlight only on selection

A small helper draws the stripe:

```go
// accentBar is the 1-cell left status stripe: a half-block in color c.
func accentBar(c lipgloss.TerminalColor) string
```

`renderSessionRow`:

- `base` is plain, except on the selected row where it carries
  `Background(selectHighlight)`. Every segment (gutter cell, indent, glyph, gaps,
  padded name, age) renders through `base`, so when selected the highlight fills
  the row continuously and otherwise the row body sits on the normal background.
- The gutter cell is `accentBar(base, cls.style().GetForeground())` for an urgent
  (striped) row, else a `base`-painted space. `accentBar` renders the `▌` through
  `base`, so on the cursor row the stripe keeps its status color with the
  selection highlight behind it (no default-background hole); on a non-urgent row
  the cell is just the (possibly highlighted) space.
- Glyph foreground is `style()` (the spinner frame for working), forced to
  `dimColor` on a dim row. Name is bold when `selected || classBlocked`, dimmed
  on a dim row. Age is always dim. A selected row is never dimmed (`dim = false`)
  so it lights up.
- Row geometry (`sessionLayout`, `gutterW`, `sessionIndent`) is unchanged — the
  gutter is always exactly one cell whether it holds `▌`, a space, or a
  highlighted space — so the name column does not move.

`renderRepoRow`: red stripe (`accentBar(classBlocked.style().GetForeground())`)
when `breakdownCounts` reports the group's worst class is blocked; selection
highlight when selected; caret + bold label otherwise. The label is padded to
the full width through `base` so the selection highlight spans the row.

`renderReasonRow` (blocked `↳ reason`): a red stripe in the gutter cell, then the
dim reason text aligned under the name column, so the blocked entry reads as one
block. No full-row background.

### Both layouts

Session/repo rows are built to the full content width (`sessionLayout` sums to
`w`), so in the split sidebar box the selection highlight fills the box's inner
width. `boxLine` emits `ansi.ResetStyle` before the right border, so a
highlighted row never bleeds onto the `│` frame. The accent stripe is a single
left-edge cell, well inside the frame, so it raises no bleed concern.

## What does not change

- `internal/status` (`Kind`, `Label`, `Rank`), sources, session layers.
- `classify`, `recentIdleWindow`, the spinner animation, the age column's
  content, sorting, filtering, the preview pane.
- Row geometry and the split/stacked layout decision.

## Edge cases

- **Selected + blocked/working:** the stripe and glyph keep their status color,
  painted over the neutral highlight, so status stays legible on the cursor row.
- **Selected + idle:** the cursor row "lights up" — highlight background, bold
  bright name — instead of staying dim. This is the intended Vim-visual feel.
- **Working spinner color:** the spinner keeps its green foreground; with the
  full-row tint gone it sits on the normal background, so contrast is a non-issue.
- **Blocked row selected:** its `↳ reason` row is not the cursor, so it keeps its
  red stripe (not the highlight).
- **Narrow width / long name:** the name truncates as today; the stripe is a
  fixed single cell and is unaffected.

## Testing

- `class_test.go`: `accent` mapping per class — blocked/working return
  `(stripe=true, dim=false)`; idle/unknown `(false, true)`; recent-idle
  `(false, false)`.
- `view_test.go` (force `termenv.ANSI256` so styles emit escapes):
  - Blocked row contains the red `accentBar(...)` stripe and a bold name;
    working contains the green stripe; recent-idle and idle contain no `▌`.
  - Idle row is dim across the whole row (the name foreground is the gray, not
    the default), distinct from a recent-idle row.
  - Selected row carries the selection-highlight background (`48;5;238`); an
    unselected row does not. On a selected blocked row the stripe stays — the
    `▌` painted over the highlight — and the `⚠` glyph keeps its red foreground.
  - `renderRepoRow`: red stripe when the group has a blocked child — kept, over
    the highlight, when selected; no stripe otherwise. Selection highlight on
    the cursor row regardless.
  - `renderReasonRow` carries the red stripe and its `↳` text.
