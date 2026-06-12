# Recent-idle highlighting

**Date:** 2026-06-12
**Status:** approved
**Scope:** `internal/tui/view.go` (and possibly `tree.go`) plus tests. No changes to
`internal/status`, sources, or session layers.

## Problem

All idle sessions render identically in dim gray, whether they went idle ten
seconds ago or two hours ago. A session that just went idle almost always means
Claude finished and is waiting on the user — that row is actionable and should
stand out, while a long-idle session is stale background noise.

## Goal

Recency bias in the display: a recently idle session reads as "needs your
attention" at a glance, then fades into the gray background once it has been
sitting for a while.

## Decisions

- **Two-tier cutoff**, not a fade or gradient: yellow while recently idle, then
  the existing dim gray. One threshold, scannable, testable.
- **Threshold:** idle for less than 5 minutes counts as recent
  (`recentIdleWindow = 5 * time.Minute`).
- **Scope:** the session row's glyph/status word/gutter, plus a distinct
  `recent` segment in the repo header's breakdown so a collapsed group still
  surfaces the signal.
- **Classification lives in the TUI**, derived at render time. `status.Kind`
  stays provider-neutral and time-independent.

## Target rendering

```
▾ hopper             ● 1 working · ○ 1 recent · ○ 1 idle
    ● working   add tests                             1m
    ○ idle      fix auth bug                          2m   ← yellow
    ○ idle      refactor parser                      18m   ← dim gray

▸ other-repo                                ○ 1 recent   ← visible even collapsed
```

The status word still reads `idle`; only its color changes. The breakdown
segment is labeled `recent` and sits in rank order, worst first:
`blocked · working · recent · idle`.

## Design

A render-time display class in `internal/tui` — `status.Kind` plus a recency
split of `Idle`:

```go
type displayClass int // classUnknown, classIdle, classRecentIdle, classWorking, classBlocked

const recentIdleWindow = 5 * time.Minute

// classify maps a status kind + age-since-status-change to a display class.
// Only Idle splits: idle for less than recentIdleWindow → classRecentIdle.
func classify(k status.Kind, age time.Duration) displayClass
```

`classify` is a pure function of `(kind, age)`; call sites pass
`time.Since(Session.UpdatedAt)`, which for an idle session is time since it
went idle. The existing 1-second refresh tick re-renders the view, so a row
fades yellow→gray on its own when it crosses the threshold.

Rendering changes in `view.go`:

- `statusStyle`, `icon`, and `gutter` key off `displayClass` instead of
  `status.Kind`. RecentIdle keeps the `○` glyph (open circle = not working)
  and colors it ANSI `3` (yellow), matching the existing 16-color palette
  (`1`/`2`/`8`).
- `breakdownCounts` counts by display class and gains a label for the new
  segment (`recent`). Ordering stays rank-based, worst first.
- The repo header's selected-gutter color derives from the worst display class
  among the group's sessions (today it uses `Group.Kind`). Simplest source of
  truth: the first segment of the already-computed breakdown.

## What does not change

- `status.Kind`, its `Label`/`Rank`, and the source/session layers.
- Sorting: idle rows already sort by `UpdatedAt` within rank, so recently idle
  rows float to the top naturally and nothing reshuffles when a row crosses
  the threshold — only its color changes.
- The age column (stays dim), blocked-reason rows, preview, filtering.

## Edge cases

- `Working`/`Blocked`/`Unknown` never split on age — only `Idle`.
- Exactly at the boundary: `age < recentIdleWindow` is recent; 5m00s is idle.
- A repo whose only session is recent-idle shows `○ 1 recent` as its entire
  breakdown.

## Testing

- `classify` boundary tests: 4m59s → `classRecentIdle`, 5m → `classIdle`;
  non-idle kinds unaffected by age.
- Breakdown counting and ordering with mixed recent/stale idle sessions.
- Session-row render test asserting the yellow style on a recent-idle row and
  dim gray on a stale one.
