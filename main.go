// Command cctop is a TUI showing live Claude Code sessions grouped by git repo.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"cctop/internal/repo"
	"cctop/internal/session"
	"cctop/internal/terminal"
	"cctop/internal/transcript"
	"cctop/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	termMode := flag.String("terminal", "auto", "terminal backend: auto|kitty|none")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot find home dir:", err)
		os.Exit(1)
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	projectsDir := filepath.Join(home, ".claude", "projects")

	m := tui.New(
		session.NewLoader(sessionsDir, session.PIDAlive),
		repo.NewResolver(repo.NewExecGit()),
		transcript.NewNamer(projectsDir),
		terminal.Detect(*termMode),
	)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
