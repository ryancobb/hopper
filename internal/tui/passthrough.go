package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"hopper/internal/source"
	"hopper/internal/terminal"
)

// keyToBytes encodes a key event as the bytes to send to a pane. The second
// return is false for unmapped keys, which the caller drops. The exit chord
// (Ctrl-]) never reaches here in normal flow: the caller intercepts it before
// translating; it returns false defensively if it does.
func keyToBytes(msg tea.KeyMsg) (string, bool) {
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			return string(msg.Runes), true
		}
		return "", false
	case tea.KeySpace:
		return " ", true
	case tea.KeyEnter:
		return "\r", true
	case tea.KeyTab:
		return "\t", true
	case tea.KeyShiftTab:
		return "\x1b[Z", true
	case tea.KeyBackspace:
		return "\x7f", true
	case tea.KeyEsc:
		return "\x1b", true
	case tea.KeyUp:
		return "\x1b[A", true
	case tea.KeyDown:
		return "\x1b[B", true
	case tea.KeyRight:
		return "\x1b[C", true
	case tea.KeyLeft:
		return "\x1b[D", true
	case tea.KeyHome:
		return "\x1b[H", true
	case tea.KeyEnd:
		return "\x1b[F", true
	case tea.KeyPgUp:
		return "\x1b[5~", true
	case tea.KeyPgDown:
		return "\x1b[6~", true
	}
	// Ctrl-A through Ctrl-Z map to control bytes 0x01..0x1a; Bubble Tea's KeyType
	// values for KeyCtrlA..KeyCtrlZ are exactly those bytes, so cast directly.
	// Enter (0x0d) and Tab (0x09) fall in that range but are handled above.
	// Ctrl-@ (NUL, type 0) is deliberately excluded: type 0 is also the
	// zero-value KeyMsg, and forwarding NUL is virtually never needed.
	if msg.Type >= tea.KeyCtrlA && msg.Type <= tea.KeyCtrlZ {
		return string([]byte{byte(msg.Type)}), true
	}
	return "", false
}

// sendMsg reports the result of a SendText command.
type sendMsg struct{ err error }

// passthroughLocatedMsg carries the result of resolving the pinned pane's
// handle, done once when entering passthrough so individual keystrokes need no
// lookup.
type passthroughLocatedMsg struct {
	id     string          // the session this locate was for; stale if it no longer matches
	handle terminal.Handle // the resolved pane handle
	ok     bool            // whether the pane was found
}

// locateCmd resolves a session's pane handle once, off the UI goroutine.
func locateCmd(term terminal.Terminal, id string, pid int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		h, ok := term.Locate(ctx, pid)
		return passthroughLocatedMsg{id: id, handle: h, ok: ok}
	}
}

// sendCmd sends data to an already-resolved pane handle, off the UI goroutine.
// The handle is cached on enter, so a keystroke costs one send and no locate.
func sendCmd(term terminal.Terminal, h terminal.Handle, data string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return sendMsg{err: term.SendText(ctx, h, data)}
	}
}

// sessionByID finds a loaded session by ID across all groups.
func (m Model) sessionByID(id string) (source.Session, bool) {
	for _, g := range m.groups {
		for _, it := range g.Items {
			if it.Session.ID == id {
				return it.Session, true
			}
		}
	}
	return source.Session{}, false
}

// inPassthrough reports whether keystrokes are currently relayed to a pane.
func (m Model) inPassthrough() bool { return m.passthroughID != "" }

// exitPassthrough clears passthrough state, setting statusMsg to reason (pass ""
// to clear it). It deliberately leaves passthroughTick set: the preview tick
// lapses itself on its next fire once the mode is off (the same pattern as the
// spinner's flag), and clearing it here would let a fast re-enter schedule a
// second, concurrent tick.
func (m Model) exitPassthrough(reason string) Model {
	m.passthroughID = ""
	m.passthroughName = ""
	m.passthroughHandle = nil
	m.statusMsg = reason
	return m
}

// enterPassthrough starts relaying keystrokes to the selected session's pane.
// It requires a session row and a terminal that can send; otherwise it is a
// no-op (with a status message when the capability is missing). It resolves the
// pane handle once (so keystrokes need no per-key locate), forces the preview
// on, and starts the passthrough preview tick, which captures the pinned pane by
// its cached handle independent of the cursor.
func (m Model) enterPassthrough() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.Kind != RowSession {
		return m, nil
	}
	if !m.term.Capabilities().Has(terminal.CapSendText) {
		m.statusMsg = "send unavailable in this terminal"
		return m, nil
	}
	s := r.Item.Session
	m.passthroughID = s.ID
	m.passthroughName = s.Name
	if m.passthroughName == "" {
		m.passthroughName = short(s.ID)
	}
	m.passthroughHandle = nil
	m.showPreview = true
	m.statusMsg = ""

	cmds := []tea.Cmd{locateCmd(m.term, s.ID, s.PID)}
	if !m.passthroughTick {
		m.passthroughTick = true
		cmds = append(cmds, passthroughTickCmd())
	}
	return m, tea.Batch(cmds...)
}

// handlePassthroughKey relays one key to the pinned pane. Ctrl-] exits the
// mode; unmapped keys are ignored; keys pressed in the brief window before the
// pane handle is resolved are dropped. Every other key is translated and sent.
func (m Model) handlePassthroughKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+]" {
		return m.exitPassthrough(""), nil
	}
	data, ok := keyToBytes(msg)
	if !ok {
		return m, nil
	}
	if m.passthroughHandle == nil {
		return m, nil // pane not resolved yet (brief window right after entering)
	}
	return m, sendCmd(m.term, m.passthroughHandle, data)
}
