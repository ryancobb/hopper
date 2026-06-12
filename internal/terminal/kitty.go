package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Kitty controls kitty via `kitty @`.
type Kitty struct {
	run func(ctx context.Context, args ...string) ([]byte, error)
}

// NewKitty returns a kitty backend talking to `kitty @` over KITTY_LISTEN_ON.
func NewKitty() *Kitty {
	return &Kitty{run: func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "kitty", append([]string{"@"}, args...)...).Output()
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
func (k *Kitty) Locate(ctx context.Context, pid int) (Handle, bool) {
	out, err := k.run(ctx, "ls")
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
func (k *Kitty) Focus(ctx context.Context, h Handle) error {
	id, ok := h.(int)
	if !ok {
		return ErrBadHandle
	}
	_, err := k.run(ctx, "focus-window", "--match", fmt.Sprintf("id:%d", id))
	return err
}

// Preview returns the last `lines` non-empty screen lines of the window,
// keeping the pane's ANSI colors.
func (k *Kitty) Preview(ctx context.Context, h Handle, lines int) (string, error) {
	id, ok := h.(int)
	if !ok {
		return "", ErrBadHandle
	}
	out, err := k.run(ctx, "get-text", "--match", fmt.Sprintf("id:%d", id), "--extent", "screen", "--ansi", "--add-wrap-markers")
	if err != nil {
		return "", err
	}
	return lastNonEmptyLines(string(out), lines), nil
}

// lastNonEmptyLines keeps the last n visually non-empty screen rows. Without
// wrap markers kitty rejoins soft-wrapped rows into one logical line, which
// can be wider than the captured window; the markers are carriage returns,
// so \n and \r both delimit rows. FieldsFunc drops the empty segments a \r\n
// pair would produce, matching the blank-row filter below.
func lastNonEmptyLines(s string, n int) string {
	var kept []string
	for _, ln := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if strings.TrimSpace(ansi.Strip(ln)) != "" {
			kept = append(kept, ln)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
}
