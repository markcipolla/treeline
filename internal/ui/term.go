package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// claudeTermMsg signals fresh output from a claude terminal session.
type claudeTermMsg struct{ dir string }

// claudeSession is an interactive `claude` running in a pty, mirrored into
// the claude pane through a virtual terminal with scrollback.
type claudeSession struct {
	dir    string
	cmd    *exec.Cmd
	pty    *os.File
	mu     sync.Mutex // guards em and scroll
	em     *vt.Emulator
	rows   int
	cols   int
	scroll int // lines scrolled up into history; 0 = live
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
	em := vt.NewEmulator(cols, rows)
	em.SetScrollbackSize(5000)
	s := &claudeSession{
		dir:    dir,
		cmd:    cmd,
		pty:    p,
		em:     em,
		cols:   cols,
		rows:   rows,
		notify: make(chan struct{}, 1),
	}
	// pty → emulator
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.em.Write(buf[:n])
				s.mu.Unlock()
				s.ping()
			}
			if err != nil {
				s.exited.Store(true)
				s.ping()
				_ = cmd.Wait()
				return
			}
		}
	}()
	// emulator responses (device attributes, cursor reports, …) → pty
	go func() {
		_, _ = io.Copy(p, em)
	}()
	return s, nil
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
	s.mu.Lock()
	if cols == s.cols && rows == s.rows {
		s.mu.Unlock()
		return
	}
	s.cols, s.rows = cols, rows
	s.scroll = 0
	s.em.Resize(cols, rows)
	s.mu.Unlock()
	_ = pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *claudeSession) close() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pty.Close()
	_ = s.em.Close()
}

// scrollBy moves the view into history (positive = older) and returns the
// resulting offset.
func (s *claudeSession) scrollBy(delta int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scroll += delta
	if max := s.em.ScrollbackLen(); s.scroll > max {
		s.scroll = max
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
	return s.scroll
}

func (s *claudeSession) scrollLive() {
	s.mu.Lock()
	s.scroll = 0
	s.mu.Unlock()
}

func (s *claudeSession) scrolled() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scroll
}

// render draws the terminal: the live screen, or a window sliding into the
// scrollback when scrolled up.
func (s *claudeSession) render(focused bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	screen := strings.Split(s.em.Render(), "\n")
	for len(screen) < s.rows {
		screen = append(screen, "")
	}
	screen = screen[:s.rows]

	if s.scroll == 0 {
		if focused && !s.exited.Load() {
			cur := s.em.CursorPosition()
			if cur.Y >= 0 && cur.Y < s.rows {
				screen[cur.Y] = s.renderLineWithCursor(cur.Y, cur.X)
			}
		}
		return strings.Join(screen, "\n")
	}

	sbLen := s.em.ScrollbackLen()
	k := s.scroll
	if k > sbLen {
		k = sbLen
	}
	clip := maxWidthStyle(s.cols)
	lines := make([]string, 0, s.rows)
	for i := sbLen - k; i < sbLen && len(lines) < s.rows; i++ {
		lines = append(lines, clip.Render(s.em.Scrollback().Line(i).Render()))
	}
	for i := 0; len(lines) < s.rows && i < len(screen); i++ {
		lines = append(lines, screen[i])
	}
	for len(lines) < s.rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

var urlRE = regexp.MustCompile(`https?://[^\s"'<>]+`)

// lineAt returns the plain text of the visual row currently shown at y,
// column-aligned (wide cells are padded) so x positions match the display.
func (s *claudeSession) lineAt(y int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scroll > 0 {
		sbLen := s.em.ScrollbackLen()
		k := s.scroll
		if k > sbLen {
			k = sbLen
		}
		idx := sbLen - k + y
		if idx < sbLen {
			if idx < 0 {
				return ""
			}
			return s.em.Scrollback().Line(idx).String()
		}
		y = idx - sbLen // remaining rows come from the live screen top
	}
	if y < 0 || y >= s.rows {
		return ""
	}
	var b strings.Builder
	for x := 0; x < s.cols; {
		c := s.em.CellAt(x, y)
		if c == nil || c.Content == "" {
			b.WriteString(" ")
			x++
			continue
		}
		b.WriteString(c.Content)
		if c.Width > 1 { // pad so rune index keeps matching the column
			b.WriteString(strings.Repeat(" ", c.Width-1))
			x += c.Width
		} else {
			x++
		}
	}
	return b.String()
}

// urlAt finds a URL under column x of the given visual row.
func (s *claudeSession) urlAt(x, y int) string {
	line := s.lineAt(y)
	if line == "" {
		return ""
	}
	for _, span := range urlRE.FindAllStringIndex(line, -1) {
		start := utf8.RuneCountInString(line[:span[0]])
		end := start + utf8.RuneCountInString(line[span[0]:span[1]])
		if x >= start && x < end {
			return strings.TrimRight(line[span[0]:span[1]], ".,;:!?")
		}
	}
	return ""
}

// renderLineWithCursor rebuilds one screen row from cells, reversing the
// cell under the cursor so it reads as a block cursor.
func (s *claudeSession) renderLineWithCursor(y, curX int) string {
	var b strings.Builder
	for x := 0; x < s.cols; {
		c := s.em.CellAt(x, y)
		if c == nil {
			b.WriteString(" ")
			x++
			continue
		}
		st := c.Style
		if x == curX {
			st.Attrs ^= uv.AttrReverse
		}
		content := c.Content
		if content == "" {
			content = " "
		}
		b.WriteString(st.String() + content + "\x1b[0m")
		if c.Width > 1 {
			x += c.Width
		} else {
			x++
		}
	}
	return b.String()
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
