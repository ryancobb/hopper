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

func TestInfoFirstPromptContentArray(t *testing.T) {
	body := `{"type":"user","message":{"content":[{"type":"text","text":"hello from array"}]}}`
	r, _ := newTestReader(&body)
	if got := r.Info("sid"); got.FirstPrompt != "hello from array" {
		t.Fatalf("FirstPrompt = %q", got.FirstPrompt)
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
	body := `{"type":"ai-title","aiTitle":"old title"}`
	r, opens := newTestReader(&body)
	if got := r.Info("sid"); got.Title != "old title" {
		t.Fatalf("Title = %q, want %q", got.Title, "old title")
	}
	body += "\n" + `{"type":"ai-title","aiTitle":"new title"}`
	if got := r.Info("sid"); got.Title != "new title" {
		t.Fatalf("Title = %q, want %q (title must refresh on growth)", got.Title, "new title")
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
// costs a single tail read, which picks up a newly appended title.
func TestInfoTailReadAndSeed(t *testing.T) {
	pad := strings.Repeat("x", 300)
	body := strings.Join([]string{
		`{"type":"ai-title","aiTitle":"early title"}`,
		`{"type":"user","message":{"content":"the prompt"}}`,
		`{"type":"system","content":"` + pad + `"}`,
	}, "\n")
	r, opens := newTestReader(&body)
	r.tailBytes = 100

	got := r.Info("sid")
	if got.Title != "early title" || got.FirstPrompt != "the prompt" {
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
	if got.FirstPrompt != "the prompt" { // cached from the seeding full scan
		t.Fatalf("FirstPrompt = %q, want %q", got.FirstPrompt, "the prompt")
	}
	if *opens != 3 { // already seeded: growth costs one tail read
		t.Fatalf("opens = %d, want 3", *opens)
	}
}
