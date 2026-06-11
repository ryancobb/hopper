package terminal

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Kitty controls kitty via `kitty @`.
type Kitty struct {
	run func(args ...string) ([]byte, error)
}

// NewKitty returns a kitty backend talking to `kitty @` over KITTY_LISTEN_ON.
func NewKitty() *Kitty {
	return &Kitty{run: func(args ...string) ([]byte, error) {
		return exec.Command("kitty", append([]string{"@"}, args...)...).Output()
	}}
}

func (k *Kitty) Name() string             { return "kitty" }
func (k *Kitty) Capabilities() Capability { return CapFocus | CapPreview }

type klsWindow struct {
	ID                  int `json:"id"`
	ForegroundProcesses []struct {
		PID int `json:"pid"`
	} `json:"foreground_processes"`
}
type klsTab struct {
	Windows []klsWindow `json:"windows"`
}
type klsOSWindow struct {
	Tabs []klsTab `json:"tabs"`
}

// Locate finds the kitty window whose foreground processes include pid.
func (k *Kitty) Locate(pid int) (Handle, bool) {
	out, err := k.run("ls")
	if err != nil {
		return nil, false
	}
	var osw []klsOSWindow
	if err := json.Unmarshal(out, &osw); err != nil {
		return nil, false
	}
	for _, o := range osw {
		for _, t := range o.Tabs {
			for _, w := range t.Windows {
				for _, fp := range w.ForegroundProcesses {
					if fp.PID == pid {
						return w.ID, true
					}
				}
			}
		}
	}
	return nil, false
}

// Focus raises the window (and its tab/OS-window).
func (k *Kitty) Focus(h Handle) error {
	id, ok := h.(int)
	if !ok {
		return ErrBadHandle
	}
	_, err := k.run("focus-window", "--match", fmt.Sprintf("id:%d", id))
	return err
}

// Preview returns the last `lines` non-empty screen lines of the window.
func (k *Kitty) Preview(h Handle, lines int) (string, error) {
	id, ok := h.(int)
	if !ok {
		return "", ErrBadHandle
	}
	out, err := k.run("get-text", "--match", fmt.Sprintf("id:%d", id), "--extent", "screen")
	if err != nil {
		return "", err
	}
	return lastNonEmptyLines(string(out), lines), nil
}

func lastNonEmptyLines(s string, n int) string {
	var kept []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			kept = append(kept, ln)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
}
