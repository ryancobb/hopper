# Live Last-Message Rows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Label session rows with Claude's own session title (latest `ai-title` transcript entry) and show each session's latest assistant message on a live continuation line, refreshed by the existing 1 Hz poll.

**Architecture:** `internal/transcript` is rewritten from a one-shot first-prompt `Namer` into a `Reader` that returns `{Title, FirstPrompt, LastMessage}` per session, re-reading a transcript only when its file size changes and reading only a bounded tail once the title is known. `internal/claude` maps that into `source.Session` (new `LastMessage` field, `Name` = title → first prompt → id prefix). `internal/tui/view.go` generalizes the existing blocked-reason continuation line to all sessions, with the blocked reason taking precedence.

**Tech Stack:** Go 1.24, bubbletea/lipgloss TUI, stdlib only (no new dependencies). Spec: `docs/superpowers/specs/2026-06-11-live-last-message-design.md`.

---

## Background for the implementer

- Claude Code writes one JSONL file per session at `~/.claude/projects/<munged-project-path>/<session-id>.jsonl`. Relevant line shapes:
  - `{"type":"ai-title","aiTitle":"Stream last message...","sessionId":"..."}` — appended repeatedly as Claude re-titles the session; **the last one is the current title**.
  - `{"type":"assistant","message":{"content":[{"type":"text","text":"..."},{"type":"tool_use",...}]}}` — assistant turns; tool-call-only entries have no `text` parts.
  - `{"type":"user","isMeta":false,"message":{"content":"..."}}` — user prompts; `content` is either a string or an array of `{type,text}` parts.
- All tests run with `go test ./...` from the repo root. Format with `gofmt -w` before committing.
- The TUI polls sources every second (`internal/tui/model.go`), so freshness comes for free; the transcript layer just has to be cheap per call.

### File map

| File | Change |
|---|---|
| `internal/transcript/transcript.go` | Rewrite: `Namer` → `Reader` with `Info(sessionID) Info` |
| `internal/transcript/transcript_test.go` | New `Reader` tests; old `Namer` tests deleted in Task 3 |
| `internal/source/source.go` | Add `Session.LastMessage` field |
| `internal/claude/claude.go` | Use `Reader`; `displayName` fallback chain; populate `LastMessage` |
| `internal/claude/claude_test.go` | Replace `fakeNamer` with `fakeReader`; add fallback tests |
| `internal/tui/view.go` | Continuation line for all sessions; reason wins |
| `internal/tui/view_test.go` | New continuation-line tests |

---

### Task 1: transcript.Reader — full-scan parsing

The new `Reader` lives alongside the old `Namer` until Task 3 switches the consumer over. This task implements `Info()` as a full scan every call; caching and tail reads come in Task 2.

**Files:**
- Modify: `internal/transcript/transcript.go`
- Test: `internal/transcript/transcript_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/transcript/transcript_test.go` (keep the existing `Namer` tests for now):

```go
type fakeFile struct{ *strings.Reader }

func (fakeFile) Close() error { return nil }

// newTestReader serves *body as session "sid"'s transcript; swap *body between
// calls to simulate the file growing. opens counts file opens.
func newTestReader(body *string) (*Reader, *int) {
	opens := 0
	r := NewReader("/proj")
	r.glob = func(string) ([]string, error) { return []string{"/proj/abc/sid.jsonl"}, nil }
	r.size = func(string) (int64, error) { return int64(len(*body)), nil }
	r.open = func(string) (io.ReadSeekCloser, error) {
		opens++
		return fakeFile{strings.NewReader(*body)}, nil
	}
	return r, &opens
}

func TestInfoTitleLastWins(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"ai-title","aiTitle":"first title"}`,
		`{"type":"user","message":{"content":"the prompt"}}`,
		`{"type":"ai-title","aiTitle":"second title"}`,
	}, "\n")
	r, _ := newTestReader(&body)
	got := r.Info("sid")
	if got.Title != "second title" {
		t.Fatalf("Title = %q, want %q", got.Title, "second title")
	}
	if got.FirstPrompt != "the prompt" {
		t.Fatalf("FirstPrompt = %q, want %q", got.FirstPrompt, "the prompt")
	}
}

func TestInfoLastAssistantTextWins(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"first answer"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"second answer"}]}}`,
	}, "\n")
	r, _ := newTestReader(&body)
	if got := r.Info("sid"); got.LastMessage != "second answer" {
		t.Fatalf("LastMessage = %q, want %q", got.LastMessage, "second answer")
	}
}

func TestInfoSkipsToolOnlyAssistant(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"real text"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
	}, "\n")
	r, _ := newTestReader(&body)
	if got := r.Info("sid"); got.LastMessage != "real text" {
		t.Fatalf("LastMessage = %q, want %q", got.LastMessage, "real text")
	}
}

func TestInfoFirstPromptRules(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"user","isMeta":true,"message":{"content":"<meta>"}}`,
		`{"type":"user","message":{"content":"/clear"}}`,
		`{"type":"user","message":{"content":"lets work on the billing bug"}}`,
		`{"type":"user","message":{"content":"second message"}}`,
	}, "\n")
	r, _ := newTestReader(&body)
	if got := r.Info("sid"); got.FirstPrompt != "lets work on the billing bug" {
		t.Fatalf("FirstPrompt = %q", got.FirstPrompt)
	}
}

func TestInfoNormalizesLastMessageToOneLine(t *testing.T) {
	body := `{"type":"assistant","message":{"content":[{"type":"text","text":"\n\nFixed   the\tbug.\nDetails follow."}]}}`
	r, _ := newTestReader(&body)
	if got := r.Info("sid"); got.LastMessage != "Fixed the bug." {
		t.Fatalf("LastMessage = %q, want %q", got.LastMessage, "Fixed the bug.")
	}
}

func TestInfoTruncatesTitle(t *testing.T) {
	long := strings.Repeat("a", 100)
	body := `{"type":"ai-title","aiTitle":"` + long + `"}`
	r, _ := newTestReader(&body)
	got := r.Info("sid")
	if len([]rune(got.Title)) != 40 || !strings.HasSuffix(got.Title, "…") {
		t.Fatalf("Title len=%d %q", len([]rune(got.Title)), got.Title)
	}
}

func TestInfoNoTranscript(t *testing.T) {
	r := NewReader("/proj")
	r.glob = func(string) ([]string, error) { return nil, nil }
	if got := r.Info("sid"); got != (Info{}) {
		t.Fatalf("want zero Info, got %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/transcript/ -run TestInfo -v`
Expected: compile error — `Reader`, `NewReader`, `Info` undefined.

- [ ] **Step 3: Implement Reader with full-scan Info**

Add to `internal/transcript/transcript.go` (the `Namer`, `firstPromptText`, `extractText`, `truncate` symbols stay untouched; `bytes` joins the imports):

```go
// Info is the display data derived from a session's transcript.
type Info struct {
	Title       string // latest ai-title entry, "" if none yet
	FirstPrompt string // first real user prompt, for fallback naming
	LastMessage string // latest assistant message text, normalized to one line
}

const (
	defaultTailBytes = 256 * 1024
	nameMax          = 40
	lastMessageMax   = 200
)

// Reader derives Infos from transcript JSONL files.
type Reader struct {
	projectsDir string
	tailBytes   int64

	glob func(pattern string) ([]string, error)
	open func(path string) (io.ReadSeekCloser, error)
	size func(path string) (int64, error)

	mu sync.Mutex
}

// NewReader builds a Reader over ~/.claude/projects.
func NewReader(projectsDir string) *Reader {
	return &Reader{
		projectsDir: projectsDir,
		tailBytes:   defaultTailBytes,
		glob:        filepath.Glob,
		open:        func(p string) (io.ReadSeekCloser, error) { return os.Open(p) },
		size: func(p string) (int64, error) {
			fi, err := os.Stat(p)
			if err != nil {
				return 0, err
			}
			return fi.Size(), nil
		},
	}
}

// Info returns the session's display data.
func (r *Reader) Info(sessionID string) Info {
	r.mu.Lock()
	defer r.mu.Unlock()

	matches, err := r.glob(filepath.Join(r.projectsDir, "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return Info{}
	}
	res, ok := r.scanFrom(matches[0], 0)
	if !ok {
		return Info{}
	}
	return Info{
		Title:       truncate(res.title, nameMax),
		FirstPrompt: truncate(res.firstPrompt, nameMax),
		LastMessage: truncate(res.last, lastMessageMax),
	}
}

// result accumulates what one scan pass saw.
type result struct {
	title, firstPrompt, last string
}

// scanFrom parses transcript lines starting at offset; a partial first line at
// a non-zero offset is discarded.
func (r *Reader) scanFrom(path string, offset int64) (result, bool) {
	f, err := r.open(path)
	if err != nil {
		return result{}, false
	}
	defer func() { _ = f.Close() }()

	var res result
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return result{}, false
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return result{}, false
		}
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			processLine(line, &res)
		}
		return res, true
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		processLine(sc.Bytes(), &res)
	}
	return res, true
}

// processLine folds one JSONL line into res: ai-titles and assistant texts
// last-win, the first real user prompt sticks.
func processLine(line []byte, res *result) {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return
	}
	switch e.Type {
	case "ai-title":
		if t := strings.TrimSpace(e.AITitle); t != "" {
			res.title = t
		}
	case "assistant":
		if t := oneLine(extractText(e.Message.Content)); t != "" {
			res.last = t
		}
	case "user":
		if res.firstPrompt != "" || e.IsMeta {
			return
		}
		t := strings.TrimSpace(extractText(e.Message.Content))
		if t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "/") {
			return
		}
		res.firstPrompt = t
	}
}

// oneLine reduces s to its first non-empty line with whitespace collapsed.
func oneLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if fields := strings.Fields(ln); len(fields) > 0 {
			return strings.Join(fields, " ")
		}
	}
	return ""
}
```

Also add the `AITitle` field to the existing `entry` struct:

```go
type entry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	AITitle string `json:"aiTitle"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}
```

Note: `scanFrom`'s `offset > 0` branch is exercised by Task 2's tests; it is included here so the function does not change shape between tasks.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/transcript/ -v`
Expected: all PASS (new `TestInfo*` plus the old `TestName*`).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/transcript/
git add internal/transcript/
git commit -m "feat(transcript): Reader deriving title, first prompt, last message"
```

---

### Task 2: transcript.Reader — size-change cache, tail reads, title seeding

**Files:**
- Modify: `internal/transcript/transcript.go`
- Test: `internal/transcript/transcript_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/transcript/transcript_test.go`:

```go
func TestInfoCachesUnchangedFile(t *testing.T) {
	body := `{"type":"ai-title","aiTitle":"stable title"}`
	r, opens := newTestReader(&body)
	a := r.Info("sid")
	b := r.Info("sid")
	if *opens != 1 {
		t.Fatalf("opens = %d, want 1 (unchanged file must not be re-read)", *opens)
	}
	if a != b {
		t.Fatalf("cached Info differs: %+v vs %+v", a, b)
	}
}

func TestInfoRereadsOnGrowth(t *testing.T) {
	body := `{"type":"assistant","message":{"content":[{"type":"text","text":"one"}]}}`
	r, opens := newTestReader(&body)
	if got := r.Info("sid"); got.LastMessage != "one" {
		t.Fatalf("LastMessage = %q, want %q", got.LastMessage, "one")
	}
	body += "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"two"}]}}`
	if got := r.Info("sid"); got.LastMessage != "two" {
		t.Fatalf("LastMessage = %q, want %q", got.LastMessage, "two")
	}
	if *opens != 2 {
		t.Fatalf("opens = %d, want 2", *opens)
	}
}

func TestInfoEmptyTranscriptRetried(t *testing.T) {
	body := ""
	r, _ := newTestReader(&body)
	if got := r.Info("sid"); got != (Info{}) {
		t.Fatalf("want zero Info, got %+v", got)
	}
	body = `{"type":"user","message":{"content":"hi there friend"}}`
	if got := r.Info("sid"); got.FirstPrompt != "hi there friend" {
		t.Fatalf("FirstPrompt = %q, want %q", got.FirstPrompt, "hi there friend")
	}
}

// TestInfoTailReadAndSeed drives the bounded-tail path: the first read's tail
// window starts mid-line (the partial line must be discarded) and contains no
// ai-title, forcing one full scan to seed the title. After seeding, growth
// costs a single tail read, and a tail window with no assistant text keeps the
// cached last message.
func TestInfoTailReadAndSeed(t *testing.T) {
	pad := strings.Repeat("x", 200)
	body := strings.Join([]string{
		`{"type":"ai-title","aiTitle":"early title"}`,
		`{"type":"system","content":"` + pad + `"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"tail msg"}]}}`,
	}, "\n")
	r, opens := newTestReader(&body)
	r.tailBytes = 100

	got := r.Info("sid")
	if got.Title != "early title" || got.LastMessage != "tail msg" {
		t.Fatalf("got %+v", got)
	}
	if *opens != 2 { // one tail read + one full scan to seed the title
		t.Fatalf("opens = %d, want 2", *opens)
	}

	body += "\n" + `{"type":"ai-title","aiTitle":"newer title"}`
	got = r.Info("sid")
	if got.Title != "newer title" {
		t.Fatalf("Title = %q, want %q", got.Title, "newer title")
	}
	if got.LastMessage != "tail msg" { // new tail window has no assistant text; cache holds
		t.Fatalf("LastMessage = %q, want %q", got.LastMessage, "tail msg")
	}
	if *opens != 3 { // already seeded: growth costs one tail read
		t.Fatalf("opens = %d, want 3", *opens)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/transcript/ -run TestInfo -v`
Expected: `TestInfoCachesUnchangedFile` FAILS (opens = 2), `TestInfoTailReadAndSeed` FAILS (opens = 1, full scan every call). The others pass already.

- [ ] **Step 3: Add per-session state and rewrite Info**

In `internal/transcript/transcript.go`, add a `cache` field to `Reader` and a `state` type, and replace the Task 1 `Info` body:

```go
// Reader derives Infos from transcripts, re-reading a file only when it grows
// and reading only a bounded tail once the session's title is known.
type Reader struct {
	projectsDir string
	tailBytes   int64

	glob func(pattern string) ([]string, error)
	open func(path string) (io.ReadSeekCloser, error)
	size func(path string) (int64, error)

	mu    sync.Mutex
	cache map[string]*state
}

// state is a session's cached read position and derived display data.
type state struct {
	path   string
	read   bool // at least one scan happened; size is meaningful
	seeded bool // title/first prompt resolved: full scan done or a title seen
	size   int64

	title, firstPrompt, last string
}
```

In `NewReader`, initialize the map: add `cache: map[string]*state{},` to the struct literal.

```go
// Info returns the session's display data, re-reading the transcript only when
// it has grown since the last call.
func (r *Reader) Info(sessionID string) Info {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := r.cache[sessionID]
	if st == nil {
		st = &state{}
		r.cache[sessionID] = st
	}
	if st.path == "" {
		matches, err := r.glob(filepath.Join(r.projectsDir, "*", sessionID+".jsonl"))
		if err != nil || len(matches) == 0 {
			return Info{}
		}
		st.path = matches[0]
	}
	sz, err := r.size(st.path)
	if err != nil || (st.read && sz == st.size) {
		return st.info()
	}

	var offset int64
	if sz > r.tailBytes {
		offset = sz - r.tailBytes
	}
	res, ok := r.scanFrom(st.path, offset)
	if !ok {
		return st.info()
	}
	st.read, st.size = true, sz
	if res.title != "" {
		st.title, st.seeded = res.title, true
	}
	if res.last != "" {
		st.last = res.last
	}
	if offset == 0 {
		// The whole file was scanned; the first prompt is authoritative.
		st.firstPrompt, st.seeded = res.firstPrompt, true
	} else if !st.seeded {
		// Long transcript first seen mid-stream with no title in its tail:
		// one full scan resolves the title / first-prompt fallback.
		if full, ok := r.scanFrom(st.path, 0); ok {
			st.title, st.firstPrompt, st.seeded = full.title, full.firstPrompt, true
		}
	}
	return st.info()
}

func (s *state) info() Info {
	return Info{
		Title:       truncate(s.title, nameMax),
		FirstPrompt: truncate(s.firstPrompt, nameMax),
		LastMessage: truncate(s.last, lastMessageMax),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/transcript/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/transcript/
git add internal/transcript/
git commit -m "feat(transcript): size-change cache, bounded tail reads, title seeding"
```

---

### Task 3: wire Reader into source/claude, delete Namer

**Files:**
- Modify: `internal/source/source.go`
- Modify: `internal/claude/claude.go`
- Modify: `internal/transcript/transcript.go` (deletions)
- Test: `internal/claude/claude_test.go`, `internal/transcript/transcript_test.go` (deletions)

- [ ] **Step 1: Write the failing tests**

In `internal/claude/claude_test.go`, add `"hopper/internal/transcript"` to the imports, replace `fakeNamer` with `fakeReader`, update `TestSessionsMapsFields`, and add `TestDisplayName`:

```go
type fakeReader struct{ infos map[string]transcript.Info }

func (f fakeReader) Info(id string) transcript.Info { return f.infos[id] }
```

In `TestSessionsMapsFields`, replace the `namer:` line of the `Source` literal with:

```go
		reader: fakeReader{infos: map[string]transcript.Info{
			"abc": {Title: "do the thing", LastMessage: "running tests"},
		}},
```

and extend the assertion to cover `LastMessage`:

```go
	if w.ID != "abc" || w.Name != "do the thing" || w.CWD != "/x" || w.PID != 7 ||
		w.Kind != status.Blocked || w.RawStatus != "waiting" ||
		w.WaitingFor != "permission prompt" || !w.UpdatedAt.Equal(now) ||
		w.LastMessage != "running tests" {
		t.Fatalf("unexpected mapping: %+v", w)
	}
```

New test:

```go
func TestDisplayName(t *testing.T) {
	cases := []struct {
		info transcript.Info
		id   string
		want string
	}{
		{transcript.Info{Title: "the title", FirstPrompt: "the prompt"}, "12345678-abc", "the title"},
		{transcript.Info{FirstPrompt: "the prompt"}, "12345678-abc", "the prompt"},
		{transcript.Info{}, "12345678-abc", "12345678"},
		{transcript.Info{}, "short", "short"},
	}
	for _, c := range cases {
		if got := displayName(c.info, c.id); got != c.want {
			t.Errorf("displayName(%+v, %q) = %q, want %q", c.info, c.id, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/claude/ -v`
Expected: compile error — `Source` has no field `reader`, `displayName` undefined, `LastMessage` undefined.

- [ ] **Step 3: Implement the wiring**

In `internal/source/source.go`, add the field to `Session` (after `Name`):

```go
	LastMessage string // the session's latest assistant message, one line, if known
```

In `internal/claude/claude.go`, replace the `namer` interface, `Source` struct, `New`, and the loop body:

```go
// reader derives display data from a session's transcript. Satisfied by
// *transcript.Reader.
type reader interface {
	Info(sessionID string) transcript.Info
}

// Source provides Claude Code sessions read from ~/.claude.
type Source struct {
	loader loader
	reader reader
}

// New builds a Claude Code source over the given sessions and projects dirs
// (typically ~/.claude/sessions and ~/.claude/projects).
func New(sessionsDir, projectsDir string) *Source {
	return &Source{
		loader: session.NewLoader(sessionsDir, session.PIDAlive),
		reader: transcript.NewReader(projectsDir),
	}
}
```

In `Sessions`, replace the `source.Session` literal:

```go
	for _, sess := range sessions {
		info := s.reader.Info(sess.ID)
		out = append(out, source.Session{
			ID:          sess.ID,
			Name:        displayName(info, sess.ID),
			LastMessage: info.LastMessage,
			CWD:         sess.CWD,
			PID:         sess.PID,
			Kind:        kindOf(sess.Status),
			RawStatus:   sess.Status,
			WaitingFor:  sess.WaitingFor,
			UpdatedAt:   sess.StatusUpdatedAt,
		})
	}
```

Add at the bottom of `claude.go`:

```go
// displayName labels a session: Claude's latest session title, else the first
// prompt, else a session-id prefix.
func displayName(info transcript.Info, id string) string {
	if info.Title != "" {
		return info.Title
	}
	if info.FirstPrompt != "" {
		return info.FirstPrompt
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
```

- [ ] **Step 4: Delete the Namer**

In `internal/transcript/transcript.go`, delete the `Namer` type, `NewNamer`, `Name`, `lookup`, and `firstPromptText` (its user-prompt rules now live in `processLine`). Update the package doc:

```go
// Package transcript derives session display data (title, first prompt, last
// assistant message) from Claude Code transcript JSONL files.
package transcript
```

In `internal/transcript/transcript_test.go`, delete `newTestNamer` and the six `TestName*` tests. Update the `Name` comment in `internal/source/source.go`:

```go
	Name        string // human-readable label, e.g. the provider's session title
```

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: all PASS; `go vet ./...` clean (catches any leftover `Namer` references).

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/
git add internal/ 
git commit -m "feat(claude): session name from ai-title with fallbacks, expose last message"
```

---

### Task 4: TUI continuation line for all sessions

**Files:**
- Modify: `internal/tui/view.go:119-122` (View loop), `internal/tui/view.go:228-234` (renderReasonRow)
- Test: `internal/tui/view_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/view_test.go` (reuses `fakeSource`, `fakeRepos`, `fakeTerm`, `applyLoad` from the existing tests):

```go
func TestLastMessageContinuationLine(t *testing.T) {
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "busy one", Kind: status.Working,
			LastMessage: "Now updating the renderer", UpdatedAt: now},
		{ID: "s2", PID: 2, CWD: "/a", Name: "quiet one", Kind: status.Idle, UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))
	m.width = 60

	out := m.View()
	if !strings.Contains(out, "↳ Now updating the renderer") {
		t.Errorf("missing last-message continuation line:\n%s", out)
	}
	if got := strings.Count(out, "↳"); got != 1 {
		t.Errorf("continuation lines = %d, want 1 (idle session has no message):\n%s", got, out)
	}
}

func TestBlockedReasonWinsOverLastMessage(t *testing.T) {
	now := time.Now()
	src := fakeSource{label: "Claude Code", sessions: []source.Session{
		{ID: "s1", PID: 1, CWD: "/a", Name: "stuck", Kind: status.Blocked,
			WaitingFor: "permission: rm", LastMessage: "About to clean up", UpdatedAt: now},
	}}
	repos := fakeRepos{infos: map[string]repo.Info{"/a": {Root: "/a", Name: "aaa", Branch: "main"}}}
	m := applyLoad(New(src, repos, &fakeTerm{}))
	m.width = 60

	out := m.View()
	if !strings.Contains(out, "↳ permission: rm") {
		t.Errorf("missing blocked-reason line:\n%s", out)
	}
	if strings.Contains(out, "About to clean up") {
		t.Errorf("blocked session must show its reason, not the last message:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run "ContinuationLine|WinsOver" -v`
Expected: `TestLastMessageContinuationLine` FAILS (no ↳ for the working session); `TestBlockedReasonWinsOverLastMessage` PASSES already (blocked path exists) — that is fine, it pins the precedence.

- [ ] **Step 3: Implement**

In `internal/tui/view.go`, replace the continuation block inside `View()`:

```go
			if r.Kind == RowSession {
				if txt := continuationText(r.Item); txt != "" {
					b.WriteString(m.renderContinuationRow(txt, w))
					b.WriteByte('\n')
				}
			}
```

Replace `renderReasonRow` with:

```go
// continuationText is the dimmed line shown under a session row: the blocked
// reason when present, otherwise the session's latest assistant message.
func continuationText(it *Item) string {
	if it.Session.Kind == status.Blocked && it.Session.WaitingFor != "" {
		return it.Session.WaitingFor
	}
	return it.Session.LastMessage
}

// renderContinuationRow is the dimmed continuation line under a session row,
// indented to the name column. It is display-only: it is not a Row, so the
// cursor never lands on it.
func (m Model) renderContinuationRow(text string, w int) string {
	_, nameStart, _ := sessionLayout(w)
	return strings.Repeat(" ", nameStart) + st.meta.Render(truncate("↳ "+text, w-nameStart))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: all PASS, including the pre-existing `TestBlockedReasonContinuationLine` (its working session has no `LastMessage`, so the ↳ count stays 1).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/tui/
git add internal/tui/
git commit -m "feat(tui): live last-message continuation line on session rows"
```

---

### Task 5: final verification

- [ ] **Step 1: Full suite, vet, format check**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: all tests PASS, vet clean, `gofmt -l` prints nothing.

- [ ] **Step 2: Eyeball it live (manual)**

Run: `go run . ` (in a terminal with live Claude Code sessions)
Expected: rows labeled with Claude's session titles; a dimmed `↳` line under working sessions updating as they talk; blocked sessions still showing their reason.

- [ ] **Step 3: Commit any stragglers**

```bash
git status --short
```

Expected: clean tree (everything committed in Tasks 1–4). If not, commit the remainder with an appropriate message.
