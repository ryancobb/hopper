package tui

// StatusKind is the display category for a session's raw status.
type StatusKind int

const (
	StatusUnknown StatusKind = iota
	StatusIdle
	StatusWorking
	StatusBlocked
)

// KindOf maps a raw session status to a display kind.
func KindOf(raw string) StatusKind {
	switch raw {
	case "idle":
		return StatusIdle
	case "busy":
		return StatusWorking
	case "waiting":
		return StatusBlocked
	default:
		return StatusUnknown
	}
}

// Label is the human-readable status word.
func (k StatusKind) Label() string {
	switch k {
	case StatusIdle:
		return "idle"
	case StatusWorking:
		return "working"
	case StatusBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// Rank orders kinds for sorting and aggregation: blocked > working > idle > unknown.
func (k StatusKind) Rank() int {
	switch k {
	case StatusBlocked:
		return 3
	case StatusWorking:
		return 2
	case StatusIdle:
		return 1
	default:
		return 0
	}
}
