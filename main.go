// Command hopper is a TUI showing live agent coding sessions grouped by git repo.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"hopper/internal/claude"
	"hopper/internal/repo"
	"hopper/internal/terminal"
	"hopper/internal/tui"
)

func main() {
	termMode := flag.String("terminal", "auto", "terminal backend: auto|kitty|none")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot find home dir:", err)
		os.Exit(1)
	}

	src := claude.New(
		filepath.Join(home, ".claude", "sessions"),
		filepath.Join(home, ".claude", "projects"),
	)

	m := tui.New(src, repo.NewResolver(repo.NewExecGit()), terminal.Detect(*termMode))

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
