package claude

import (
	"context"
	"testing"
	"time"

	"hopper/internal/session"
	"hopper/internal/status"
	"hopper/internal/transcript"
)

type fakeLoader struct {
	sessions []session.Session
	err      error
}

func (f fakeLoader) Load() ([]session.Session, error) { return f.sessions, f.err }

type fakeReader struct{ infos map[string]transcript.Info }

func (f fakeReader) Info(id string) transcript.Info { return f.infos[id] }

func TestKindOf(t *testing.T) {
	cases := map[string]status.Kind{
		"idle":       status.Idle,
		"busy":       status.Working,
		"waiting":    status.Blocked,
		"compacting": status.Unknown,
		"":           status.Unknown,
	}
	for raw, want := range cases {
		if got := kindOf(raw); got != want {
			t.Errorf("kindOf(%q)=%v want %v", raw, got, want)
		}
	}
}

func TestSessionsMapsFields(t *testing.T) {
	now := time.Now()
	s := &Source{
		loader: fakeLoader{sessions: []session.Session{{
			PID: 7, ID: "abc", CWD: "/x", Status: "waiting",
			WaitingFor: "permission prompt", StatusUpdatedAt: now,
		}}},
		reader: fakeReader{infos: map[string]transcript.Info{
			"abc": {Title: "do the thing"},
		}},
	}
	got, err := s.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	w := got[0]
	if w.ID != "abc" || w.Name != "do the thing" || w.CWD != "/x" || w.PID != 7 ||
		w.Kind != status.Blocked || w.RawStatus != "waiting" ||
		w.WaitingFor != "permission prompt" || !w.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected mapping: %+v", w)
	}
}

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

func TestLabel(t *testing.T) {
	if (&Source{}).Label() != "Claude Code" {
		t.Fatal("label wrong")
	}
}
