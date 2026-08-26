package ui

import (
	"path/filepath"
	"strings"
)

// File-type icons for the ide pane's explorer, tabs and search results.
//
// The glyphs live in the Nerd Font private use area — the same set nvim's
// devicons draw from — because that is what a file tree's icons are in
// practice. A terminal without a Nerd Font renders them as boxes, so
// `file_icons: false` in the config puts the plain tree back
// (see config.Config.Icons).
//
// They are deliberately monochrome: the row's own colour already says
// selected, open, or dim, and a second colour system fighting that would make
// the tree louder rather than clearer. The shape carries the type.

const (
	iconFolder     = "" //
	iconFolderOpen = "" //
	iconFile       = "" //
	iconGo         = ""
	iconRuby       = ""
	iconPython     = ""
	iconJS         = ""
	iconTS         = ""
	iconRust       = ""
	iconMarkdown   = ""
	iconJSON       = ""
	iconHTML       = ""
	iconCSS        = ""
	iconShell      = ""
	iconGit        = ""
	iconDocker     = ""
	iconConfig     = ""
	iconLock       = ""
	iconImage      = ""
)

// ideExtIcons maps a lowercased extension to its glyph.
var ideExtIcons = map[string]string{
	".go":      iconGo,
	".mod":     iconGo,
	".sum":     iconGo,
	".rb":      iconRuby,
	".rake":    iconRuby,
	".gemspec": iconRuby,
	".py":      iconPython,
	".js":      iconJS,
	".mjs":     iconJS,
	".cjs":     iconJS,
	".jsx":     iconJS,
	".ts":      iconTS,
	".tsx":     iconTS,
	".rs":      iconRust,
	".md":      iconMarkdown,
	".mdx":     iconMarkdown,
	".json":    iconJSON,
	".html":    iconHTML,
	".erb":     iconHTML,
	".htm":     iconHTML,
	".css":     iconCSS,
	".scss":    iconCSS,
	".sass":    iconCSS,
	".sh":      iconShell,
	".bash":    iconShell,
	".zsh":     iconShell,
	".fish":    iconShell,
	".yml":     iconConfig,
	".yaml":    iconConfig,
	".toml":    iconConfig,
	".ini":     iconConfig,
	".conf":    iconConfig,
	".env":     iconConfig,
	".lock":    iconLock,
	".png":     iconImage,
	".jpg":     iconImage,
	".jpeg":    iconImage,
	".gif":     iconImage,
	".svg":     iconImage,
	".webp":    iconImage,
	".ico":     iconImage,
}

// ideNameIcons maps whole filenames — the ones whose type lives in the name
// rather than an extension — to their glyph.
var ideNameIcons = map[string]string{
	"dockerfile":         iconDocker,
	"docker-compose.yml": iconDocker,
	".dockerignore":      iconDocker,
	".gitignore":         iconGit,
	".gitattributes":     iconGit,
	".gitmodules":        iconGit,
	".gitkeep":           iconGit,
	"makefile":           iconConfig,
	"rakefile":           iconRuby,
	"gemfile":            iconRuby,
	"go.mod":             iconGo,
	"go.sum":             iconGo,
}

// ideFileIcon is the glyph for one path: directories fold open and shut, a
// known name wins over the extension, and anything unrecognised is a plain
// file. The empty string means icons are switched off, and every caller
// treats that as "draw nothing".
func (m Model) ideFileIcon(rel string, dir, expanded bool) string {
	if m.cfg == nil || !m.cfg.Icons() {
		return ""
	}
	if dir {
		if expanded {
			return iconFolderOpen
		}
		return iconFolder
	}
	name := strings.ToLower(filepath.Base(rel))
	if ic, ok := ideNameIcons[name]; ok {
		return ic
	}
	if ic, ok := ideExtIcons[filepath.Ext(name)]; ok {
		return ic
	}
	return iconFile
}

// ideIconCell is the icon plus its trailing space, ready to sit in a row —
// or nothing at all when icons are off, so the row closes up rather than
// leaving a gap.
func (m Model) ideIconCell(rel string, dir, expanded bool) string {
	if ic := m.ideFileIcon(rel, dir, expanded); ic != "" {
		return ic + " "
	}
	return ""
}
