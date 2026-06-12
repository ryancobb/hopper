// Package transcript derives a session display name from its first user prompt.
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
