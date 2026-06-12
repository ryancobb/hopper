package tui

import (
	"testing"
	"time"

	"hopper/internal/repo"
	"hopper/internal/source"
	"hopper/internal/status"
)

func item(id, root, name, branch string, kind status.Kind, ago time.Duration) Item {
	return Item{
		Session: source.Session{
			ID:        id,
			Name:      name + "-prompt",
			CWD:       root,
			Kind:      kind,
			RawStatus: kind.Label(),
			UpdatedAt: time.Now().Add(-ago),
		},
		Repo: repo.Info{Root: root, Name: name, Branch: branch},
	}
}

func TestBuildGroupsSortsAndAggregates(t *testing.T) {
	items := []Item{
		item("s1", "/b", "bbb", "main", status.Idle, time.Minute),
		item("s2", "/a", "aaa", "main", status.Idle, 2*time.Minute),
		item("s3", "/a", "aaa", "feat", status.Blocked, time.Minute),
	}
	groups := BuildGroups(items)
	if len(groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(groups))
	}
	// sorted by label: aaa before bbb
	if groups[0].Label != "aaa" || groups[1].Label != "bbb" {
		t.Fatalf("group order: %s,%s", groups[0].Label, groups[1].Label)
	}
	// within aaa, blocked (s3) sorts before idle (s2)
	if groups[0].Items[0].Session.ID != "s3" {
		t.Fatalf("aaa first item = %s", groups[0].Items[0].Session.ID)
	}
}

func TestFlattenCollapse(t *testing.T) {
	groups := BuildGroups([]Item{
		item("s1", "/a", "aaa", "main", status.Idle, time.Minute),
		item("s2", "/a", "aaa", "main", status.Working, time.Minute),
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
		item("s1", "/a", "alpha", "main", status.Idle, time.Minute),
		item("s2", "/b", "beta", "feature-x", status.Idle, time.Minute),
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

func TestBuildGroupsNoRepo(t *testing.T) {
	groups := BuildGroups([]Item{
		item("s1", "", "(no repo)", "", status.Working, time.Minute),
	})
	if len(groups) != 1 || groups[0].Key != "" || groups[0].Label != "(no repo)" {
		t.Fatalf("no-repo group: %+v", groups)
	}
	rows := Flatten(groups, map[string]bool{"": true}, "")
	if len(rows) != 1 || rows[0].Kind != RowRepo {
		t.Fatalf("collapsed no-repo should be just the repo row, got %d", len(rows))
	}
}
