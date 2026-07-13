// Package walk implements a parallel-pipeline-friendly filesystem
// walker (ports.FileWalker) with .gitignore-style ignore support.
//
// This file implements the ignore-pattern matcher itself: a small,
// hand-rolled subset of the .gitignore pattern language (no third-party
// dependency), supporting `*`, `**`, `!` negation, `#` comments, and
// trailing `/` for directory-only patterns.
package walk

import (
	"path/filepath"
	"strings"
)

// ignorePattern is one parsed line from a .gitignore-style file.
type ignorePattern struct {
	negate   bool     // line started with "!"
	dirOnly  bool     // line ended with "/"
	segments []string // pattern split on "/", possibly prefixed with "**"
}

// parsePattern parses a single raw line from an ignore file. It returns
// ok=false for blank lines and comments, which callers should simply
// skip.
func parsePattern(raw string) (ignorePattern, bool) {
	// Per the gitignore spec, trailing spaces are trimmed unless
	// escaped with a backslash; we keep the simple (unescaped) case.
	line := strings.TrimRight(raw, " \t")
	if line == "" {
		return ignorePattern{}, false
	}
	if strings.HasPrefix(line, "#") {
		return ignorePattern{}, false
	}

	negate := false
	if strings.HasPrefix(line, "!") {
		negate = true
		line = line[1:]
	}
	// Basic support for escaping a leading '!' or '#' with a backslash.
	if strings.HasPrefix(line, `\`) && len(line) > 1 && (line[1] == '!' || line[1] == '#') {
		line = line[1:]
	}
	if line == "" {
		return ignorePattern{}, false
	}

	dirOnly := false
	if strings.HasSuffix(line, "/") {
		dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if line == "" {
		return ignorePattern{}, false
	}

	anchored := false
	if strings.HasPrefix(line, "/") {
		anchored = true
		line = strings.TrimPrefix(line, "/")
	}

	segs := strings.Split(line, "/")
	if len(segs) > 1 {
		// A "/" anywhere but the very end anchors the pattern to the
		// directory containing the ignore file, per gitignore rules.
		anchored = true
	}
	if !anchored {
		segs = append([]string{"**"}, segs...)
	}

	return ignorePattern{negate: negate, dirOnly: dirOnly, segments: segs}, true
}

// match reports whether relPath (slash-separated, relative to the
// ignore file's base directory, no leading slash) matches this pattern.
// isDir indicates whether relPath refers to a directory.
func (p ignorePattern) match(relPath string, isDir bool) bool {
	if p.dirOnly && !isDir {
		return false
	}
	if relPath == "" {
		return false
	}
	segs := strings.Split(relPath, "/")
	return matchSegments(p.segments, segs)
}

// matchSegments matches a pattern (already split on "/", with "**"
// segments retained literally) against a path (also split on "/").
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		if len(pat) == 1 {
			return true // trailing "**" matches everything beneath
		}
		for i := 0; i <= len(path); i++ {
			if matchSegments(pat[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], path[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

// PatternSet is an ordered list of ignore patterns, typically all the
// lines from one ignore file. Later patterns take precedence over
// earlier ones, and a negated pattern ("!pattern") that matches
// un-ignores a path that an earlier pattern ignored — this mirrors
// git's own last-match-wins semantics.
type PatternSet struct {
	patterns []ignorePattern
}

// ParsePatternLines builds a PatternSet from raw lines (as read from an
// ignore file), skipping blank lines and comments.
func ParsePatternLines(lines []string) PatternSet {
	var ps PatternSet
	for _, l := range lines {
		if p, ok := parsePattern(l); ok {
			ps.patterns = append(ps.patterns, p)
		}
	}
	return ps
}

// Empty reports whether the set has no usable patterns.
func (ps PatternSet) Empty() bool { return len(ps.patterns) == 0 }

// Match reports whether relPath (slash-separated, relative to the
// directory this PatternSet was loaded from) is ignored, applying every
// pattern in order and letting the last match win (negation included).
func (ps PatternSet) Match(relPath string, isDir bool) bool {
	ignored := false
	relPath = filepath.ToSlash(relPath)
	for _, p := range ps.patterns {
		if p.match(relPath, isDir) {
			ignored = !p.negate
		}
	}
	return ignored
}
