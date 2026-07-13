package match

import (
	"regexp"

	"github.com/laraibg786/ogrep/internal/core/domain"
)

// regexpMatcher wraps the stdlib regexp package (RE2 semantics: linear
// time, no catastrophic backtracking), matching rg's own safety
// philosophy of never allowing a hostile pattern to hang the search.
type regexpMatcher struct {
	re *regexp.Regexp
}

// FindAll implements ports.Matcher.
func (m *regexpMatcher) FindAll(text string) []domain.Span {
	idx := m.re.FindAllStringIndex(text, -1)
	if idx == nil {
		return nil
	}
	spans := make([]domain.Span, len(idx))
	for i, pair := range idx {
		spans[i] = domain.Span{Start: pair[0], End: pair[1]}
	}
	return spans
}
