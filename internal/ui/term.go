package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/markcipolla/treeline/internal/tmux"
)

// claudeTermMsg signals fresh output from an embedded terminal session.
type claudeTermMsg struct{ s *claudeSession }

// claudeSession is an interactive `claude` running in a pty, mirrored into
// the claude pane through a virtual terminal with scrollback.
type claudeSession struct {
	dir string
	// tmuxName is the session on treeline's tmux server backing this pane,
	// empty when the program runs in the pty directly. When set, closing the
	// pane detaches instead of killing, so the work survives quitting.
	tmuxName string
	cmd      *exec.Cmd
	pty      *os.File
	mu       sync.Mutex // guards em and scroll
	em       *vt.Emulator
	rows     int
	cols     int
	scroll   int // lines scrolled up into history; 0 = live
	notify   chan struct{}
	exited   atomic.Bool

	// mouse selection, in absolute coordinates: line < scrollback length is
	// history, line >= is the live screen (index - sbLen)
	selOn    bool // drag in progress
	selMoved bool
	selShown bool // highlight persists after release until cleared
	selA     selPoint
	selB     selPoint
}

// selPoint addresses a cell across scrollback + live screen.
type selPoint struct{ line, col int }

// startTerm and startShell are swappable so tests don't spawn processes.
// persist asks for the program to run inside treeline's tmux server, so the
// session outlives this treeline run.
var (
	startTerm = func(dir string, cols, rows int, persist bool) (*claudeSession, error) {
		return startProgramSession(dir, cols, rows, persist, "claude", "claude")
	}
	startShell = func(dir string, cols, rows int, persist bool) (*claudeSession, error) {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/zsh"
		}
		return startProgramSession(dir, cols, rows, persist, "shell", sh)
	}
)

func startProgramSession(dir string, cols, rows int, persist bool, kind, name string, args ...string) (*claudeSession, error) {
	if cols < 20 {
		cols = 20
	}
	if rows < 5 {
		rows = 5
	}
	var (
		cmd      *exec.Cmd
		tmuxName string
	)
	if persist && tmux.Available() {
		// Attach to (or create) a session on treeline's own tmux server: the
		// process we spawn is only a client, so it can be dropped later
		// without taking the program down with it.
		tmuxName = tmux.Name(kind, dir)
		cmd = tmux.Command(tmuxName, dir, name, args...)
	} else {
		cmd = exec.Command(name, args...)
		cmd.Dir = dir
	}
	// TERM describes the pane's virtual terminal; TMUX is dropped so a
	// treeline running inside tmux can still attach (see tmux.Env).
	cmd.Env = tmux.Env(append(os.Environ(), "TERM=xterm-256color"))
	p, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	em := vt.NewEmulator(cols, rows)
	em.SetScrollbackSize(5000)
	s := &claudeSession{
		dir:      dir,
		tmuxName: tmuxName,
		cmd:      cmd,
		pty:      p,
		em:       em,
		cols:     cols,
		rows:     rows,
		notify:   make(chan struct{}, 1),
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
		return claudeTermMsg{s: s}
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

// close drops the pane. For a tmux-backed session the process being killed is
// only this pane's tmux client, never the server: the claude or shell inside
// keeps running, detached, and the next launch attaches straight back to it.
// (Killing our own client rather than asking tmux to detach-client keeps a
// second treeline attached to the same worktree undisturbed.)
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

// sendWheel hands a wheel event to the program running in the pane, encoded
// the way the terminal would send it. A full-screen program repaints in place
// rather than letting lines scroll off the top, so it builds no scrollback of
// ours to move — the scrolling has to be its own. Reports whether the event
// was the program's to handle; SendMouse is itself a no-op when the program
// never asked for mouse events, and then the wheel simply does nothing, which
// is all an empty scrollback could have offered anyway.
func (s *claudeSession) sendWheel(up bool, x, y int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.em.IsAltScreen() {
		return false
	}
	btn := vt.MouseWheelDown
	if up {
		btn = vt.MouseWheelUp
	}
	s.em.SendMouse(vt.MouseWheel{X: x, Y: y, Button: btn})
	return true
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

// absAt converts pane-relative cell coords into an absolute selection point.
func (s *claudeSession) absAt(x, y int) selPoint {
	if x < 0 {
		x = 0
	}
	if x >= s.cols {
		x = s.cols - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= s.rows {
		y = s.rows - 1
	}
	sbLen := s.em.ScrollbackLen()
	k := s.scroll
	if k > sbLen {
		k = sbLen
	}
	return selPoint{line: sbLen - k + y, col: x}
}

func (s *claudeSession) selPress(x, y int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selA = s.absAt(x, y)
	s.selB = s.selA
	s.selOn, s.selMoved, s.selShown = true, false, true
}

func (s *claudeSession) selDrag(x, y int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.selOn {
		return
	}
	p := s.absAt(x, y)
	if p != s.selB {
		s.selMoved = true
	}
	s.selB = p
}

// selRelease finishes a drag: it returns the selected text (empty for a
// plain click) and whether the pointer actually moved.
func (s *claudeSession) selRelease() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selOn = false
	if !s.selMoved {
		s.selShown = false
		return "", false
	}
	return s.selectedTextLocked(), true
}

func (s *claudeSession) selecting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selOn
}

func (s *claudeSession) clearSel() {
	s.mu.Lock()
	s.selOn, s.selMoved, s.selShown = false, false, false
	s.mu.Unlock()
}

// selBoundsLocked returns the normalized selection, if one is visible.
func (s *claudeSession) selBoundsLocked() (a, b selPoint, ok bool) {
	if !s.selShown {
		return a, b, false
	}
	a, b = s.selA, s.selB
	if a.line > b.line || (a.line == b.line && a.col > b.col) {
		a, b = b, a
	}
	return a, b, true
}

// cellAtAbsLocked reads a cell from scrollback or the live screen.
func (s *claudeSession) cellAtAbsLocked(x, abs int) *uv.Cell {
	sbLen := s.em.ScrollbackLen()
	if abs < sbLen {
		return s.em.ScrollbackCellAt(x, abs)
	}
	return s.em.CellAt(x, abs-sbLen)
}

// lineTextAbsLocked is the column-aligned plain text of an absolute line.
func (s *claudeSession) lineTextAbsLocked(abs int) string {
	var b strings.Builder
	for x := 0; x < s.cols; {
		c := s.cellAtAbsLocked(x, abs)
		if c == nil || c.Content == "" {
			b.WriteString(" ")
			x++
			continue
		}
		b.WriteString(c.Content)
		if c.Width > 1 {
			b.WriteString(strings.Repeat(" ", c.Width-1))
			x += c.Width
		} else {
			x++
		}
	}
	return b.String()
}

func (s *claudeSession) selectedTextLocked() string {
	a, b, ok := s.selBoundsLocked()
	if !ok {
		return ""
	}
	var lines []string
	for l := a.line; l <= b.line; l++ {
		text := []rune(s.lineTextAbsLocked(l))
		from, to := 0, len(text)
		if l == a.line && a.col < len(text) {
			from = a.col
		}
		if l == b.line && b.col+1 < len(text) {
			to = b.col + 1
		}
		if from > to {
			from = to
		}
		lines = append(lines, strings.TrimRight(string(text[from:to]), " "))
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard puts text on the system clipboard.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("pbcopy")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// render draws the terminal: the live screen, or a window sliding into the
// scrollback when scrolled up.
func (s *claudeSession) render(focused bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	sbLen := s.em.ScrollbackLen()
	k := s.scroll
	if k > sbLen {
		k = sbLen
	}
	selA, selB, hasSel := s.selBoundsLocked()

	// highlightAbs rebuilds a visual row from cells when it intersects the
	// selection, reversing the selected span.
	highlightAbs := func(abs int, fallback string) string {
		if !hasSel || abs < selA.line || abs > selB.line {
			return fallback
		}
		from, to := 0, s.cols-1
		if abs == selA.line {
			from = selA.col
		}
		if abs == selB.line {
			to = selB.col
		}
		return s.renderCellsLine(abs, -1, from, to)
	}

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
		for y := range screen {
			screen[y] = highlightAbs(sbLen+y, screen[y])
		}
		return strings.Join(screen, "\n")
	}

	clip := maxWidthStyle(s.cols)
	lines := make([]string, 0, s.rows)
	for i := sbLen - k; i < sbLen && len(lines) < s.rows; i++ {
		lines = append(lines, highlightAbs(i, clip.Render(s.em.Scrollback().Line(i).Render())))
	}
	for i := 0; len(lines) < s.rows && i < len(screen); i++ {
		lines = append(lines, highlightAbs(sbLen+i, screen[i]))
	}
	for len(lines) < s.rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// renderCellsLine rebuilds one absolute line from cells, reversing the
// cursor cell (curX ≥ 0) and/or the selected column span selFrom..selTo.
func (s *claudeSession) renderCellsLine(abs, curX, selFrom, selTo int) string {
	var b strings.Builder
	for x := 0; x < s.cols; {
		c := s.cellAtAbsLocked(x, abs)
		var st uv.Style
		content := " "
		width := 1
		if c != nil {
			st = c.Style
			if c.Content != "" {
				content = c.Content
			}
			if c.Width > 1 {
				width = c.Width
			}
		}
		if x == curX || (selFrom >= 0 && x >= selFrom && x <= selTo) {
			st.Attrs ^= uv.AttrReverse
		}
		b.WriteString(st.String() + content + "\x1b[0m")
		x += width
	}
	return b.String()
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

// renderLineWithCursor rebuilds one screen row with a block cursor.
func (s *claudeSession) renderLineWithCursor(y, curX int) string {
	return s.renderCellsLine(s.em.ScrollbackLen()+y, curX, -1, -1)
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
