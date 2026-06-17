# Preview: clip captured rows, with horizontal scroll

**Date:** 2026-06-16
**Status:** approved
**Scope:** `internal/tui` (`model.go`, `view.go`) plus tests; a doc-comment
touch-up in `internal/terminal/kitty.go` and `kitty_test.go`. No behavior change
to the capture itself, and nothing in `internal/source`, `internal/claude`,
`internal/session`, `internal/transcript`, or `internal/status`.

## Problem

The preview box re-wraps each captured pane line to the box's inner width
(`reflow` → `ansi.Hardwrap`). This shatters the layout of full-screen TUI output
— the common case, since hopper is a Claude-session manager and the preview
almost always shows a Claude session.

Two facts, confirmed against live `kitty @ get-text` captures:

1. **Claude emits physical rows, not soft-wrapped logical lines.** A captured
   Claude window (110 cols) had zero carriage returns: Claude hard-wraps its own
   output to the source window's width, so every screen row is already a distinct
   row. `reflow`'s premise (rejoin soft-wraps, then re-wrap) does not apply; its
   re-wrapping is pure damage.
2. **Sessions are often wider than the preview.** Live Claude sessions run as
   wide as 272 cols; the split-layout preview is ~223 cols inner. Every
   full-width source line wraps, so a 272-col divider becomes a 223-col line plus
   a ~49-col stub on the next row — sandwiching the `❯` prompt between divider
   fragments and splitting text mid-word.

## Goal

Treat the preview as a **viewport onto the source screen**: render each captured
row as-is, clipped to the box width, so layout (dividers, indentation, the prompt
box) stays intact and vertical density matches the source. Add a horizontal
offset the user can pan with `h`/`l`, so content past the right edge is reachable
rather than lost.

## Decisions

- **Clip, don't wrap.** A captured row wider than the box shows its visible
  window with a trailing `…`, never wraps onto new rows. One source row → one box
  row.
- **Horizontal scroll is clip with an adjustable left offset.** The default
  offset is 0 (plain clip, which already fixes the Claude case); `h`/`l` move it.
  Clipping and scrolling are the same code path — scroll is not a separate mode.
- **ANSI slicing is library-provided.** `ansi.TruncateLeft`/`ansi.Cut` are
  ANSI- and wide-rune-aware and re-emit the SGR style active before the cut
  point, so a styled row slices correctly with no manual style-carry code. (This
  is why the deleted `carryStyle`/`sgrPattern` machinery is not needed.)
- **Accepted tradeoff: a wide row's far edge is off-screen until scrolled.** For
  Claude the right edge is mostly padding / the right border, so offset 0 reads
  well; scrolling is the escape hatch for diffs, tables, and long code lines.
- **Capture is unchanged.** `Preview` still requests no wrap markers; kitty still
  hands back logical lines, which the view now clips instead of re-wrapping.
  Soft-wrap fidelity for non-Claude shells (wrap markers → physical rows) and
  preserving interior blank lines are explicitly **out of scope**.

## Design

### Rendering: clip + offset (`internal/tui/view.go`)

- **Delete `reflow`, `carryStyle`, and `sgrPattern`.** They exist solely to
  support wrapping. Nothing else references them.

- **`previewBody` collapses away.** Today it returns `reflow(content,
  innerWidth(w))`. With reflow gone and an offset to apply, replace it with a
  helper that slices each row to the horizontal window:

  ```go
  // clipRow renders one captured row into the visible window starting at column
  // off. boxLine handles the right edge (truncate + "…"); this only drops the
  // left columns, marking the cut with a leading "…" when scrolled.
  func clipRow(ln string, off int) string {
      if off <= 0 {
          return ln
      }
      return ansi.TruncateLeft(ln, off, "…")
  }
  ```

  `renderPreviewBox` and `renderPreviewPane` call `previewContent()` directly and
  map `clipRow(ln, m.previewCol)` over the rows before handing them to
  `renderBox`. `boxLine`'s existing `ansi.Truncate(ln, inner, "…")` clips the
  right and supplies the right-edge `…`, so:
  - offset 0, narrow row → unchanged;
  - offset 0, wide row → right `…` (more content to the right);
  - offset > 0 → left `…` (scrolled), plus right `…` while content remains.

- **`boxLine`, `innerWidth` unchanged.** `boxLine` already clips ANSI-aware and
  resets the pen before the right border, so a clipped, unterminated color can't
  bleed into the frame.

### Scroll state and keys (`internal/tui/model.go`)

- **State:** add `previewCol int` to `Model` — the leftmost visible column, ≥ 0.

- **Keys (normal mode):** `h`/`l` (and `←`/`→` as aliases) pan the preview,
  giving a clean vim split — `j/k`/`↑`/`↓` navigate the list, `h/l`/`←`/`→`
  scroll the preview. This repurposes `h`/`l`, which today are fold
  collapse/expand; fold moves to `z` (next bullet).

  ```go
  case "l", "right":
      m.scrollPreview(previewScrollStep)
      return m, nil
  case "h", "left":
      m.scrollPreview(-previewScrollStep)
      return m, nil
  ```

  with `previewScrollStep = 8` (a small chunk; precise enough, fast enough). With
  the preview hidden or nothing to scroll, these are no-ops.

- **Fold moves to `z`.** Replace the `expand`/`collapse` pair with a single
  `toggleFold` on `z` that flips the current group's `collapsed` state — on a
  repo row it toggles that group; on a session row it toggles the parent group
  and moves the cursor to its header (preserving today's `collapse`-from-session
  nicety). `Enter` on a repo row still toggles fold (`activate` is unchanged), so
  `z` is the explicit key and `Enter` the incidental one.

  ```go
  case "z":
      m.toggleFold()
      return m, nil
  ```

- **Clamp:** `scrollPreview` adjusts `previewCol` and clamps to
  `[0, maxPreviewCol()]`, where `maxPreviewCol` is `max(0, widest captured row −
  inner)`. Both inputs come from current state: the rows from `previewContent()`
  (widest via `lipgloss.Width`), the inner width from the active layout. Factor
  the "preview inner width for the current layout" out of `renderPreviewBox` /
  `renderPreviewPane` into a small `previewInnerWidth()` helper both the renderer
  and the clamp use, so they can't disagree. Clamping in the handler means `→`
  past the end is a no-op and `←` returns immediately.

- **Reset to 0** on any cursor movement or hiding the pane: `moveCursor`, the
  `g`/`G` jumps, and `togglePreview` set `m.previewCol = 0`. It is **not** reset
  on the refresh tick or a `previewMsg`, so a live session's preview doesn't snap
  back to the left while the user is reading a scrolled view.

- **Passthrough:** unchanged — `h`/`l`/`←`/`→` are forwarded to the pinned pane
  there (passthrough is for interacting, not reading), so scroll is normal-mode
  only.

### Footer hint (`internal/tui/view.go`)

Update the footer string so the keys read as the new scheme:

```
j/k move · h/l scroll · z fold · Enter focus · i send · n new · x kill · s sleep · p preview · / filter · r refresh · q quit
```

### Capture comment (`internal/terminal/kitty.go`)

No behavior change. The `Preview` doc comment's rationale ("the view re-wraps to
the preview pane's width") is now false. Reword: wrap markers are omitted; the
view clips each captured line to the box width and pans it horizontally. For the
common Claude case the capture is already physical rows, so clipping renders them
faithfully.

## Target behavior

A 272-col Claude session in a ~223-col preview:

```
Before (reflow)                         After (clip, previewCol = 0)
───────────────                         ────────────────────────────
※ recap: We're walking through the MR    ※ recap: We're walking through the MR …
240712 review findings one by one to     ❯
…                                        ─────────────────────────────────────…
─────────────────────────────────────     ? for shortcuts · ← for agents
──────────────────                      (each source row → exactly one box row)
❯
```

Pressing `l` (or `→`) pans right; rows then begin with `…` and reveal the
previously clipped columns:

```
After (clip, previewCol = 16)
─────────────────────────────
…alking through the MR !240712 review …
…
```

## What does not change

- The capture (`kitty @ get-text … --extent screen --ansi`, no wrap markers).
- `lastNonEmptyLines` — blank lines are still dropped (vertical spacing stays
  compressed; out of scope here).
- Preview sizing/trimming (`previewSize`, `previewReservedRows`, the
  keep-newest-rows trim in `renderPreviewBox`); scroll is purely horizontal.
- Refresh tick, filtering, focus, split/stacked layout choice, passthrough.

## Testing

`internal/tui/view_test.go`:

- **Remove** the wrapping tests, which assert the behavior being deleted:
  `TestPreviewReflowsLongLine`, `TestReflowCarriesColorAcrossWraps`,
  `TestReflowCarriesColonDelimitedSGR`, `TestReflowLeavesPlainTextUnstyled`,
  `TestPreviewBoxStaysWithinBudgetWhenReflowed`.
- **Add — clip:** a captured line wider than the box's inner width renders as
  exactly one content row that ends in `…` and does not spill onto a second row
  (the inverse of the old `TestPreviewReflowsLongLine`).
- **Add — styled clip:** a clipped colored row keeps its color and resets before
  the border (no bleed), proving `ansi` slicing carries the style.
- **Add — budget:** a capture of many rows stays within `previewSize` (still
  holds because each row maps to one row).

`internal/tui/model_test.go`:

- **Add — scroll:** `l` (and `→`) increases `previewCol` by the step and the
  rendered rows gain a leading `…`; `h` (and `←`) decreases it; `previewCol`
  clamps at 0 on the low end and at `maxPreviewCol` on the high end (`l` past the
  widest row is a no-op).
- **Add — reset:** moving the cursor to a different session, and `g`/`G`, reset
  `previewCol` to 0; a `previewMsg`/refresh of the same session does **not**.
- **Add — passthrough:** `h`/`l`/`←`/`→` in passthrough are forwarded to the
  pane, not consumed as scroll.
- **Fold — `z`:** `z` on a repo row toggles that group's `collapsed` state; `z`
  on a session row collapses the parent and moves the cursor to its header. Adapt
  the existing `expand`/`collapse` key tests to drive `z` (and assert `h`/`l` no
  longer fold).

`internal/terminal/kitty_test.go`:

- Fix the stale comment at line 76 (the capture still hands back logical lines
  with no wrap markers; the view now clips/pans rather than re-wraps). Assertions
  unchanged.
