package transcript

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func titleLine(s string) string { return `{"type":"ai-title","aiTitle":"` + s + `"}` }
func promptLine(s string) string {
	return `{"type":"user","message":{"content":"` + s + `"}}`
}

// newTestReader builds a Reader over a temp projects dir holding a real
// transcript for session "sid" with the given lines, counting file opens.
func newTestReader(t *testing.T, lines ...string) (*Reader, string, *int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "proj", "sid.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewReader(dir)
	opens := 0
	open := r.open
	r.open = func(p string) (io.ReadSeekCloser, error) {
		opens++
		return open(p)
	}
	return r, path, &opens
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("\n" + line); err != nil {
		t.Fatal(err)
	}
}

func TestNameTitleLastWins(t *testing.T) {
	r, _, _ := newTestReader(t,
		titleLine("first title"),
		promptLine("the prompt"),
		titleLine("second title"),
	)
	if got := r.Name("sid"); got != "second title" {
		t.Fatalf("Name = %q, want %q", got, "second title")
	}
}

func TestNameFirstPromptFallback(t *testing.T) {
	r, _, _ := newTestReader(t,
		`{"type":"user","isMeta":true,"message":{"content":"<meta>"}}`,
		promptLine("/clear"),
		promptLine("<command-message>/clear</command-message>"),
		promptLine("lets work on the billing bug"),
		promptLine("second message"),
	)
	if got := r.Name("sid"); got != "lets work on the billing bug" {
		t.Fatalf("Name = %q", got)
	}
}

func TestNameFirstPromptContentArray(t *testing.T) {
	r, _, _ := newTestReader(t,
		`{"type":"user","message":{"content":[{"type":"text","text":"hello from array"}]}}`,
	)
	if got := r.Name("sid"); got != "hello from array" {
		t.Fatalf("Name = %q", got)
	}
}

func TestNameCollapsesWhitespace(t *testing.T) {
	r, _, _ := newTestReader(t, promptLine(`fix\nthe\tflaky   test`))
	if got := r.Name("sid"); got != "fix the flaky test" {
		t.Fatalf("Name = %q, want %q", got, "fix the flaky test")
	}
}

func TestNameNoTranscript(t *testing.T) {
	r := NewReader(t.TempDir())
	if got := r.Name("sid"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestNameCachesUnchangedFile(t *testing.T) {
	r, _, opens := newTestReader(t, titleLine("stable title"))
	a := r.Name("sid")
	b := r.Name("sid")
	if *opens != 1 {
		t.Fatalf("opens = %d, want 1 (unchanged file must not be re-read)", *opens)
	}
	if a != b || a != "stable title" {
		t.Fatalf("cached Name differs: %q vs %q", a, b)
	}
}

func TestNameRefreshesOnGrowth(t *testing.T) {
	r, path, opens := newTestReader(t, titleLine("old title"))
	if got := r.Name("sid"); got != "old title" {
		t.Fatalf("Name = %q, want %q", got, "old title")
	}
	appendLine(t, path, titleLine("new title"))
	if got := r.Name("sid"); got != "new title" {
		t.Fatalf("Name = %q, want %q (must refresh on growth)", got, "new title")
	}
	if *opens != 2 {
		t.Fatalf("opens = %d, want 2", *opens)
	}
}

func TestNameEmptyTranscriptRetried(t *testing.T) {
	r, path, _ := newTestReader(t)
	if got := r.Name("sid"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
	appendLine(t, path, promptLine("hi there friend"))
	if got := r.Name("sid"); got != "hi there friend" {
		t.Fatalf("Name = %q, want %q", got, "hi there friend")
	}
}

// TestNameTailReadAndSeed drives the bounded-tail path: the first read's tail
// window starts mid-line (the partial line must be discarded) and contains no
// ai-title, forcing one full scan to seed the title. After seeding, growth
// costs a single tail read, which picks up a newly appended title.
func TestNameTailReadAndSeed(t *testing.T) {
	pad := strings.Repeat("x", 300)
	r, path, opens := newTestReader(t,
		titleLine("early title"),
		promptLine("the prompt"),
		`{"type":"system","content":"`+pad+`"}`,
	)
	r.tailBytes = 100

	if got := r.Name("sid"); got != "early title" {
		t.Fatalf("Name = %q, want %q", got, "early title")
	}
	if *opens != 2 { // one tail read + one full scan to seed the title
		t.Fatalf("opens = %d, want 2", *opens)
	}

	appendLine(t, path, titleLine("newer title"))
	if got := r.Name("sid"); got != "newer title" {
		t.Fatalf("Name = %q, want %q", got, "newer title")
	}
	if *opens != 3 { // already seeded: growth costs one tail read
		t.Fatalf("opens = %d, want 3", *opens)
	}
}

// TestNameFullRescanClearsStaleTitle pins that a full scan is authoritative:
// a rewritten transcript with no ai-title drops the previously cached title.
func TestNameFullRescanClearsStaleTitle(t *testing.T) {
	r, path, _ := newTestReader(t, titleLine("ghost title"))
	if got := r.Name("sid"); got != "ghost title" {
		t.Fatalf("Name = %q, want %q", got, "ghost title")
	}
	if err := os.WriteFile(path, []byte(promptLine("a fresh start")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.Name("sid"); got != "a fresh start" {
		t.Fatalf("Name = %q, want %q (full rescan must clear stale title)", got, "a fresh start")
	}
}

// errAfterFile reads through then fails at EOF, simulating an I/O error
// mid-scan.
type errAfterFile struct{ io.ReadSeekCloser }

func (f errAfterFile) Read(p []byte) (int, error) {
	n, err := f.ReadSeekCloser.Read(p)
	if err == io.EOF {
		return n, errors.New("disk gone")
	}
	return n, err
}

func TestNameKeepsCacheOnScanFailure(t *testing.T) {
	r, path, _ := newTestReader(t, titleLine("good title"))
	if got := r.Name("sid"); got != "good title" {
		t.Fatalf("Name = %q, want %q", got, "good title")
	}
	appendLine(t, path, titleLine("unseen title"))
	open := r.open
	r.open = func(p string) (io.ReadSeekCloser, error) {
		f, err := open(p)
		if err != nil {
			return nil, err
		}
		return errAfterFile{f}, nil
	}
	if got := r.Name("sid"); got != "good title" {
		t.Fatalf("Name = %q, want cached %q after failed scan", got, "good title")
	}
}
