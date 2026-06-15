package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"hopper/internal/status"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		kind status.Kind
		age  time.Duration
		want displayClass
	}{
		{status.Idle, 0, classRecentIdle},
		{status.Idle, recentIdleWindow - time.Second, classRecentIdle},
		{status.Idle, recentIdleWindow, classIdle}, // boundary: exactly 5m is stale
		{status.Idle, 2 * time.Hour, classIdle},
		{status.Working, 2 * time.Hour, classWorking}, // only Idle splits on age
		{status.Blocked, 2 * time.Hour, classBlocked},
		{status.Unknown, 0, classUnknown},
	}
	for _, c := range cases {
		if got := classify(c.kind, c.age); got != c.want {
			t.Errorf("classify(%v, %v) = %v, want %v", c.kind, c.age, got, c.want)
		}
	}
}

func TestClassRecentIdleAppearance(t *testing.T) {
	if got := classRecentIdle.icon(); got != "○" {
		t.Errorf("icon = %q, want %q", got, "○")
	}
	if got := classRecentIdle.style().GetForeground(); got != lipgloss.Color("3") {
		t.Errorf("foreground = %v, want yellow (3)", got)
	}
}

func TestClassRankOrder(t *testing.T) {
	if !(classBlocked > classWorking && classWorking > classRecentIdle &&
		classRecentIdle > classIdle && classIdle > classUnknown) {
		t.Error("classes must rank worst-highest: blocked > working > recent > idle > unknown")
	}
}

func TestAccent(t *testing.T) {
	cases := []struct {
		c          displayClass
		wantStripe bool
		wantDim    bool
	}{
		{classBlocked, true, false},
		{classWorking, true, false},
		{classRecentIdle, true, false},
		{classIdle, false, true},
		{classUnknown, false, true},
	}
	for _, tc := range cases {
		stripe, dim := tc.c.accent()
		if stripe != tc.wantStripe || dim != tc.wantDim {
			t.Errorf("%v.accent() = (%v,%v), want (%v,%v)", tc.c, stripe, dim, tc.wantStripe, tc.wantDim)
		}
	}
}
