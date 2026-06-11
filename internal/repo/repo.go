// Package repo resolves a working directory to its git repo root and branch.
package repo

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Info describes the repo grouping for a session's cwd.
type Info struct {
	Root   string // git toplevel; "" when not in a repo
	Name   string // display label for the repo group
	Branch string
}

// GitRunner runs git commands. Injected for testing.
type GitRunner interface {
	Available() bool
	Run(dir string, args ...string) (string, error) // git -C dir args...
}

// Resolver caches root resolution permanently and branch with a short TTL.
type Resolver struct {
	git GitRunner
	now func() time.Time
	ttl time.Duration

	mu     sync.Mutex
	roots  map[string]rootResult
	branch map[string]branchResult
}

type rootResult struct {
	root, name string
	inRepo     bool
}
type branchResult struct {
	branch string
	exp    time.Time
}

// NewResolver builds a Resolver around git.
func NewResolver(git GitRunner) *Resolver {
	return &Resolver{
		git:    git,
		now:    time.Now,
		ttl:    2 * time.Second,
		roots:  map[string]rootResult{},
		branch: map[string]branchResult{},
	}
}

// Resolve returns the repo Info for a working directory.
func (r *Resolver) Resolve(cwd string) Info {
	rr := r.resolveRoot(cwd)
	info := Info{Name: rr.name}
	if rr.inRepo {
		info.Root = rr.root
		info.Branch = r.resolveBranch(rr.root)
	}
	return info
}

func (r *Resolver) resolveRoot(cwd string) rootResult {
	r.mu.Lock()
	if v, ok := r.roots[cwd]; ok {
		r.mu.Unlock()
		return v
	}
	r.mu.Unlock()

	var res rootResult
	if !r.git.Available() {
		// No git: treat the cwd as its own group, blank branch.
		res = rootResult{root: cwd, name: filepath.Base(cwd), inRepo: true}
	} else if out, err := r.git.Run(cwd, "rev-parse", "--show-toplevel"); err == nil && out != "" {
		res = rootResult{root: out, name: filepath.Base(out), inRepo: true}
	} else {
		res = rootResult{name: "(no repo)"}
	}

	r.mu.Lock()
	r.roots[cwd] = res
	r.mu.Unlock()
	return res
}

func (r *Resolver) resolveBranch(root string) string {
	if !r.git.Available() {
		return ""
	}
	r.mu.Lock()
	if v, ok := r.branch[root]; ok && r.now().Before(v.exp) {
		r.mu.Unlock()
		return v.branch
	}
	r.mu.Unlock()

	out, err := r.git.Run(root, "branch", "--show-current")
	if err != nil {
		out = ""
	}
	r.mu.Lock()
	r.branch[root] = branchResult{branch: out, exp: r.now().Add(r.ttl)}
	r.mu.Unlock()
	return out
}

// ExecGit is the real GitRunner.
type ExecGit struct{ available bool }

// NewExecGit detects whether git is on PATH once.
func NewExecGit() *ExecGit {
	_, err := exec.LookPath("git")
	return &ExecGit{available: err == nil}
}

func (g *ExecGit) Available() bool { return g.available }

func (g *ExecGit) Run(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
