package tui

import (
	"testing"
	"time"

	"cctop/internal/repo"
	"cctop/internal/session"
)

func item(id, root, name, branch, status string, ago time.Duration) Item {
	return Item{
		Session: session.Session{ID: id, CWD: root, Status: status,
			StatusUpdatedAt: time.Now().Add(-ago)},
		Repo: repo.Info{Root: root, Name: name, Branch: branch},
		Name: name + "-prompt",
		Kind: KindOf(status),
	}
}

func TestBuildGroupsSortsAndAggregates(t *testing.T) {
	items := []Item{
		item("s1", "/b", "bbb", "main", "idle", time.Minute),
		item("s2", "/a", "aaa", "main", "idle", 2*time.Minute),
		item("s3", "/a", "aaa", "feat", "waiting", time.Minute), // blocked
	}
	groups := BuildGroups(items)
	if len(groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(groups))
	}
	// sorted by label: aaa before bbb
	if groups[0].Label != "aaa" || groups[1].Label != "bbb" {
		t.Fatalf("group order: %s,%s", groups[0].Label, groups[1].Label)
	}
	// aaa aggregate = blocked (worst of)
	if groups[0].Kind != StatusBlocked {
		t.Fatalf("aaa aggregate = %v", groups[0].Kind)
	}
	// within aaa, blocked (s3) sorts before idle (s2)
	if groups[0].Items[0].Session.ID != "s3" {
		t.Fatalf("aaa first item = %s", groups[0].Items[0].Session.ID)
	}
}

func TestFlattenCollapse(t *testing.T) {
	groups := BuildGroups([]Item{
		item("s1", "/a", "aaa", "main", "idle", time.Minute),
		item("s2", "/a", "aaa", "main", "busy", time.Minute),
	})
	all := Flatten(groups, map[string]bool{}, "")
	if len(all) != 3 { // 1 repo + 2 sessions
		t.Fatalf("want 3 rows, got %d", len(all))
	}
	collapsed := Flatten(groups, map[string]bool{"/a": true}, "")
	if len(collapsed) != 1 || collapsed[0].Kind != RowRepo {
		t.Fatalf("collapsed should be just the repo row, got %d", len(collapsed))
	}
}

func TestFlattenFilter(t *testing.T) {
	groups := BuildGroups([]Item{
		item("s1", "/a", "alpha", "main", "idle", time.Minute),
		item("s2", "/b", "beta", "feature-x", "idle", time.Minute),
	})
	rows := Flatten(groups, map[string]bool{}, "feature")
	// only beta group matches (by branch); ignores collapse
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (beta repo+session), got %d", len(rows))
	}
	if rows[0].Group.Label != "beta" {
		t.Fatalf("filtered group = %s", rows[0].Group.Label)
	}
}
