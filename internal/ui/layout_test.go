package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestLayoutModeByWidth pins the widths where the arrangement changes.
func TestLayoutModeByWidth(t *testing.T) {
	for _, tc := range []struct {
		width int
		mode  int
	}{
		{80, layTable}, {109, layTable},
		{110, layStack}, {179, layStack},
		{180, layCols}, {279, layCols},
		{280, layFour}, {400, layFour},
	} {
		m := Model{width: tc.width, height: 40}
		if got := m.layoutMode(); got != tc.mode {
			t.Errorf("%d cols: layout %d, want %d", tc.width, got, tc.mode)
		}
	}
}

// TestColumnLayoutsSitSideBySide: past minCols the issues list stops being a
// strip above the work panes and becomes a column beside them — and past
// minFour the shell steps out from under the git pane into its own column.
// Panes sharing a row means their titles land on the same line.
func TestColumnLayoutsSitSideBySide(t *testing.T) {
	for _, tc := range []struct {
		width       int
		sameRowAs   []string
		belowClaude string
	}{
		{160, []string{"balance", "ide", "git"}, ""}, // stacked: issues on top
		{200, []string{"issues", "balance", "ide", "git"}, "shell"},
		{300, []string{"issues", "balance", "ide", "git", "shell"}, ""},
	} {
		m := withIssues(newTestModel(t, tc.width))
		m.height = 44
		m.resize()
		if !m.selectWorktree(m.wts[1].Path) {
			t.Fatal("no row for the second worktree")
		}
		m.syncPanes()

		var titleRow string
		for _, line := range strings.Split(m.View(), "\n") {
			plain := ansiRE.ReplaceAllString(line, "")
			if strings.Contains(plain, tc.sameRowAs[0]) && strings.Contains(plain, tc.sameRowAs[1]) {
				titleRow = plain
				break
			}
		}
		if titleRow == "" {
			t.Fatalf("%d cols: no line carries %v", tc.width, tc.sameRowAs)
		}
		for _, want := range tc.sameRowAs {
			if !strings.Contains(titleRow, want) {
				t.Errorf("%d cols: %q missing from the title row %q", tc.width, want, titleRow)
			}
		}
		if tc.belowClaude != "" && strings.Contains(titleRow, tc.belowClaude) {
			t.Errorf("%d cols: %q should sit below, not beside: %q", tc.width, tc.belowClaude, titleRow)
		}
	}
}

// TestColumnLayoutsFitTheTerminal: every column layout has to fill the screen
// without spilling over it — an overflowing line wraps and shunts the whole
// panel down, and a short one leaves a gap under the panes.
func TestColumnLayoutsFitTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{180, 30}, {200, 44}, {240, 50}, {300, 60}, {400, 80}} {
		for _, pane := range []int{paneIssues, paneClaude, paneIDE, paneDiff, paneTerm} {
			m := withIssues(newTestModel(t, size[0]))
			m.height = size[1]
			m.resize()
			if !m.selectWorktree(m.wts[1].Path) {
				t.Fatal("no row for the second worktree")
			}
			mm, _ := m.focusPane(pane)
			m = mm.(Model)

			lines := strings.Split(m.View(), "\n")
			for n, line := range lines {
				if w := lipgloss.Width(line); w > size[0] {
					t.Fatalf("%dx%d pane %d: line %d is %d cols wide",
						size[0], size[1], pane, n, w)
				}
			}
			if len(lines) > size[1] {
				t.Errorf("%dx%d pane %d: view is %d lines",
					size[0], size[1], pane, len(lines))
			}
		}
	}
}

// TestColumnLayoutPanesShareAHeight: the columns are joined side by side, so a
// pane taller than its neighbours would pad them and leave the row ragged.
func TestColumnLayoutPanesShareAHeight(t *testing.T) {
	for _, width := range []int{180, 200, 240, 300} {
		m := withIssues(newTestModel(t, width))
		m.height = 44
		m.resize()
		l := m.layout()

		issues := len(strings.Split(frame(band{{m.issuesPart("issues & worktrees", true)}}), "\n"))
		if issues != l.issues.h {
			t.Errorf("%d cols: issues column is %d lines, want %d", width, issues, l.issues.h)
		}
		right := l.git.h + l.term.h
		if l.mode == layFour {
			right = l.git.h // its own column, full height
		}
		if right != l.claude.h {
			t.Errorf("%d cols: balance is %d lines, the git column %d", width, l.claude.h, right)
		}
	}
}
