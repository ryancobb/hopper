package tui

import "testing"

func TestKindOf(t *testing.T) {
	cases := map[string]StatusKind{
		"idle":    StatusIdle,
		"busy":    StatusWorking,
		"waiting": StatusBlocked,
		"weird":   StatusUnknown,
		"":        StatusUnknown,
	}
	for raw, want := range cases {
		if got := KindOf(raw); got != want {
			t.Errorf("KindOf(%q)=%v want %v", raw, got, want)
		}
	}
}

func TestKindLabelAndRank(t *testing.T) {
	if StatusBlocked.Rank() <= StatusWorking.Rank() ||
		StatusWorking.Rank() <= StatusIdle.Rank() ||
		StatusIdle.Rank() <= StatusUnknown.Rank() {
		t.Fatalf("rank order wrong")
	}
	if StatusWorking.Label() != "working" || StatusBlocked.Label() != "blocked" {
		t.Errorf("labels wrong")
	}
}
