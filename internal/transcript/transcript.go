// Package transcript derives session display names from Claude Code
// transcript JSONL files: a session is named by its latest ai-title entry,
// falling back to its first real user prompt.
package transcript

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const defaultTailBytes = 256 * 1024

// Reader derives session display names from transcripts, re-reading a file
// only when its size changes and reading only a bounded tail once the
// session's title is known.
type Reader struct {
	projectsDir string
	tailBytes   int64
	open        func(path string) (io.ReadSeekCloser, error) // seam for tests

	mu    sync.Mutex
	cache map[string]*state
}

// state is a session's cached read position and derived display data.
type state struct {
	path   string
	size   int64 // file size at the last scan; -1 before the first
	seeded bool  // title/first prompt resolved: full scan done or a title seen
	result
}

// NewReader builds a Reader over ~/.claude/projects.
func NewReader(projectsDir string) *Reader {
	return &Reader{
		projectsDir: projectsDir,
		tailBytes:   defaultTailBytes,
		open:        func(p string) (io.ReadSeekCloser, error) { return os.Open(p) },
		cache:       map[string]*state{},
	}
}

// Name returns the session's display name — Claude's latest title for the
// session, else its first real user prompt, else "" — re-reading the
// transcript only when its size has changed since the last call.
func (r *Reader) Name(sessionID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := r.cache[sessionID]
	if st == nil {
		st = &state{size: -1}
		r.cache[sessionID] = st
	}
	if st.path == "" {
		matches, err := filepath.Glob(filepath.Join(r.projectsDir, "*", sessionID+".jsonl"))
		if err != nil || len(matches) == 0 {
			return ""
		}
		st.path = matches[0]
	}
	fi, err := os.Stat(st.path)
	if err != nil || fi.Size() == st.size {
		return st.name()
	}

	var offset int64
	if fi.Size() > r.tailBytes {
		offset = fi.Size() - r.tailBytes
	}
	res, ok := r.scanFrom(st.path, offset)
	if !ok {
		return st.name()
	}
	st.size = fi.Size()
	switch {
	case offset == 0: // whole file scanned: both fields are authoritative
		st.result, st.seeded = res, true
	case res.title != "": // a title in the tail wins; no seed scan needed
		st.title, st.seeded = res.title, true
	case !st.seeded: // long transcript with no title in its tail: seed once
		if full, ok := r.scanFrom(st.path, 0); ok {
			st.result, st.seeded = full, true
		}
	}
	return st.name()
}

func (s *state) name() string {
	if s.title != "" {
		return s.title
	}
	return s.firstPrompt
}

// result accumulates what one scan pass saw.
type result struct {
	title, firstPrompt string
}

// scanFrom parses transcript lines starting at offset; a partial first line at
// a non-zero offset is discarded.
func (r *Reader) scanFrom(path string, offset int64) (result, bool) {
	f, err := r.open(path)
	if err != nil {
		return result{}, false
	}
	defer func() { _ = f.Close() }()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return result{}, false
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var res result
	skip := offset > 0
	for sc.Scan() {
		if skip {
			skip = false
			continue
		}
		processLine(sc.Bytes(), &res)
	}
	return res, sc.Err() == nil
}

type entry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	AITitle string `json:"aiTitle"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// processLine folds one JSONL line into res: ai-titles last-win, the first
// real user prompt sticks.
func processLine(line []byte, res *result) {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return
	}
	switch e.Type {
	case "ai-title":
		if t := oneLine(e.AITitle); t != "" {
			res.title = t
		}
	case "user":
		if res.firstPrompt != "" || e.IsMeta {
			return
		}
		t := oneLine(extractText(e.Message.Content))
		if t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "/") {
			return
		}
		res.firstPrompt = t
	}
}

// oneLine trims s and collapses internal whitespace runs to single spaces, so
// multi-line prompts work as one-line labels.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}
