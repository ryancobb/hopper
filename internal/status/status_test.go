package status

import "testing"

func TestLabel(t *testing.T) {
	if Idle.Label() != "idle" || Working.Label() != "working" ||
		Blocked.Label() != "blocked" || Unknown.Label() != "unknown" {
		t.Fatal("labels wrong")
	}
}

func TestRankOrder(t *testing.T) {
	if Blocked.Rank() <= Working.Rank() ||
		Working.Rank() <= Idle.Rank() ||
		Idle.Rank() <= Unknown.Rank() {
		t.Fatalf("rank order wrong")
	}
}
