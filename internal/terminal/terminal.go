// Package terminal abstracts terminal control (focus, preview) behind a backend.
package terminal

import (
	"context"
	"errors"
	"os"
)

// Capability is a bitmask of features a backend supports.
type Capability uint

const (
	CapFocus    Capability = 1 << iota // bring a session's pane to the foreground
	CapPreview                         // read a session's pane contents
	CapSendText                        // write keystrokes/text to a session's pane
)

// Has reports whether c includes x.
func (c Capability) Has(x Capability) bool { return c&x != 0 }

// Handle is a backend-specific pane reference.
type Handle any

// Terminal is a pluggable terminal backend.
type Terminal interface {
	Name() string
	Capabilities() Capability
	Locate(ctx context.Context, pid int) (Handle, bool)
	Focus(ctx context.Context, h Handle) error
	// Preview returns the last `lines` non-empty rows of the pane's screen.
	// Each newline-separated line must be exactly one rendered screen row,
	// no wider than the source pane: soft-wrapped output must be split into
	// its rows, not rejoined into logical lines (the TUI's box layout relies
	// on this). The text is raw terminal output and may carry ANSI styling,
	// including colors left open across rows; callers must not assume plain
	// text.
	Preview(ctx context.Context, h Handle, lines int) (string, error)
	// SendText transmits data to the pane verbatim, including control bytes and
	// escape sequences. Callers pre-encode keys to bytes; the backend must not
	// re-interpret or alter them.
	SendText(ctx context.Context, h Handle, data string) error
}

// Errors returned by backends.
var (
	ErrUnsupported = errors.New("terminal feature unsupported")
	ErrNotFound    = errors.New("session not found in terminal")
	ErrBadHandle   = errors.New("bad terminal handle")
)

// none is the fallback backend with no capabilities.
type none struct{}

func (none) Name() string                                         { return "no terminal" }
func (none) Capabilities() Capability                             { return 0 }
func (none) Locate(context.Context, int) (Handle, bool)           { return nil, false }
func (none) Focus(context.Context, Handle) error                  { return ErrUnsupported }
func (none) Preview(context.Context, Handle, int) (string, error) { return "", ErrUnsupported }
func (none) SendText(context.Context, Handle, string) error       { return ErrUnsupported }

// Detect picks a backend. mode is "auto", "kitty", or "none".
func Detect(mode string) Terminal {
	switch mode {
	case "kitty":
		return NewKitty()
	case "none":
		return none{}
	default: // auto
		if os.Getenv("KITTY_LISTEN_ON") != "" || os.Getenv("TERM") == "xterm-kitty" {
			return NewKitty()
		}
		return none{}
	}
}
