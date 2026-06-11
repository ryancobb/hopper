package transcript

import (
	"io"
	"strings"
	"testing"
)

func newTestNamer(file string, body string) (*Namer, *int) {
	opens := 0
	n := NewNamer("/proj")
	n.glob = func(pattern string) ([]string, error) {
		if file == "" {
			return nil, nil
		}
		return []string{file}, nil
	}
	n.open = func(p string) (io.ReadCloser, error) {
		opens++
		return io.NopCloser(strings.NewReader(body)), nil
	}
	return n, &opens
}

func TestNameFirstRealPrompt(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"system","content":"x"}`,
		`{"type":"user","isMeta":true,"message":{"content":"<meta>"}}`,
		`{"type":"user","message":{"content":"lets work on the billing bug"}}`,
		`{"type":"user","message":{"content":"second message"}}`,
	}, "\n")
	n, _ := newTestNamer("/proj/abc/sid.jsonl", body)
	if got := n.Name("sid"); got != "lets work on the billing bug" {
		t.Fatalf("got %q", got)
	}
}

func TestNameSkipsSlashAndAngle(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"user","message":{"content":"<command-message>/clear</command-message>"}}`,
		`{"type":"user","message":{"content":"/clear"}}`,
	}, "\n")
	n, _ := newTestNamer("/proj/abc/sid.jsonl", body)
	if got := n.Name("sid"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestNameContentArray(t *testing.T) {
	body := `{"type":"user","message":{"content":[{"type":"text","text":"hello from array"}]}}`
	n, _ := newTestNamer("/proj/abc/sid.jsonl", body)
	if got := n.Name("sid"); got != "hello from array" {
		t.Fatalf("got %q", got)
	}
}

func TestNameTruncates(t *testing.T) {
	long := strings.Repeat("a", 100)
	body := `{"type":"user","message":{"content":"` + long + `"}}`
	n, _ := newTestNamer("/proj/abc/sid.jsonl", body)
	got := n.Name("sid")
	if len([]rune(got)) != 40 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got len=%d %q", len([]rune(got)), got)
	}
}

func TestNameCachesOnlyHits(t *testing.T) {
	body := `{"type":"user","message":{"content":"cached prompt"}}`
	n, opens := newTestNamer("/proj/abc/sid.jsonl", body)
	n.Name("sid")
	n.Name("sid")
	if *opens != 1 {
		t.Fatalf("expected 1 open (cached), got %d", *opens)
	}
}

func TestNameNoMatchNotCached(t *testing.T) {
	n, _ := newTestNamer("", "")
	if got := n.Name("sid"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}
