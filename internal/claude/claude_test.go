package claude

import (
	"context"
	"testing"
	"time"

	"hopper/internal/session"
	"hopper/internal/status"
)

type fakeLoader struct {
	sessions []session.Session
	err      error
}

func (f fakeLoader) Load() ([]session.Session, error) { return f.sessions, f.err }

type fakeNamer struct{ names map[string]string }

func (f fakeNamer) Name(id string) string { return f.names[id] }

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
		namer: fakeNamer{names: map[string]string{"abc": "do the thing"}},
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

func TestLabel(t *testing.T) {
	if (&Source{}).Label() != "Claude Code" {
		t.Fatal("label wrong")
	}
}
