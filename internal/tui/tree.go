package tui

import (
	"sort"
	"strings"

	"cctop/internal/repo"
	"cctop/internal/session"
)

// Item is a session enriched for display.
type Item struct {
	Session session.Session
	Repo    repo.Info
	Name    string
	Kind    StatusKind
}

// Group is a repo with its sessions.
type Group struct {
	Key   string // repo root, or "" for the no-repo bucket
	Label string
	Items []Item
	Kind  StatusKind // aggregate (worst of)
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
		if it.Kind.Rank() > g.Kind.Rank() {
			g.Kind = it.Kind
		}
	}
	groups := make([]Group, 0, len(order))
	for _, k := range order {
		g := byKey[k]
		sort.SliceStable(g.Items, func(i, j int) bool {
			a, b := g.Items[i], g.Items[j]
			if a.Kind.Rank() != b.Kind.Rank() {
				return a.Kind.Rank() > b.Kind.Rank()
			}
			return a.Session.StatusUpdatedAt.After(b.Session.StatusUpdatedAt)
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
			strings.Contains(strings.ToLower(it.Name), f) ||
			strings.Contains(strings.ToLower(it.Repo.Branch), f) {
			out = append(out, it)
		}
	}
	return out
}
