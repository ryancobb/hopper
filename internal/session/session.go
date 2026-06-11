// Package session reads live Claude Code session state from ~/.claude/sessions.
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Session is a live Claude Code session as shown in the tree.
type Session struct {
	PID             int
	ID              string
	CWD             string
	Status          string // raw: busy/idle/waiting/...
	WaitingFor      string
	StatusUpdatedAt time.Time
	StartedAt       time.Time
	Version         string
}

type rawSession struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	CWD             string `json:"cwd"`
	Status          string `json:"status"`
	WaitingFor      string `json:"waitingFor"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
	StartedAt       int64  `json:"startedAt"`
	Version         string `json:"version"`
}

// Loader reads session files from a directory, filtering out dead processes.
type Loader struct {
	dir     string
	isAlive func(pid int) bool
}

// NewLoader returns a Loader reading dir, using isAlive to drop stale files.
func NewLoader(dir string, isAlive func(pid int) bool) *Loader {
	return &Loader{dir: dir, isAlive: isAlive}
}

// Load returns all live sessions. Missing dir yields an empty slice, not an error.
func (l *Loader) Load() ([]Session, error) {
	entries, err := os.ReadDir(l.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(l.dir, e.Name()))
		if err != nil {
			continue
		}
		var r rawSession
		if err := json.Unmarshal(body, &r); err != nil || r.PID == 0 {
			continue
		}
		if !l.isAlive(r.PID) {
			continue
		}
		out = append(out, Session{
			PID:             r.PID,
			ID:              r.SessionID,
			CWD:             r.CWD,
			Status:          r.Status,
			WaitingFor:      r.WaitingFor,
			StatusUpdatedAt: time.UnixMilli(r.StatusUpdatedAt),
			StartedAt:       time.UnixMilli(r.StartedAt),
			Version:         r.Version,
		})
	}
	return out, nil
}

// PIDAlive reports whether a process is alive (signal 0). Default for NewLoader.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
