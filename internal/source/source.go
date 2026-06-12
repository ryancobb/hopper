// Package source defines hopper's provider-neutral view of live agent sessions.
//
// A Source adapts some agent or tool (e.g. Claude Code) into a uniform set of
// live sessions the TUI can display. Adding support for another tool means
// adding a new Source implementation; nothing else in the app changes.
package source

import (
	"context"
	"time"

	"hopper/internal/status"
)

// Session is a single live session from a Source, normalized for display.
type Session struct {
	ID         string
	Name       string // human-readable label, e.g. the provider's session title
	CWD        string
	PID        int // 0 when the session has no addressable local process
	Kind       status.Kind
	RawStatus  string // the provider's raw status, shown verbatim when Kind is Unknown
	WaitingFor string // why the session is blocked, if known
	UpdatedAt  time.Time
}

// Source supplies the live sessions for one agent or tool.
type Source interface {
	// Label names the source for display, e.g. "Claude Code".
	Label() string
	// Sessions returns the source's current live sessions.
	Sessions(ctx context.Context) ([]Session, error)
}
