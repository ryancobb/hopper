package tui

import (
	"time"

	"github.com/charmbracelet/lipgloss"
	"hopper/internal/status"
)

// displayClass is the render-time status category: status.Kind plus a
// recency split of Idle. Declaration order is rank order, worst highest,
// so classes compare directly.
type displayClass int

const (
	classUnknown displayClass = iota
	classIdle
	classRecentIdle
	classWorking
	classBlocked
)

// recentIdleWindow is how long an idle session stays "recently idle"
// (rendered yellow) before fading to the dim idle look.
const recentIdleWindow = 5 * time.Minute

// classify maps a status kind and its age (time since the status last
// changed) to a display class. Only Idle splits on age: a session idle for
// less than recentIdleWindow likely just finished and is waiting on the user.
func classify(k status.Kind, age time.Duration) displayClass {
	switch k {
	case status.Blocked:
		return classBlocked
	case status.Working:
		return classWorking
	case status.Idle:
		if age < recentIdleWindow {
			return classRecentIdle
		}
		return classIdle
	default:
		return classUnknown
	}
}

// icon is the single-cell status glyph; both idle classes share ○.
func (c displayClass) icon() string {
	switch c {
	case classBlocked:
		return "⚠"
	case classWorking:
		return "●"
	case classRecentIdle, classIdle:
		return "○"
	default:
		return "·"
	}
}

// style is the class's foreground color style.
func (c displayClass) style() lipgloss.Style {
	switch c {
	case classBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	case classWorking:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case classRecentIdle:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	case classIdle:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	default:
		return lipgloss.NewStyle()
	}
}
