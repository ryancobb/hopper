package terminal

import (
	"context"
	"strings"
	"testing"
)

const lsFixture = `[
 {"id":1,"tabs":[
   {"id":1,"windows":[
     {"id":10,"foreground_processes":[{"pid":97201},{"pid":97046}]},
     {"id":2,"foreground_processes":[{"pid":9801}]}
   ]},
   {"id":3,"windows":[
     {"id":4,"foreground_processes":[{"pid":42210}]}
   ]}
 ]}
]`

func fakeKitty(out string, capture *[][]string) *Kitty {
	return &Kitty{run: func(_ context.Context, args ...string) ([]byte, error) {
		if capture != nil {
			*capture = append(*capture, args)
		}
		return []byte(out), nil
	}}
}

func TestKittyLocate(t *testing.T) {
	k := fakeKitty(lsFixture, nil)
	ctx := context.Background()
	h, ok := k.Locate(ctx, 97046)
	if !ok || h.(int) != 10 {
		t.Fatalf("want window 10, got %v ok=%v", h, ok)
	}
	h, ok = k.Locate(ctx, 42210)
	if !ok || h.(int) != 4 {
		t.Fatalf("want window 4, got %v ok=%v", h, ok)
	}
	if _, ok := k.Locate(ctx, 999); ok {
		t.Fatal("999 should not be found")
	}
}

func TestKittyFocusCommand(t *testing.T) {
	var calls [][]string
	k := fakeKitty("", &calls)
	if err := k.Focus(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls[0], " ")
	if got != "focus-window --match id:10" {
		t.Fatalf("focus args: %q", got)
	}
}

func TestKittyPreviewTail(t *testing.T) {
	text := "line1\nline2\n\nline3\nline4\n\n\n"
	var calls [][]string
	k := fakeKitty(text, &calls)
	out, err := k.Preview(context.Background(), 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if out != "line3\nline4" {
		t.Fatalf("preview tail: %q", out)
	}
	got := strings.Join(calls[0], " ")
	if got != "get-text --match id:4 --extent screen --ansi" {
		t.Fatalf("preview args: %q", got)
	}
}

func TestKittyPreviewCapturesLogicalLines(t *testing.T) {
	// The preview reflows to the pane width in the view, so the capture must
	// hand back logical lines: no --add-wrap-markers, letting kitty rejoin
	// soft-wrapped screen rows into one line we can re-wrap.
	var calls [][]string
	k := fakeKitty("one logical line\n", &calls)
	out, err := k.Preview(context.Background(), 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls[0], " "); strings.Contains(got, "--add-wrap-markers") {
		t.Fatalf("capture should not request wrap markers: %q", got)
	}
	// A logical line comes through intact (not split into rows).
	if out != "one logical line" {
		t.Fatalf("logical line not preserved: %q", out)
	}
}

func TestKittySendTextCommand(t *testing.T) {
	var stdin string
	var args []string
	k := &Kitty{runIn: func(_ context.Context, in string, a ...string) ([]byte, error) {
		stdin, args = in, a
		return nil, nil
	}}
	if err := k.SendText(context.Background(), 10, "2\r"); err != nil {
		t.Fatal(err)
	}
	if stdin != "2\r" {
		t.Fatalf("stdin = %q, want %q", stdin, "2\r")
	}
	if got := strings.Join(args, " "); got != "send-text --match id:10 --stdin" {
		t.Fatalf("send-text args: %q", got)
	}
	if !k.Capabilities().Has(CapSendText) {
		t.Fatal("kitty should advertise CapSendText")
	}
}

func TestKittyPreviewKeepsColor(t *testing.T) {
	// A colored line, a line that is only escape codes (visually blank), and a
	// plain line. The blank one must be dropped; the colors must survive.
	text := "\x1b[32mgreen\x1b[m\n\x1b[m   \nplain\n"
	k := fakeKitty(text, nil)
	out, err := k.Preview(context.Background(), 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	if out != "\x1b[32mgreen\x1b[m\nplain" {
		t.Fatalf("preview with color: %q", out)
	}
}

func TestKittyLaunchCommand(t *testing.T) {
	var calls [][]string
	k := fakeKitty("", &calls)
	if err := k.Launch(context.Background(), "/work/acme"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls[0], " ")
	if got != "launch --type=tab --keep-focus --cwd=/work/acme claude" {
		t.Fatalf("launch args: %q", got)
	}
}

func TestKittyAdvertisesLaunch(t *testing.T) {
	if !NewKitty().Capabilities().Has(CapLaunch) {
		t.Fatal("kitty should advertise CapLaunch")
	}
}
