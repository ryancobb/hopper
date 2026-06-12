// Package transcript derives a session display name from its first user prompt.
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

// Namer finds and caches session display names.
type Namer struct {
	projectsDir string
	maxLen      int
	glob        func(pattern string) ([]string, error)
	open        func(path string) (io.ReadCloser, error)

	mu    sync.Mutex
	cache map[string]string
}

// NewNamer builds a Namer over ~/.claude/projects.
func NewNamer(projectsDir string) *Namer {
	return &Namer{
		projectsDir: projectsDir,
		maxLen:      40,
		glob:        filepath.Glob,
		open:        func(p string) (io.ReadCloser, error) { return os.Open(p) },
		cache:       map[string]string{},
	}
}

// Name returns the truncated first prompt for a session, or "" if none yet.
// Only non-empty results are cached, so a just-started session is retried.
func (n *Namer) Name(sessionID string) string {
	n.mu.Lock()
	if v, ok := n.cache[sessionID]; ok {
		n.mu.Unlock()
		return v
	}
	n.mu.Unlock()

	name := n.lookup(sessionID)
	if name != "" {
		n.mu.Lock()
		n.cache[sessionID] = name
		n.mu.Unlock()
	}
	return name
}

func (n *Namer) lookup(sessionID string) string {
	matches, err := n.glob(filepath.Join(n.projectsDir, "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	f, err := n.open(matches[0])
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		if text, ok := firstPromptText(sc.Bytes()); ok {
			return truncate(text, n.maxLen)
		}
	}
	return ""
}

type entry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	AITitle string `json:"aiTitle"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func firstPromptText(line []byte) (string, bool) {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return "", false
	}
	if e.Type != "user" || e.IsMeta {
		return "", false
	}
	text := strings.TrimSpace(extractText(e.Message.Content))
	if text == "" || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "/") {
		return "", false
	}
	return text, true
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
	return res, true
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
