// Package match provides ports.Matcher implementations: a literal
// fixed-string matcher (bytes/strings.Index-based, no regexp
// compilation) and a regexp matcher backed by the stdlib RE2 engine.
// MatcherFactory.Compile picks between them automatically.
package match

import (
	"fmt"
	"regexp"
	"strings"

	"officegrep/internal/core/domain"
	"officegrep/internal/core/ports"
)

// regexMetaChars mirrors the set of bytes that give a string special
// meaning in Go's regexp/syntax. If a pattern contains none of these,
// it behaves identically whether treated as a literal or as a regexp,
// so we can take the literal fast path automatically (as rg does).
const regexMetaChars = `\.+*?()|[]{}^$`

// Factory implements ports.MatcherFactory, compiling a pattern and
// domain.SearchOptions into either a literalMatcher or a regexpMatcher.
type Factory struct{}

// NewFactory returns a ready-to-use Factory.
func NewFactory() Factory { return Factory{} }

// Compile implements ports.MatcherFactory. It uses the literal fast path
// when opts.FixedStrings is set, or automatically when the pattern
// contains no regex metacharacters at all — otherwise it compiles a
// stdlib regexp, honoring IgnoreCase via an "(?i)" prefix and WholeWord
// by wrapping the pattern in `\b...\b`.
func (Factory) Compile(pattern string, opts domain.SearchOptions) (ports.Matcher, error) {
	useLiteral := opts.FixedStrings || !containsRegexMeta(pattern)

	if useLiteral {
		return newLiteralMatcher(pattern, opts.IgnoreCase, opts.WholeWord), nil
	}

	expr := pattern
	if opts.WholeWord {
		expr = `\b(?:` + expr + `)\b`
	}
	if opts.IgnoreCase {
		expr = "(?i)" + expr
	}

	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("compiling pattern %q: %w", pattern, err)
	}
	return &regexpMatcher{re: re}, nil
}

func containsRegexMeta(pattern string) bool {
	return strings.ContainsAny(pattern, regexMetaChars)
}
