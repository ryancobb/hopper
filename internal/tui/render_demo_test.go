package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"hopper/internal/repo"
	"hopper/internal/source"
	"hopper/internal/status"
)

// TestRenderDemo is a throwaway visual smoke test: prints the full view with
// realistic sample data. Run: go test ./internal/tui -run RenderDemo -v
func TestRenderDemo(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.ANSI256)

	now := time.Now()
	items := []Item{
		{Session: source.Session{ID: "a1b2c3", Name: "fix auth flow", Kind: status.Working, UpdatedAt: now.Add(-2 * time.Minute)}, Repo: repo.Info{Root: "/r/hopper", Name: "hopper", Branch: "main"}},
		{Session: source.Session{ID: "d4e5f6", Name: "refactor tree rendering for clarity", Kind: status.Blocked, WaitingFor: "permission: Bash(rm -rf ...)", UpdatedAt: now.Add(-14 * time.Minute)}, Repo: repo.Info{Root: "/r/hopper", Name: "hopper", Branch: "main"}},
		{Session: source.Session{ID: "g7h8i9", Name: "investigate flaky kitty test", Kind: status.Idle, UpdatedAt: now.Add(-3 * time.Hour)}, Repo: repo.Info{Root: "/r/hopper", Name: "hopper", Branch: "main"}},
		{Session: source.Session{ID: "j1k2l3", Name: "add billing webhooks", Kind: status.Working, UpdatedAt: now.Add(-1 * time.Minute)}, Repo: repo.Info{Root: "/r/acme-api", Name: "acme-api", Branch: "feat/billing-webhooks"}},
		{Session: source.Session{ID: "m4n5o6", Name: "bump deps", Kind: status.Idle, UpdatedAt: now.Add(-45 * time.Minute)}, Repo: repo.Info{Root: "/r/acme-api", Name: "acme-api", Branch: "feat/billing-webhooks"}},
		{Session: source.Session{ID: "p7q8r9", Name: "draft landing page copy", Kind: status.Blocked, WaitingFor: "user input", UpdatedAt: now.Add(-7 * time.Minute)}, Repo: repo.Info{Root: "/r/website", Name: "website-marketing-revamp", Branch: "redesign/spring-2026-launch"}},
	}
	m := New(fakeSource{label: "Claude Code"}, fakeRepos{}, &fakeTerm{})
	m.width = 100
	mm, _ := m.Update(loadedMsg{items: items})
	m = mm.(Model)
	m.cursor = 2
	fmt.Println(m.View())
}
