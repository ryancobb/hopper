package tui

import (
	"sort"
	"strings"

	"hopper/internal/repo"
	"hopper/internal/source"
	"hopper/internal/status"
)

// Item is a live session paired with its resolved repo, ready for display.
type Item struct {
	Session source.Session
	Repo    repo.Info
}

// Group is a repo with its sessions.
type Group struct {
	Key   string // repo root, or "" for the no-repo bucket
	Label string
	Items []Item
	Kind  status.Kind // aggregate (worst of)
}

// RowKind distinguishes repo header rows from session rows.
type RowKind int

const (
	RowRepo RowKind = iota
	RowSession
)

// Row is one visible line in the tree.
type Row struct {
	Kind  RowKind
	Group *Group
	Item  *Item
}

// BuildGroups groups items by repo root, sorts, and computes aggregate status.
func BuildGroups(items []Item) []Group {
	byKey := map[string]*Group{}
	var order []string
	for _, it := range items {
		g, ok := byKey[it.Repo.Root]
		if !ok {
			g = &Group{Key: it.Repo.Root, Label: it.Repo.Name}
			byKey[it.Repo.Root] = g
			order = append(order, it.Repo.Root)
		}
		g.Items = append(g.Items, it)
		if it.Session.Kind.Rank() > g.Kind.Rank() {
			g.Kind = it.Session.Kind
		}
	}
	groups := make([]Group, 0, len(order))
	for _, k := range order {
		g := byKey[k]
		sort.SliceStable(g.Items, func(i, j int) bool {
			a, b := g.Items[i], g.Items[j]
			if a.Session.Kind.Rank() != b.Session.Kind.Rank() {
				return a.Session.Kind.Rank() > b.Session.Kind.Rank()
			}
			return a.Session.UpdatedAt.After(b.Session.UpdatedAt)
		})
		groups = append(groups, *g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Label < groups[j].Label })
	return groups
}

// Flatten produces visible rows honoring collapse state and an optional filter.
// A non-empty filter ignores collapse (matches are always shown expanded).
func Flatten(groups []Group, collapsed map[string]bool, filter string) []Row {
	f := strings.ToLower(strings.TrimSpace(filter))
	var rows []Row
	for gi := range groups {
		g := &groups[gi]
		items := g.Items
		if f != "" {
			items = filterItems(items, f)
			if len(items) == 0 {
				continue
			}
		}
		rows = append(rows, Row{Kind: RowRepo, Group: g})
		if f == "" && collapsed[g.Key] {
			continue
		}
		for ii := range items {
			rows = append(rows, Row{Kind: RowSession, Item: &items[ii]})
		}
	}
	return rows
}

func filterItems(items []Item, f string) []Item {
	var out []Item
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Repo.Name), f) ||
			strings.Contains(strings.ToLower(it.Session.Name), f) ||
			strings.Contains(strings.ToLower(it.Repo.Branch), f) {
			out = append(out, it)
		}
	}
	return out
}
