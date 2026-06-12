// Package claude adapts Claude Code's on-disk session state into a hopper source.
package claude

import (
	"context"

	"hopper/internal/session"
	"hopper/internal/source"
	"hopper/internal/status"
	"hopper/internal/transcript"
)

// loader reads the live session records. Satisfied by *session.Loader.
type loader interface {
	Load() ([]session.Session, error)
}

// namer derives a display name for a session id. Satisfied by *transcript.Reader.
type namer interface {
	Name(sessionID string) string
}

// Source provides Claude Code sessions read from ~/.claude.
type Source struct {
	loader loader
	namer  namer
}

// New builds a Claude Code source over the given sessions and projects dirs
// (typically ~/.claude/sessions and ~/.claude/projects).
func New(sessionsDir, projectsDir string) *Source {
	return &Source{
		loader: session.NewLoader(sessionsDir, session.PIDAlive),
		namer:  transcript.NewReader(projectsDir),
	}
}

// Label identifies the source in the UI.
func (s *Source) Label() string { return "Claude Code" }

// Sessions returns the live Claude Code sessions, normalized for display.
func (s *Source) Sessions(_ context.Context) ([]source.Session, error) {
	sessions, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	out := make([]source.Session, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, source.Session{
			ID:         sess.ID,
			Name:       displayName(s.namer.Name(sess.ID), sess.ID),
			CWD:        sess.CWD,
			PID:        sess.PID,
			Kind:       kindOf(sess.Status),
			RawStatus:  sess.Status,
			WaitingFor: sess.WaitingFor,
			UpdatedAt:  sess.StatusUpdatedAt,
		})
	}
	return out, nil
}

// displayName guarantees a session label: the transcript-derived name when
// present, else a session-id prefix.
func displayName(name, id string) string {
	if name != "" {
		return name
	}
	return id[:min(8, len(id))]
}

// kindOf maps Claude Code's raw status vocabulary to a generic status kind.
func kindOf(raw string) status.Kind {
	switch raw {
	case "idle":
		return status.Idle
	case "busy":
		return status.Working
	case "waiting":
		return status.Blocked
	default:
		return status.Unknown
	}
}
