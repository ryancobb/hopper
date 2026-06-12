// Package transcript derives session display data (title and first prompt)
// from Claude Code transcript JSONL files.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type entry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	AITitle string `json:"aiTitle"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
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

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// Info is the display data derived from a session's transcript.
type Info struct {
	Title       string // latest ai-title entry, "" if none yet
	FirstPrompt string // first real user prompt, for fallback naming
}

const (
	defaultTailBytes = 256 * 1024
	nameMax          = 40
)

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

	title, firstPrompt string
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
		cache: map[string]*state{},
	}
}

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
	if offset == 0 {
		// The whole file was scanned: title and first prompt are authoritative.
		st.title, st.firstPrompt, st.seeded = res.title, res.firstPrompt, true
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
	}
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
	return res, sc.Err() == nil
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
		if t := strings.TrimSpace(e.AITitle); t != "" {
			res.title = t
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
