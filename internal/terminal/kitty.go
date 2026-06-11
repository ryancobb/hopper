package terminal

// Kitty is the kitty terminal backend. Behaviour is implemented in Task 6.
type Kitty struct {
	run func(args ...string) ([]byte, error)
}

// NewKitty returns a kitty backend talking to `kitty @`.
func NewKitty() *Kitty { return &Kitty{} }

func (k *Kitty) Name() string                        { return "kitty" }
func (k *Kitty) Capabilities() Capability            { return CapFocus | CapPreview }
func (k *Kitty) Locate(int) (Handle, bool)           { return nil, false }
func (k *Kitty) Focus(Handle) error                  { return ErrUnsupported }
func (k *Kitty) Preview(Handle, int) (string, error) { return "", ErrUnsupported }
