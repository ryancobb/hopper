# Session lifecycle keys: kill, new, sleep

**Date:** 2026-06-16
**Status:** approved
**Scope:** `internal/terminal` (interface + kitty backend, new `Launch`),
`internal/tui` (`model.go`, `view.go`, `class.go`), and tests. No changes to
`internal/source`, `internal/claude`, `internal/session`,
`internal/transcript`, or `internal/status`.

## Problem

hopper today is a viewer that can navigate, focus, preview, and (newly) pass
keystrokes to a session's pane. Three common chores still pull the user out of
hopper or make them wait:

- **Killing a stuck or finished session** means switching to its pane and
  quitting Claude there.
- **Starting a session in a repo** means leaving hopper, opening a terminal in
  the right directory, and running `claude`. hopper already knows which repos
  have no live session, but can't act on it.
- **A finished session keeps nagging.** A just-idle session shows a yellow
  "recently idle" stripe for five minutes (`classRecentIdle`); once the user
  has seen the result there is no way to quiet it early.

## Goal

Add three keymaps that let the user manage session lifecycle and attention
without leaving hopper:

- `x` — **kill** the selected session (graceful, confirmed).
- `n` — start a **new** session in the selected repo.
- `s` — **sleep** (snooze) a recently-idle session so it stops drawing
  attention.

## Decisions

- **Graceful kill, confirmed.** `x` sends `SIGTERM` (not `SIGKILL`) so Claude
  can flush state, behind a `y`/`n` footer confirm. This is hopper's first
  destructive action; everything else observes or relays. The confirm, not the
  key choice, is the safeguard. `SIGKILL` is a later fallback if `SIGTERM`
  proves unreliable, not part of v1.
- **Kill needs no terminal capability.** It is a local-PID signal, independent
  of the terminal backend, so it works regardless of `Capabilities()`. It is
  gated only on the cursor being a session row with a non-zero PID.
- **New launches in the selected repo root.** `n` reuses hopper's existing repo
  grouping: the working directory is the row's repo root. No path prompt in v1.
- **New stays in hopper but follows the session.** kitty keeps focus on hopper
  (`--keep-focus`), and hopper moves its own cursor onto the new session once it
  appears on a refresh. The new session is identified as the one in the launch
  cwd whose ID was absent at launch time; the wait is bounded by a window
  (`launchAdoptWindow`) so a session that never appears does not hijack the
  cursor later. Adoption defers while a filter or kill confirm is active (moving
  the cursor then would anchor a filtered-out row or shift the highlight off the
  confirm's target), and a failed launch clears the pending state immediately.
- **New is a terminal capability.** Spawning a pane is backend-specific, so it
  is gated by a new `CapLaunch`, mirroring `CapFocus`/`CapPreview`/
  `CapSendText`. Backends without it show an unavailable message.
- **Sleep is attention-only.** It changes nothing in the source, status, or
  process; it only demotes the render-time `classRecentIdle` to `classIdle`
  early. It is the manual equivalent of waiting out the five-minute window.
- **Sleep snoozes, it does not mute.** Sleep is pinned to the acknowledged
  idle period (the session's `UpdatedAt` at sleep time). If the session does
  more work and finishes again, `UpdatedAt` advances, the snapshot no longer
  matches, and the yellow stripe returns. A genuinely new result still earns
  attention.
- **Self-updating list, no special bookkeeping.** A killed process goes dead
  and the loader drops it on the next refresh; a new session's JSON appears and
  the loader picks it up on the next refresh. Both reuse the existing one-second
  tick rather than mutating `groups`/`rows` directly.

## Design

### Terminal capability: Launch

`internal/terminal` gains a capability and an interface method:

```go
const (
    CapFocus Capability = 1 << iota
    CapPreview
    CapSendText
    CapLaunch // spawn a new session pane in a working directory
)

type Terminal interface {
    // ...existing...
    // Launch starts a new agent session (runs `claude`) in cwd, in a new
    // pane/tab. The backend chooses placement; the new session is discovered
    // on the next source refresh once it writes its session file.
    Launch(ctx context.Context, cwd string) error
}
```

- `none` returns `ErrUnsupported` and does not advertise `CapLaunch`.
- `Kitty` advertises `CapFocus | CapPreview | CapSendText | CapLaunch` and
  implements `Launch` via `kitty @ launch --type=tab --keep-focus --cwd=<cwd> claude`
  (same `kitty @` transport as the other verbs). `--keep-focus` leaves the focus
  on hopper rather than switching to the new tab, so the user stays in hopper;
  the new session surfaces on the next refresh. The command is `claude`
  on `PATH`; that assumption matches the Claude Code source and is not
  configurable in v1. `kitty @ launch` prints the new window id on stdout,
  which is ignored.

### Kill: process signal seam

Killing is a local-PID `SIGTERM`. To keep it testable without signalling a real
process, `Model` holds an injectable function set by `New`:

```go
// in Model
kill func(pid int) error

// default, in New:
func defaultKill(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
```

`New`'s signature is unchanged; it sets `kill = defaultKill`. Tests assign a
fake to capture the PID and simulate failure. (`internal/session` already does
PID liveness with `syscall.Kill(pid, 0)`; the default here is the natural
sibling, kept in the tui package since the kill target is a provider-neutral
`source.Session` PID.)

### TUI state

`Model` gains:

```go
confirming      bool   // a kill confirm is pending
pendingKillPID  int    // PID to signal on confirm
pendingKillName string // session name shown in the prompt

slept map[string]time.Time // session ID -> acknowledged UpdatedAt (snooze)
```

`slept` is initialized in `New` like `collapsed`. It is in-memory only; restart
clears it.

Two messages carry async results, mirroring `focusMsg`/`sendMsg`:

```go
type killMsg struct{ err error }
type launchMsg struct{ err error }
```

### Key handling (`handleKey`)

Routing order in `handleKey`: passthrough first (unchanged), then **the kill
confirm**, then filtering (unchanged), then the normal keymap. The confirm is
checked before the normal keymap so `x`'s `y`/`n` answer is not interpreted as
navigation.

- **Kill confirm active** (`m.confirming`):
  - `y` → fire `killCmd(m.pendingKillPID)`, clear confirm state.
  - `n` / `esc` / any other key → cancel, clear confirm state, no signal.
- **`x`** (normal mode): if the cursor is a `RowSession` with a non-zero PID,
  set `confirming = true`, snapshot `pendingKillPID` and `pendingKillName`.
  Otherwise no-op (repo rows, empty selection, PID 0).
- **`n`** (normal mode): resolve the launch cwd, then if the terminal has
  `CapLaunch` fire `launchCmd(cwd)`, else set
  `statusMsg = "new session unavailable in this terminal"`. cwd resolution:
  - `RowRepo` with non-empty `Group.Key` → `Group.Key` (repo root).
  - `RowSession` → `Item.Repo.Root` if non-empty, else `Item.Session.CWD`
    (no-repo bucket fallback).
  - `RowRepo` with `Key == ""` (the no-repo group header) → no-op with a status
    message; there is no single directory.
- **`s`** (normal mode): if the cursor is a `RowSession` whose session is
  `status.Idle`, set `slept[id] = session.UpdatedAt`. Otherwise no-op (sleeping
  a working/blocked session or a repo row does nothing).

`killCmd` and `launchCmd` follow the existing async pattern: a timeout-bounded
`tea.Cmd` returning `killMsg`/`launchMsg`. On error each sets `statusMsg`; on
success each clears it and lets the refresh tick reconcile the list (a killed
PID disappears, a new session appears). No proactive reload.

### Sleep classification (`class.go`)

`classify` takes a snooze flag so a slept session renders as plain idle:

```go
func classify(k status.Kind, age time.Duration, slept bool) displayClass {
    switch k {
    case status.Blocked:
        return classBlocked
    case status.Working:
        return classWorking
    case status.Idle:
        if age < recentIdleWindow && !slept {
            return classRecentIdle
        }
        return classIdle
    default:
        return classUnknown
    }
}
```

The row renderer in `view.go` already has the `Item` (session ID + `UpdatedAt`).
It computes:

```go
slept := !m.slept[id].IsZero() && m.slept[id].Equal(session.UpdatedAt)
```

so the snooze applies only to the exact idle period that was acknowledged; once
`UpdatedAt` advances the flag clears itself (snooze, not mute). Stale entries
(sessions gone, or `UpdatedAt` advanced) are pruned in `applyLoaded` to bound
the map, but correctness does not depend on pruning since a non-matching entry
never suppresses.

### View (`view.go`)

- Footer help gains `x kill · n new · s sleep` alongside the existing hints.
- While a kill confirm is pending, the footer is replaced by a styled banner
  modeled on the passthrough banner: a red ` KILL ` tag, the target session's
  name, and a `y to confirm · any other key cancels` hint. This uses the same
  footer-takeover pattern as the filter prompt and passthrough banner, but its
  red tag makes the destructive prompt unmistakable.

## Target behavior

```
▾ hopper
  ❯ ○ idle      add tests        ← yellow stripe (recent idle); press s → dims now
    ⚠ blocked   fix auth bug
▾ acme-api
    (no sessions)                ← repo row; press n → new claude tab here
```

- `x` on a session row → footer shows the red ` KILL ` banner with the session
  name and `y to confirm · any other key cancels`; `y` sends `SIGTERM` and the
  row vanishes on the next refresh; any other key cancels.
- `n` on the `acme-api` repo row → a new kitty tab opens running `claude` in
  the repo root without stealing focus (the user stays in hopper); when the
  session appears on the next refresh, hopper expands the repo group and moves
  the cursor onto it.
- `s` on the recently-idle "add tests" row → the yellow stripe drops to the
  dim idle look immediately. If that session works again and finishes, the
  stripe returns.

## What does not change

- `source`, `claude`, `session`, `transcript`, `status` — no new status kinds,
  no provider concepts. "Recently idle" stays a render-time split.
- Existing keys (`j/k`, `h/l`, `g/G`, Enter, `i`, `o`, `p`, `r`, `/`, `q`).
- Preview/focus/passthrough mechanics, the refresh and spinner ticks, filtering.
- The five-minute `recentIdleWindow` auto-fade; sleep only triggers it early.

## Edge cases

- `x` on a repo row, empty selection, or a session with PID 0: no-op.
- Kill confirm pending when a `loadedMsg` reorders rows: the confirm targets the
  snapshotted PID (in `pendingKill`), not the cursor, so it cannot kill the wrong
  session. A refresh in which the target session has ended dismisses the confirm,
  so `y` cannot signal a PID the OS may have recycled.
- `killCmd` failure (e.g. process already gone, `EPERM`): `statusMsg` shows the
  error; nothing else changes (a gone process drops on refresh anyway).
- `n` on a backend without `CapLaunch` (`none`): unavailable status message; no
  launch.
- `n` on the no-repo group header (`Key == ""`): no-op with a status message.
- `launchCmd` failure: `statusMsg` shows it; no row appears.
- `s` on a non-idle row or repo row: no-op. `s` on an already-dim idle row
  (age ≥ window): records the snapshot harmlessly, no visible change.
- These actions are unavailable inside passthrough and filter modes (those modes
  capture keys first), consistent with the existing routing.

## Testing

- **Terminal:** kitty `Launch` test asserting the `@ launch --type=tab
  --keep-focus --cwd=... claude` argv; `none` returns `ErrUnsupported` and omits
  `CapLaunch`.
- **Kill:** model tests with a fake `kill` func — `x` on a session row opens the
  confirm; `y` calls `kill` with the row's PID; `n`/`esc` cancels without
  calling it; the confirm targets the snapshotted PID across a `loadedMsg`
  reorder; `x` on a repo row / PID 0 is a no-op; `killMsg{err}` sets `statusMsg`.
- **New:** model tests with a fake `Terminal` capturing `Launch` — `n` on a repo
  row launches in `Group.Key`; on a session row uses `Repo.Root`, falling back
  to `Session.CWD` when the repo root is empty; the no-repo header and a backend
  without `CapLaunch` no-op with a status message; `launchMsg{err}` sets
  `statusMsg`. Adoption: after `n`, a `loadedMsg` introducing a new session in
  the launch cwd moves the cursor onto it; a refresh with only the pre-existing
  session keeps waiting; past the deadline the pending launch clears.
- **Sleep:** `classify` table gains the `slept` axis (slept idle within the
  window returns `classIdle`, not `classRecentIdle`). Model test: `s` records
  `slept[id] = UpdatedAt`; rendering suppresses the stripe; after `UpdatedAt`
  advances the suppression lapses (snooze); `applyLoaded` prunes stale entries.
- **View:** footer shows the three new hints; the kill confirm renders the
  ` KILL ` banner with the session name and the `y`/cancel hint in place of the
  footer.
