package terminal

import (
	"context"
	"errors"
	"testing"
)

func TestCapabilityHas(t *testing.T) {
	c := CapFocus | CapPreview
	if !c.Has(CapFocus) || !c.Has(CapPreview) {
		t.Fatal("expected both caps")
	}
	if (CapFocus).Has(CapPreview) {
		t.Fatal("focus should not have preview")
	}
}

func TestNoneBackend(t *testing.T) {
	n := none{}
	if n.Capabilities() != 0 || n.Name() != "no terminal" {
		t.Fatal("none backend wrong")
	}
	if _, ok := n.Locate(context.Background(), 123); ok {
		t.Fatal("none should not locate")
	}
	if err := n.Focus(context.Background(), nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestNoneSendTextUnsupported(t *testing.T) {
	n := none{}
	if n.Capabilities().Has(CapSendText) {
		t.Fatal("none should not advertise CapSendText")
	}
	if err := n.SendText(context.Background(), nil, "x"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestDetectModes(t *testing.T) {
	if _, ok := Detect("none").(none); !ok {
		t.Fatal("mode none should give none backend")
	}
	if _, ok := Detect("kitty").(*Kitty); !ok {
		t.Fatal("mode kitty should give kitty backend")
	}
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/x")
	if _, ok := Detect("auto").(*Kitty); !ok {
		t.Fatal("auto with KITTY_LISTEN_ON should give kitty backend")
	}
	t.Setenv("KITTY_LISTEN_ON", "")
	t.Setenv("TERM", "dumb")
	if _, ok := Detect("auto").(none); !ok {
		t.Fatal("auto without kitty env should give none backend")
	}
}
