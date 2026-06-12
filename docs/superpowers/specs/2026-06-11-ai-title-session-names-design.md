# Session names from Claude's ai-title

**Date:** 2026-06-11
**Status:** approved (scope reduced from an earlier draft that also streamed the
last assistant message; that part was dropped)
**Scope:** `internal/transcript`, `internal/claude`, and their tests. No TUI or
source-model changes.

## Problem

Session rows are labeled with the *first* user prompt of the session, extracted
once from the transcript JSONL and cached forever. The first prompt goes stale:
a session that started as "fix the flaky test" may be doing something entirely
different an hour later.

Claude Code already writes `ai-title` entries into the transcript
(`{"type":"ai-title","aiTitle":"…","sessionId":"…"}`), re-titling the session
as the conversation evolves. That is Claude's own name for the session, and it
is available to hopper for free.

## Goal

Rows identify sessions by Claude's current session name, picked up live as
Claude re-titles them.

## Design

### Session name

- The row label is the **latest `ai-title` entry** in the session's transcript
  (last occurrence wins; the label may change between ticks as Claude
  re-titles).
- Fallbacks, in order: first user prompt (current behavior), then a session-ID
  prefix.
- Same 40-rune truncation as today.

### Transcript reading

`internal/transcript`'s one-shot first-prompt `Namer` becomes a per-session
`Reader` returning `{Title, FirstPrompt}`:

- **Size-change cache:** each tick costs one `stat` per session; the transcript
  is re-read only when the file size differs from the last read. Idle sessions
  are nearly free, and a title that has been found is refreshed (not frozen)
  when the file grows.
- **Bounded tail read:** on change, read only the last 256 KB of the file and
  scan it for the latest `ai-title` (and, when the window covers the whole
  file, the first prompt). New titles are appended, so they live near the end
  of an active transcript. A partial first line in the tail window is
  discarded.
- **Title seeding:** if the tail contains no `ai-title` and no title is cached
  for the session (long transcript opened mid-stream), do one full scan to seed
  the title and first-prompt fallback; subsequent updates come from tail reads.
- Cache keyed by session ID as today.

### Data flow

- `internal/claude` populates `source.Session.Name` from the transcript title,
  falling back to the first prompt, then a session-ID prefix.
- `source.Session` and the TUI are otherwise unchanged.

### Unchanged

The TUI entirely (rows, blocked-reason continuation line, refresh loop),
grouping/sort, `source.Session` fields, keybindings, footer, filter.

## Testing

- Transcript: latest `ai-title` wins over earlier ones; fallback to first
  prompt when no `ai-title`; first-prompt rules preserved (skip meta, `<`, `/`
  prefixes; string and array content forms); 40-rune truncation; size-change
  cache returns cached values for an unchanged file and re-reads on growth;
  tail read discards a partial leading line and triggers a one-time full scan
  to seed a title not present in the tail.
- Claude source: `Name` mapping and the title → first prompt → ID-prefix
  fallback chain.
