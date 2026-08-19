package ui

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"github.com/mattn/go-runewidth"
)

// claudeTermMsg signals fresh output from a claude terminal session.
type claudeTermMsg struct{ dir string }

// claudeSession is an interactive `claude` running in a pty, mirrored into
// the claude pane through a vt10x virtual terminal.
type claudeSession struct {
	dir    string
	cmd    *exec.Cmd
	pty    *os.File
	vt     vt10x.Terminal
	notify chan struct{}
	exited atomic.Bool
}

// startTerm is swappable so tests don't spawn real claude processes.
var startTerm = startClaudeSession

func startClaudeSession(dir string, cols, rows int) (*claudeSession, error) {
	if cols < 20 {
		cols = 20
	}
	if rows < 5 {
		rows = 5
	}
	cmd := exec.Command("claude")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	p, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("starting claude: %w", err)
	}
	s := &claudeSession{
		dir:    dir,
		cmd:    cmd,
		pty:    p,
		vt:     vt10x.New(vt10x.WithSize(cols, rows)),
		notify: make(chan struct{}, 1),
	}
	go func() {
		buf := make([]byte, 8192)
		var pending []byte
		for {
			n, err := p.Read(buf)
			if n > 0 {
				data := append(pending, buf[:n]...)
				complete := data
				pending = nil
				// hold back a trailing unfinished escape sequence so the
				// sanitizer sees whole sequences (unless it grows absurd)
				if i := incompleteEscAt(data); i >= 0 && len(data)-i < 512 {
					complete = data[:i]
					pending = append([]byte(nil), data[i:]...)
				}
				if len(complete) > 0 {
					s.vt.Write(privateCSI.ReplaceAll(complete, nil)) // vt10x locks internally
					s.ping()
				}
			}
			if err != nil {
				if len(pending) > 0 {
					s.vt.Write(privateCSI.ReplaceAll(pending, nil))
				}
				s.exited.Store(true)
				s.ping()
				_ = cmd.Wait()
				return
			}
		}
	}()
	return s, nil
}

// privateCSI matches <, > and = prefixed CSI sequences (kitty keyboard
// protocol, XTVERSION, modifyOtherKeys, …). vt10x doesn't know the prefixes
// and misreads e.g. the final "u" of "ESC[<u" as an ANSI.SYS cursor restore,
// homing the cursor and scrambling claude's relative-movement redraws.
var privateCSI = regexp.MustCompile("\x1b\\[[<>=][0-9;]*[a-zA-Z@`~]")

// incompleteEscAt returns the index of a trailing unterminated escape
// sequence, or -1 when the buffer ends cleanly.
func incompleteEscAt(b []byte) int {
	i := len(b) - 1
	for ; i >= 0 && len(b)-i < 512; i-- {
		if b[i] == 0x1b {
			break
		}
	}
	if i < 0 || b[i] != 0x1b {
		return -1
	}
	rest := b[i+1:]
	if len(rest) == 0 {
		return i // bare ESC at the end
	}
	switch rest[0] {
	case '[': // CSI: complete once a final byte 0x40–0x7e appears
		for _, c := range rest[1:] {
			if c >= 0x40 && c <= 0x7e {
				return -1
			}
		}
		return i
	case ']': // OSC: terminated by BEL or ST (ESC \)
		for j := 1; j < len(rest); j++ {
			if rest[j] == 0x07 || (rest[j] == '\\' && rest[j-1] == 0x1b) {
				return -1
			}
		}
		return i
	case '(', ')': // charset designation: one more byte
		if len(rest) >= 2 {
			return -1
		}
		return i
	}
	return -1 // two-byte escape, already complete
}

func (s *claudeSession) ping() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// waitClaudeTerm re-arms after every claudeTermMsg so output keeps flowing.
func waitClaudeTerm(s *claudeSession) tea.Cmd {
	return func() tea.Msg {
		<-s.notify
		return claudeTermMsg{dir: s.dir}
	}
}

func (s *claudeSession) resize(cols, rows int) {
	if cols < 20 || rows < 5 {
		return
	}
	_ = pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	s.vt.Resize(cols, rows)
}

func (s *claudeSession) close() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pty.Close()
}

// render draws the virtual terminal as ANSI-styled lines sized to the grid.
func (s *claudeSession) render(focused bool) string {
	s.vt.Lock()
	defer s.vt.Unlock()
	cols, rows := s.vt.Size()
	cur := s.vt.Cursor()
	showCursor := focused && s.vt.CursorVisible() && !s.exited.Load()

	var b strings.Builder
	for y := 0; y < rows; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		last := ""
		skip := false
		for x := 0; x < cols; x++ {
			if skip { // second cell of a wide rune
				skip = false
				continue
			}
			g := s.vt.Cell(x, y)
			sgr := sgrFor(g, showCursor && x == cur.X && y == cur.Y)
			if sgr != last {
				b.WriteString("\x1b[0m" + sgr)
				last = sgr
			}
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
			if runewidth.RuneWidth(ch) == 2 {
				skip = true
			}
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// sgrFor builds the escape prefix for one cell. vt10x attr bits: reverse=1,
// underline=2, bold=4; colors <256 are palette, otherwise packed RGB.
func sgrFor(g vt10x.Glyph, cursor bool) string {
	var p []string
	if g.Mode&1 != 0 || cursor {
		p = append(p, "7")
	}
	if g.Mode&2 != 0 {
		p = append(p, "4")
	}
	if g.Mode&4 != 0 {
		p = append(p, "1")
	}
	if c := g.FG; c != vt10x.DefaultFG && c != vt10x.DefaultCursor {
		if c < 256 {
			p = append(p, fmt.Sprintf("38;5;%d", c))
		} else if c < 1<<24 {
			p = append(p, fmt.Sprintf("38;2;%d;%d;%d", c>>16&0xff, c>>8&0xff, c&0xff))
		}
	}
	if c := g.BG; c != vt10x.DefaultBG {
		if c < 256 {
			p = append(p, fmt.Sprintf("48;5;%d", c))
		} else if c < 1<<24 {
			p = append(p, fmt.Sprintf("48;2;%d;%d;%d", c>>16&0xff, c>>8&0xff, c&0xff))
		}
	}
	if len(p) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(p, ";") + "m"
}

// encodeKey turns a bubbletea key event into the bytes a pty expects.
func encodeKey(k tea.KeyMsg) []byte {
	switch k.Type {
	case tea.KeyRunes:
		if k.Alt {
			return append([]byte{0x1b}, []byte(string(k.Runes))...)
		}
		return []byte(string(k.Runes))
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	}
	if k.Type >= 0 && k.Type < 32 { // ctrl+a … ctrl+z and friends
		return []byte{byte(k.Type)}
	}
	return nil
}
