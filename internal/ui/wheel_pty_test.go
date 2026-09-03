package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// paneWith starts a session running script and pumps it the way the app does:
// pty into the emulator, and the emulator's replies back out to the pty.
func paneWith(t *testing.T, script string) *agentSession {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	p, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 40, Rows: 10})
	if err != nil {
		t.Fatal(err)
	}
	em := vt.NewEmulator(40, 10)
	em.SetScrollbackSize(500)
	s := &agentSession{dir: t.TempDir(), cmd: cmd, pty: p, em: em, cols: 40, rows: 10, notify: make(chan struct{}, 1)}
	s.trackMouseModes()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				s.mu.Lock()
				em.Write(buf[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { _, _ = io.Copy(p, em) }()
	t.Cleanup(func() { _ = p.Close() })
	return s
}

// TestWheelReachesFullScreenProgram: agent runs full-screen, repainting in
// place, so no line ever scrolls off into our scrollback and scrollBy has
// nothing to move. The wheel has to go to the program instead.
func TestWheelReachesFullScreenProgram(t *testing.T) {
	if _, err := exec.LookPath("stty"); err != nil {
		t.Skip("no stty")
	}
	dir := t.TempDir()
	sink := filepath.Join(dir, "stdin.bin")
	// raw mode, as a real TUI sets for itself: otherwise the line discipline
	// holds the mouse report back waiting for a newline that never comes
	s := paneWith(t, fmt.Sprintf(
		`stty raw -echo; printf '\033[?1049h\033[?1000h\033[?1006h'; dd of=%q bs=1 count=10 2>/dev/null`, sink))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		alt := s.em.IsAltScreen()
		s.mu.Unlock()
		if alt {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	alt, sb := s.em.IsAltScreen(), s.em.ScrollbackLen()
	s.mu.Unlock()
	if !alt {
		t.Fatal("child never entered the alternate screen")
	}
	if sb != 0 {
		t.Fatalf("scrollback is %d, expected a full-screen program to leave none", sb)
	}
	// the old behaviour, for the record: nothing to scroll
	if got := s.scrollBy(3); got != 0 {
		t.Errorf("scrollBy on an empty scrollback moved to %d", got)
	}

	if !s.sendWheel(true, 5, 3) {
		t.Fatal("sendWheel declined a full-screen program")
	}
	// SGR report: button 64 is wheel-up, at one-based column 6, row 4
	const want = "\x1b[<64;6;4M"
	var got []byte
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		// wait for the whole report: dd writes it a byte at a time
		if b, err := os.ReadFile(sink); err == nil && len(b) >= len(want) {
			got = b
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(got) != want {
		t.Errorf("child received %q, want %q", got, want)
	}
}

// TestWheelScrollsPlainProgramScrollback: a program that leaves its output on
// the normal screen does build scrollback, and the wheel keeps moving it
// rather than being handed over.
func TestWheelScrollsPlainProgramScrollback(t *testing.T) {
	s := paneWith(t, "for i in $(seq 1 60); do echo line-$i; done; sleep 5")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := s.em.ScrollbackLen()
		s.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	alt, sb := s.em.IsAltScreen(), s.em.ScrollbackLen()
	s.mu.Unlock()
	if alt {
		t.Fatal("plain program should not be on the alternate screen")
	}
	if sb == 0 {
		t.Fatal("no scrollback captured")
	}
	if s.sendWheel(true, 5, 3) {
		t.Error("sendWheel took an event the scrollback should have handled")
	}
	if got := s.scrollBy(3); got != 3 {
		t.Errorf("scrollBy(3) = %d, want 3", got)
	}
}
