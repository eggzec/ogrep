package match

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"officegrep/internal/core/domain"
)

// literalMatcher finds all non-overlapping occurrences of a fixed
// substring, optionally case-insensitively and/or restricted to whole
// words. It never compiles a regexp: matching is strings.Index-based,
// which is both faster and immune to regex metacharacter surprises.
type literalMatcher struct {
	pattern    string
	ignoreCase bool
	wholeWord  bool
}

// newLiteralMatcher builds a literalMatcher. When ignoreCase is set, the
// pattern is lower-cased up front (using Unicode-aware case folding) and
// matching lower-cases text as it scans, which keeps the common ASCII
// case cheap while still being correct for non-ASCII text.
func newLiteralMatcher(pattern string, ignoreCase, wholeWord bool) *literalMatcher {
	p := pattern
	if ignoreCase {
		p = strings.ToLower(p)
	}
	return &literalMatcher{pattern: p, ignoreCase: ignoreCase, wholeWord: wholeWord}
}

// FindAll implements ports.Matcher.
func (m *literalMatcher) FindAll(text string) []domain.Span {
	if m.pattern == "" {
		return nil
	}

	haystack := text
	if m.ignoreCase {
		haystack = strings.ToLower(text)
	}

	var spans []domain.Span
	offset := 0
	for {
		idx := strings.Index(haystack[offset:], m.pattern)
		if idx < 0 {
			break
		}
		start := offset + idx
		end := start + len(m.pattern)
		if !m.wholeWord || isWholeWord(text, start, end) {
			spans = append(spans, domain.Span{Start: start, End: end})
		}
		// Advance past this match, non-overlapping, matching the
		// conventional grep/regexp.FindAllIndex semantics.
		offset = end
		if start == end {
			offset++ // guard against an empty pattern causing no progress
		}
		if offset > len(haystack) {
			break
		}
	}
	return spans
}

// isWholeWord reports whether text[start:end] is bounded on both sides
// by non-word-rune boundaries (or the start/end of the string), using
// Unicode letter/digit/underscore as the definition of a "word" rune.
func isWholeWord(text string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if isWordRune(r) {
			return false
		}
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if isWordRune(r) {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
