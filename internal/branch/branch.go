// Package branch builds branch and directory names in treeline's
// [feature|bug|fix]/LMAP-NNN/slug-of-title convention.
package branch

import (
	"errors"
	"regexp"
	"strings"
)

var issueKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-\d+$`)

// ParseIssueKey returns the normalized (uppercase) issue key if s looks like
// one (e.g. "lmap-142" -> "LMAP-142"), or "" otherwise.
func ParseIssueKey(s string) string {
	s = strings.TrimSpace(s)
	if issueKeyRe.MatchString(s) {
		return strings.ToUpper(s)
	}
	return ""
}

// Slugify lowercases s, replaces runs of non-alphanumerics with single
// dashes, and truncates to max characters, preferring a word boundary.
func Slugify(s string, max int) string {
	var b []rune
	prevDash := true // also trims leading dashes
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b = append(b, r)
			prevDash = false
		case !prevDash:
			b = append(b, '-')
			prevDash = true
		}
	}
	for len(b) > 0 && b[len(b)-1] == '-' {
		b = b[:len(b)-1]
	}
	if max > 0 && len(b) > max {
		b = b[:max]
		if i := lastDash(b); i > max/2 {
			b = b[:i]
		}
		for len(b) > 0 && b[len(b)-1] == '-' {
			b = b[:len(b)-1]
		}
	}
	return string(b)
}

func lastDash(rs []rune) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == '-' {
			return i
		}
	}
	return -1
}

// Name assembles type/KEY/slug, omitting the slug segment when empty.
func Name(typ, key, slug string) string {
	if slug == "" {
		return typ + "/" + key
	}
	return typ + "/" + key + "/" + slug
}

// DirFor derives the worktree directory name from a branch name:
// "feature/LMAP-142/fix-login" -> "LMAP-142-fix-login" (drops the type
// segment), anything else flattens slashes to dashes.
func DirFor(branchName string) string {
	parts := strings.Split(branchName, "/")
	if len(parts) >= 2 && ParseIssueKey(parts[1]) != "" {
		return strings.Join(parts[1:], "-")
	}
	return strings.ReplaceAll(branchName, "/", "-")
}

// ValidateRef performs a local sanity check on a branch name. Git itself is
// the final authority (worktree add will fail on anything git rejects).
func ValidateRef(name string) error {
	if name == "" {
		return errors.New("branch name is empty")
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return errors.New("branch name cannot start or end with /")
	}
	if strings.HasPrefix(name, "-") {
		return errors.New("branch name cannot start with -")
	}
	if strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".") {
		return errors.New("branch name cannot end with . or .lock")
	}
	for _, bad := range []string{"..", "//", "@{", " ", "\t", "~", "^", ":", "?", "*", "[", "\\"} {
		if strings.Contains(name, bad) {
			return errors.New("branch name cannot contain " + strings.TrimSpace(bad))
		}
	}
	return nil
}
