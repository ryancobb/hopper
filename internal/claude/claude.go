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

// reader derives display data from a session's transcript. Satisfied by
// *transcript.Reader.
type reader interface {
	Info(sessionID string) transcript.Info
}

// Source provides Claude Code sessions read from ~/.claude.
type Source struct {
	loader loader
	reader reader
}

// New builds a Claude Code source over the given sessions and projects dirs
// (typically ~/.claude/sessions and ~/.claude/projects).
func New(sessionsDir, projectsDir string) *Source {
	return &Source{
		loader: session.NewLoader(sessionsDir, session.PIDAlive),
		reader: transcript.NewReader(projectsDir),
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
			Name:       displayName(s.reader.Info(sess.ID), sess.ID),
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

// displayName labels a session: Claude's latest session title, else the first
// prompt, else a session-id prefix.
func displayName(info transcript.Info, id string) string {
	if info.Title != "" {
		return info.Title
	}
	if info.FirstPrompt != "" {
		return info.FirstPrompt
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
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
