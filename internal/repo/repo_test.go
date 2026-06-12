package repo

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeGit struct {
	avail bool
	calls int
	resp  map[string]string
	errs  map[string]error
}

func key(dir string, args ...string) string {
	k := dir
	for _, a := range args {
		k += " " + a
	}
	return k
}

func (f *fakeGit) Available() bool { return f.avail }
func (f *fakeGit) Run(_ context.Context, dir string, args ...string) (string, error) {
	f.calls++
	k := key(dir, args...)
	if e, ok := f.errs[k]; ok {
		return "", e
	}
	return f.resp[k], nil
}

func TestResolveInRepo(t *testing.T) {
	g := &fakeGit{avail: true, resp: map[string]string{
		key("/repo/sub", "rev-parse", "--show-toplevel"): "/repo",
		key("/repo", "branch", "--show-current"):         "main",
	}}
	r := NewResolver(g)
	got := r.Resolve(context.Background(), "/repo/sub")
	if got.Root != "/repo" || got.Name != "repo" || got.Branch != "main" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveNotARepo(t *testing.T) {
	g := &fakeGit{avail: true, errs: map[string]error{
		key("/tmp", "rev-parse", "--show-toplevel"): errors.New("not a repo"),
	}}
	got := NewResolver(g).Resolve(context.Background(), "/tmp")
	if got.Root != "" || got.Name != "(no repo)" || got.Branch != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveGitUnavailable(t *testing.T) {
	g := &fakeGit{avail: false}
	got := NewResolver(g).Resolve(context.Background(), "/x/y/myproj")
	if got.Root != "/x/y/myproj" || got.Name != "myproj" || got.Branch != "" {
		t.Fatalf("got %+v", got)
	}
	if g.calls != 0 {
		t.Errorf("expected no git calls, got %d", g.calls)
	}
}

func TestRootCachedBranchTTL(t *testing.T) {
	g := &fakeGit{avail: true, resp: map[string]string{
		key("/repo/sub", "rev-parse", "--show-toplevel"): "/repo",
		key("/repo", "branch", "--show-current"):         "main",
	}}
	now := time.Unix(0, 0)
	r := NewResolver(g)
	r.now = func() time.Time { return now }
	ctx := context.Background()

	r.Resolve(ctx, "/repo/sub") // 1 root + 1 branch = 2 calls
	r.Resolve(ctx, "/repo/sub") // root cached, branch within TTL = 0 calls
	if g.calls != 2 {
		t.Fatalf("want 2 calls, got %d", g.calls)
	}
	now = now.Add(3 * time.Second) // past 2s TTL
	r.Resolve(ctx, "/repo/sub")    // branch recomputed = 1 call
	if g.calls != 3 {
		t.Fatalf("want 3 calls, got %d", g.calls)
	}
}

func TestResolveFailureNotCached(t *testing.T) {
	g := &fakeGit{avail: true, errs: map[string]error{
		key("/tmp", "rev-parse", "--show-toplevel"): errors.New("transient"),
	}}
	r := NewResolver(g)
	ctx := context.Background()
	r.Resolve(ctx, "/tmp")
	r.Resolve(ctx, "/tmp")
	if g.calls != 2 {
		t.Fatalf("no-repo result must not be cached; want 2 git calls, got %d", g.calls)
	}
}
