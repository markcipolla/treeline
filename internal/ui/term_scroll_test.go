package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// TestScrollbackCapture drives a real pty program through the emulator and
// checks that lines scrolled off the top are reachable by scrolling up.
func TestScrollbackCapture(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	cmd := exec.Command("sh", "-c", "for i in $(seq 1 40); do echo line-$i; done")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	p, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 40, Rows: 10})
	if err != nil {
		t.Fatal(err)
	}
	em := vt.NewEmulator(40, 10)
	em.SetScrollbackSize(500)
	s := &agentSession{dir: t.TempDir(), cmd: cmd, pty: p, em: em, cols: 40, rows: 10, notify: make(chan struct{}, 1)}
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := p.Read(buf)
		if n > 0 {
			em.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	if em.ScrollbackLen() == 0 {
		t.Fatal("no lines captured in scrollback")
	}
	live := s.render(false)
	if !strings.Contains(live, "line-40") {
		t.Fatalf("live screen missing last line: %q", live)
	}
	s.scrollBy(em.ScrollbackLen()) // all the way up
	top := s.render(false)
	if !strings.Contains(top, "line-1") {
		t.Fatalf("scrolled view missing first line, got: %s", fmt.Sprintf("%.200q", top))
	}
	s.scrollLive()
	if got := s.render(false); !strings.Contains(got, "line-40") {
		t.Fatal("scrollLive did not return to the live screen")
	}

	// drag selection: anchor on one live row, extend to the next, and the
	// extracted text spans both lines stream-style
	sbLen := em.ScrollbackLen()
	s.selPress(0, 0)
	s.selDrag(6, 1)
	text, moved := s.selRelease()
	if !moved {
		t.Fatal("drag did not register movement")
	}
	_ = sbLen
	if !strings.Contains(text, "line-3") || !strings.Contains(text, "\n") {
		t.Fatalf("selection text wrong: %q", text)
	}
}
