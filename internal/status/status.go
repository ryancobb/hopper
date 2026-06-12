// Package status defines hopper's provider-neutral session status.
package status

// Kind is the display category of a session's status.
type Kind int

const (
	Unknown Kind = iota
	Idle
	Working
	Blocked
)

// Label is the human-readable status word.
func (k Kind) Label() string {
	switch k {
	case Idle:
		return "idle"
	case Working:
		return "working"
	case Blocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// Rank orders kinds for sorting and aggregation: Blocked > Working > Idle > Unknown.
func (k Kind) Rank() int {
	switch k {
	case Blocked:
		return 3
	case Working:
		return 2
	case Idle:
		return 1
	default:
		return 0
	}
}
