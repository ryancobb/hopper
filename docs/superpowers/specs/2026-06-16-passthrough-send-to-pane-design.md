# Passthrough: send keystrokes to a session's pane

**Date:** 2026-06-16
**Status:** approved
**Scope:** `internal/terminal` (interface + kitty backend) and `internal/tui`
(`model.go`, `view.go`) plus tests. No changes to `internal/source`,
`internal/claude`, `internal/session`, `internal/transcript`, or
`internal/status`.

## Problem

When a session is blocked on a permission prompt, or just needs a nudge, the
only way to respond today is to jump to its terminal pane (`o`/Enter focus) and
type there. hopper can read a session's pane (preview) but cannot write to it,
so every interaction means leaving hopper for the pane and coming back.

## Goal

Let the user answer permission prompts and send free-form text to a session
**without leaving hopper**. The preview already shows the live pane, including
any numbered permission menu; the missing half is a way to type back into it.

## Decisions

- **One primitive, full passthrough.** Rather than parse prompts or offer
  semantic "approve/deny" presets, hopper relays raw keystrokes to the selected
  session's pane and lets the pane interpret them. The preview is the human's
  view of the menu; the human reads the option and presses the key. No
  prompt-parsing, no `Prompt` model, nothing provider-specific.
- **Auto-submit.** Keys go through live as typed; Enter sends `\r`, so the pane
  acts immediately. No staging buffer.
- **No confirmation.** Passthrough is an explicit, clearly-indicated mode; the
  mode banner and the live preview are the confirmation. Single keypress fires.
- **Preview is the cockpit.** Entering passthrough forces the preview on and
  re-captures after each forwarded key, so the pane feels live.
- **Provider-neutral boundary holds for free.** Sending raw keys carries no
  Claude-specific logic, so `source`/`claude` are untouched.

## Design

### Terminal capability

`internal/terminal` gains a capability and an interface method:

```go
const (
    CapFocus Capability = 1 << iota
    CapPreview
    CapSendText // write keystrokes/text to a session's pane
)

type Terminal interface {
    // ...existing...
    // SendText transmits data to the pane verbatim, including control bytes
    // and escape sequences. Callers pre-encode keys to bytes; the backend must
    // not re-interpret or alter them.
    SendText(ctx context.Context, h Handle, data string) error
}
```

- `none` returns `ErrUnsupported` and does not advertise `CapSendText`.
- `Kitty` advertises `CapFocus | CapPreview | CapSendText` and implements
  `SendText` via `kitty @ send-text` against the PID-located window (same
  `Locate` path as `Focus`/`Preview`). To avoid shell/escape mangling, the
  bytes are piped on stdin (`kitty @ send-text --match id:N --stdin`). If
  stdin escaping misbehaves in practice, `kitty @ send-key` is the fallback;
  the implementation verifies against a real kitty before settling.

### Key translation

A pure function in `internal/tui` maps a Bubble Tea key event to the bytes to
send, so it is unit-testable without a terminal:

```go
// keyToBytes encodes a key event as the bytes to send to a pane. The second
// return is false for unmapped keys, which the caller drops. The exit chord
// (Ctrl-]) never reaches here: the caller intercepts it before translating.
func keyToBytes(msg tea.KeyMsg) (string, bool)
```

Mapping:

- Printable runes (`msg.Runes`) → those UTF-8 bytes as typed.
- Enter → `\r`; Tab → `\t`; Backspace → `\x7f`; Esc → `\x1b`.
- Arrows → `\x1b[A` / `B` / `C` / `D`; Home/End/PgUp/PgDn → their CSI sequences.
- Ctrl-letter → the control byte (e.g. Ctrl-C → `0x03`).
- Unmapped keys → dropped (`false`), never guessed.

`Esc` is forwarded (it answers "No, tell Claude what to do differently (esc)"
and cancels menus), so it cannot be the exit key.

### TUI mode

`Model` gains passthrough state:

```go
passthrough   bool
passthroughID string // session ID the keys are pinned to
```

- **Enter** (`i` on a selected session row): if the row is a `RowSession` and
  the terminal has `CapSendText`, set `passthrough = true` and pin
  `passthroughID` to the session ID; force `showPreview = true` and capture.
  Otherwise no-op with a status message
  (`"send unavailable in this terminal"` when the capability is absent),
  mirroring how `focusSelected` gates today.
- **While active**, `handleKey` routes to a passthrough handler before the
  normal keymap:
  - `Ctrl-]` → exit passthrough (clear state); does not reach the pane.
  - any other key → `keyToBytes`; if mapped, `SendText` to the pinned session's
    pane, then re-capture the preview so the result shows immediately. If
    unmapped, ignore.
- **Target is pinned by ID**, looked up at send time. A `loadedMsg` that
  reorders rows cannot redirect keystrokes to a different session; if the
  pinned session has disappeared, the next send hits `ErrNotFound` (below).

A new message carries send results:

```go
type sendMsg struct{ err error }
```

- `nil` / `ErrNotFound`-free success → clear `statusMsg`, trigger a preview
  re-capture.
- `ErrNotFound` → the pane is gone; show it in `statusMsg` and exit passthrough.
- other error → show in `statusMsg`, stay in passthrough (transient).

### View

`view.go` shows the mode prominently in the footer while passthrough is active:

```
PASSTHROUGH → fix auth bug        Ctrl-] to exit
```

This replaces the normal footer keybind hint for the duration, so the relayed
keys and the exit chord are always visible. The session list and preview render
as usual; only the footer changes.

## Target behavior

```
▾ hopper
    ● working   add tests
  ❯ ○ blocked   fix auth bug        ← selected, press i
```

After `i`, the preview shows the live prompt and the footer flips to
`PASSTHROUGH → fix auth bug   Ctrl-] to exit`. Pressing `2` sends `2` to the
pane (selecting "Yes, and don't ask again"); the preview re-captures and shows
the prompt cleared. `Ctrl-]` returns to normal navigation.

## What does not change

- `source`, `claude`, `session`, `transcript`, `status` — no parsing, no
  prompt model, no new provider concepts.
- Existing keys (`j/k`, `h/l`, `g/G`, Enter, `o`, `p`, `r`, `/`, `q`) outside
  passthrough. Inside passthrough they are forwarded to the pane, so the cursor
  is pinned until `Ctrl-]`.
- Preview capture mechanics, refresh tick, filtering, focus.

## Edge cases

- Entering passthrough on a repo row or with no selection: no-op.
- Terminal without `CapSendText` (`none`): `i` shows the unavailable message;
  passthrough never starts.
- Pane vanishes mid-passthrough: first send returns `ErrNotFound`, status shows
  it, mode exits.
- Filter mode (`/`) and passthrough are mutually exclusive; `/` is forwarded as
  a keystroke while in passthrough, not interpreted by hopper.
- `q`/`Ctrl-C` while in passthrough are forwarded to the pane, not hopper —
  exit with `Ctrl-]` first to quit. (Acceptable: the banner shows the exit
  chord; documented in the keys help.)

## Testing

- `keyToBytes` table tests: printable runes, Enter/Tab/Backspace/Esc, all four
  arrows, representative Ctrl-combos, and unmapped keys returning `false`.
- Model tests with a fake `Terminal` capturing `SendText` calls: entering
  passthrough requires a session row and `CapSendText`; keys are translated and
  forwarded to the pinned session; the target stays pinned by ID across a
  `loadedMsg` reorder; `Ctrl-]` exits without sending; `ErrNotFound` exits and
  reports.
- Kitty `SendText` test asserting the `@ send-text` argv / stdin payload,
  alongside the existing kitty tests.
- View test asserting the passthrough footer banner renders with the session
  name and exit hint.
