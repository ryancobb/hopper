# Live last-message rows with Claude session names

**Date:** 2026-06-11
**Status:** approved
**Scope:** `internal/transcript`, `internal/source`, `internal/claude`, `internal/tui/view.go`, and their tests. No changes to the refresh loop, tree grouping, or terminal backends.

## Problem

Session rows are labeled with the *first* user prompt of the session, extracted
once from the transcript JSONL and cached. Two shortcomings:

- The first prompt goes stale: a session that started as "fix the flaky test"
  may be doing something entirely different an hour later.
- The list says nothing about what each session is doing *right now*; the user
  has to focus a session to find out.

Claude Code already writes `ai-title` entries into the transcript
(`{"type":"ai-title","aiTitle":"…","sessionId":"…"}`), re-titling the session as
the conversation evolves. It also records every assistant message. Both are
available to hopper for free.

## Goal

Rows identify sessions by Claude's own session name and show, live, what each
session last said — refreshed on the existing 1 Hz tick.

## Target rendering

```
▾ hopper                                    ● 1 working · ⚠ 1 blocked
  ● working   Stream last message in TUI rows                       2m
              ↳ Now updating the view renderer to…
  ⚠ blocked   Fix flaky geometry test                               8m
              ↳ permission: Bash(rm -rf …)
```

## Design

### Session name

- The row label is the **latest `ai-title` entry** in the session's transcript
  (last occurrence wins; titles change over the session's life, so the label may
  change between ticks).
- Fallbacks, in order: first user prompt (current behavior), then session ID
  prefix (current behavior).
- Same 40-rune truncation as today.

### Live last-message line

- Every session row gets a dimmed continuation line showing the **most recent
  assistant message containing text**. Tool-call-only assistant entries are
  skipped.
- The text is normalized for a single line: first non-empty line of the message,
  whitespace collapsed, truncated to the available width with `…`, indented to
  the name column with the `↳` marker (same geometry as the blocked-reason
  line).
- **One continuation line per session, reason wins:** blocked sessions keep
  their existing `WaitingFor` line *instead of* the last message. Non-blocked
  sessions with no extractable assistant text get no continuation line.
- The line refreshes on the existing 1 Hz poll; no new refresh machinery.
- Continuation lines remain non-rows for cursor purposes, as today.

### Transcript reading

`internal/transcript` grows from a one-shot first-prompt cache into a
per-session reader returning `{Title, LastMessage}`:

- **Size-change cache:** each tick costs one `stat` per session; the transcript
  is re-read only when the file size differs from the last read. Idle sessions
  are nearly free.
- **Bounded tail read:** on change, read only the last 256 KB of the file and
  scan it for the latest `ai-title`, latest assistant text, and (for fallback)
  whether a first prompt is needed. Both hot values live near the end of an
  active transcript. A partial first line in the tail window is discarded.
- **Title seeding:** if the tail contains no `ai-title` and no title is cached
  for the session (long transcript opened mid-stream), do one full scan to seed
  it; subsequent updates come from tail reads. The first-prompt fallback keeps
  its existing full-scan path, which already runs at most once per session.
- Cache keyed by session ID as today; cached values survive ticks where the
  file is unchanged.

### Data flow

- `source.Session` gains `LastMessage string`.
- `internal/claude` populates `Name` from the transcript title (with fallbacks)
  and `LastMessage` from the transcript reader.
- `internal/tui/view.go` extends the blocked-reason continuation rendering to
  all sessions, choosing `WaitingFor` over `LastMessage` when both exist.

### Unchanged

Refresh interval and load flow (`model.go`), grouping/sort (`tree.go`), repo
headers, selection, keybindings, footer, filter, width degradation behavior.

## Testing

- Transcript: latest `ai-title` wins over earlier ones; tool-call-only
  assistant entries are skipped when finding the last message; fallback to
  first prompt when no `ai-title`; multi-line assistant text collapses to one
  normalized line; size-change cache returns cached values for an unchanged
  file and re-reads on growth; tail read handles a partial leading line.
- View: continuation line shows the last message for working/idle sessions,
  shows the blocked reason (not the last message) for blocked sessions, is
  absent when neither exists, and truncates to width.
