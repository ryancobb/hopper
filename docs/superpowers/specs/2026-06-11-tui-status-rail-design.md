# TUI redesign: status-rail layout

**Date:** 2026-06-11
**Status:** approved
**Scope:** `internal/tui/view.go` and its tests. No changes to model, tree, sources, or terminal backends.

## Problem

The current list is jumbled and hard to scan at a glance. Specifically:

- The status indicator sits mid-line, after a fixed 20-char name column, so the eye
  has to hunt for it on every row.
- Repo headers cram name, branch, and a worst-status-only badge into adjacent
  hard-truncated columns, producing noise like `website-marketing-r… redesign/spring-202…`.
- The branch shown on a repo header is the first session's branch, which can be
  wrong for the group.
- Ages are left-aligned after the status field, so they form a ragged edge.
- A blocked session's reason trails off the end of the row and gets cut.
- The selected row must be rendered colorless (the full-width background highlight
  breaks on inner ANSI resets), so selection erases the status colors.

## Goal

A balanced, at-a-glance view of overall activity state. Information the user scans:
session name, status (with blocked reason), and age. Branch is not scanned and is
dropped from the display.

## Target rendering

```
Claude Code                                  kitty · 6 sessions · 3 repos
─────────────────────────────────────────────────────────────────────────

▾ acme-api                                       ● 1 working · ○ 1 idle
  ● working   add billing webhooks                                    1m
  ○ idle      bump deps                                              45m

▾ hopper                            ⚠ 1 blocked · ● 1 working · ○ 1 idle
  ⚠ blocked   refactor tree rendering for clarity                    14m
              ↳ permission: Bash(rm -rf …)
  ● working   fix auth flow                                           2m
  ○ idle      investigate flaky kitty test                            3h

─────────────────────────────────────────────────────────────────────────
j/k move · h/l fold · Enter focus · p preview · / filter · r refresh · q quit
```

## Design

### Session rows

Anatomy: `<gutter> <glyph> <status word> <name, flexible> <age, right-aligned>`.

- The status glyph + word form a fixed-width colored column at the left edge of the
  list, creating a vertical color rail: green `● working`, red `⚠ blocked`, dim
  `○ idle`. Unknown kinds keep showing the raw status verbatim.
- The session name occupies the flexible middle and truncates with `…`.
- Age is right-aligned to the terminal edge, forming a clean rail on the right.
- A blocked session's `WaitingFor` reason renders on its own dimmed continuation
  line under the row, indented to the name column: `↳ permission: Bash(rm -rf …)`,
  truncated to the available width. Rows themselves stay uniform.
- The continuation line belongs to its session row for cursor purposes (the cursor
  never lands on it; it is not a `Row`).

### Repo header rows

Anatomy: `<caret> <bold repo name> ... <status breakdown, right-aligned>`.

- Breakdown lists every status present in the group with its count, worst first:
  `⚠ 1 blocked · ● 1 working · ○ 1 idle`. Zero counts are omitted. Each segment is
  colored with its status color.
- The branch column is removed.
- Blank line between repo groups is kept.

### Selection

The full-width background highlight is replaced by a colored `▌` bar in the left
gutter plus a bold session/repo name. Selected rows keep their normal status
colors, so the rail never breaks. This removes the "selected row content must be
ANSI-free" constraint and its test (`TestSelectedRowContentIsPlain`).

### Width behavior

- Only the name column flexes; glyph/status/age columns are fixed.
- As width shrinks, the name truncates first; when the name column would drop
  below 12 cells, the status word is dropped (glyph-only) to give the name room.
- Header stays `Claude Code` left, `<terminal> · N sessions · N repos` right,
  with the existing gap logic.

### Unchanged

Grouping and sort order (`tree.go`), keybindings, footer text, filter behavior,
preview pane, status messages, empty ("no live sessions") and error states.

## Testing

- Rewrite `TestStatusColumnsAlign` for the new geometry: session status glyphs at
  a fixed left column; repo breakdown and ages right-aligned to the width.
- Delete `TestSelectedRowContentIsPlain`; replace with a test asserting the
  selected row carries the `▌` marker and retains ANSI styling.
- Add tests: repo breakdown counts (zero statuses omitted, worst first), blocked
  reason continuation line (present for blocked-with-reason, absent otherwise,
  truncated to width), age right-alignment at a given width, status-word
  degradation at narrow widths.
- `render_demo_test.go` (visual smoke test added during brainstorming) is deleted.
