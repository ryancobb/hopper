// Package terminal abstracts terminal control (focus, preview) behind a backend.
package terminal

import (
	"errors"
	"os"
)

// Capability is a bitmask of features a backend supports.
type Capability uint

const (
	CapFocus   Capability = 1 << iota // bring a session's pane to the foreground
	CapPreview                        // read a session's pane contents
)

// Has reports whether c includes x.
func (c Capability) Has(x Capability) bool { return c&x != 0 }

// Handle is a backend-specific pane reference.
type Handle any

// Terminal is a pluggable terminal backend.
type Terminal interface {
	Name() string
	Capabilities() Capability
	Locate(pid int) (Handle, bool)
	Focus(h Handle) error
	Preview(h Handle, lines int) (string, error)
}

// Errors returned by backends.
var (
	ErrUnsupported = errors.New("terminal feature unsupported")
	ErrNotFound    = errors.New("session not found in terminal")
	ErrBadHandle   = errors.New("bad terminal handle")
)

// none is the fallback backend with no capabilities.
type none struct{}

func (none) Name() string                        { return "no terminal" }
func (none) Capabilities() Capability            { return 0 }
func (none) Locate(int) (Handle, bool)           { return nil, false }
func (none) Focus(Handle) error                  { return ErrUnsupported }
func (none) Preview(Handle, int) (string, error) { return "", ErrUnsupported }

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
