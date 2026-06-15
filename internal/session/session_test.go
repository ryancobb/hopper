package session

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadParsesAndFiltersDeadPIDs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "100.json", `{
		"pid":100,"sessionId":"aaaa-1","cwd":"/repo/a","status":"busy",
		"waitingFor":"","statusUpdatedAt":1781211050894,"startedAt":1781210895663,"version":"2.1.173"}`)
	writeFile(t, dir, "200.json", `{
		"pid":200,"sessionId":"bbbb-2","cwd":"/repo/b","status":"waiting",
		"waitingFor":"permission prompt","statusUpdatedAt":1781211050894,"startedAt":1,"version":"x"}`)
	writeFile(t, dir, "bad.json", `{not json`)

	alive := func(pid int) bool { return pid == 100 } // 200 is "dead", bad has no pid

	l := NewLoader(dir, alive)
	got, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 live session, got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.PID != 100 || s.ID != "aaaa-1" || s.CWD != "/repo/a" || s.Status != "busy" {
		t.Errorf("unexpected session: %+v", s)
	}
	if s.StatusUpdatedAt.UnixMilli() != 1781211050894 {
		t.Errorf("StatusUpdatedAt not parsed: %v", s.StatusUpdatedAt)
	}
}

func TestLoadExcludesSDKAgentSessions(t *testing.T) {
	dir := t.TempDir()
	// A normal CLI session: kept.
	writeFile(t, dir, "100.json", `{"pid":100,"sessionId":"cli-1","cwd":"/repo/a","status":"busy","entrypoint":"cli","version":"x"}`)
	// An SDK-spawned sub-agent: excluded.
	writeFile(t, dir, "200.json", `{"pid":200,"sessionId":"agent-1","cwd":"/repo/a","entrypoint":"sdk-py","version":"x"}`)
	// No entrypoint at all (older session file): kept, not an agent.
	writeFile(t, dir, "300.json", `{"pid":300,"sessionId":"old-1","cwd":"/repo/b","status":"idle","version":"x"}`)

	l := NewLoader(dir, func(int) bool { return true })
	got, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.ID] = true
	}
	if ids["agent-1"] {
		t.Errorf("sdk-py agent session should be excluded, got %+v", got)
	}
	if !ids["cli-1"] || !ids["old-1"] {
		t.Errorf("non-agent sessions should be kept, got %+v", got)
	}
}

func TestLoadEmptyDir(t *testing.T) {
	l := NewLoader(t.TempDir(), func(int) bool { return true })
	got, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0, got %d", len(got))
	}
}

func TestLoadMissingDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	l := NewLoader(missing, func(int) bool { return true })
	got, err := l.Load()
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}
