package match

import (
	"testing"

	"github.com/laraibg786/ogrep/internal/core/domain"
)

func spansEqual(a, b []domain.Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLiteralMatcherBasic(t *testing.T) {
	m := newLiteralMatcher("cat", false, false)
	got := m.FindAll("the cat sat on the cat mat")
	want := []domain.Span{{Start: 4, End: 7}, {Start: 19, End: 22}}
	if !spansEqual(got, want) {
		t.Errorf("FindAll = %v, want %v", got, want)
	}
}

func TestLiteralMatcherNonOverlapping(t *testing.T) {
	m := newLiteralMatcher("aa", false, false)
	got := m.FindAll("aaaa")
	want := []domain.Span{{Start: 0, End: 2}, {Start: 2, End: 4}}
	if !spansEqual(got, want) {
		t.Errorf("FindAll = %v, want %v (expected non-overlapping matches)", got, want)
	}
}

func TestLiteralMatcherCaseInsensitive(t *testing.T) {
	m := newLiteralMatcher("Cat", true, false)
	got := m.FindAll("CAT cat CaT")
	want := []domain.Span{{Start: 0, End: 3}, {Start: 4, End: 7}, {Start: 8, End: 11}}
	if !spansEqual(got, want) {
		t.Errorf("FindAll = %v, want %v", got, want)
	}
}

func TestLiteralMatcherCaseSensitiveNoMatch(t *testing.T) {
	m := newLiteralMatcher("Cat", false, false)
	got := m.FindAll("cat CAT")
	if len(got) != 0 {
		t.Errorf("FindAll = %v, want no matches (case-sensitive)", got)
	}
}

func TestLiteralMatcherWholeWord(t *testing.T) {
	m := newLiteralMatcher("cat", false, true)
	got := m.FindAll("cat category cat cats concatenate")
	// Only the standalone "cat" tokens should match, not "category",
	// "cats", or the "cat" inside "concatenate".
	want := []domain.Span{{Start: 0, End: 3}, {Start: 13, End: 16}}
	if !spansEqual(got, want) {
		t.Errorf("FindAll = %v, want %v", got, want)
	}
}

func TestLiteralMatcherWholeWordAndCaseInsensitive(t *testing.T) {
	m := newLiteralMatcher("Cat", true, true)
	got := m.FindAll("CAT scatter cat")
	want := []domain.Span{{Start: 0, End: 3}, {Start: 12, End: 15}}
	if !spansEqual(got, want) {
		t.Errorf("FindAll = %v, want %v", got, want)
	}
}

func TestLiteralMatcherEmptyPattern(t *testing.T) {
	m := newLiteralMatcher("", false, false)
	if got := m.FindAll("anything"); got != nil {
		t.Errorf("FindAll with empty pattern = %v, want nil", got)
	}
}

func TestFactoryAutoLiteralWhenNoMetachars(t *testing.T) {
	f := NewFactory()
	m, err := f.Compile("hello world", domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.(*literalMatcher); !ok {
		t.Errorf("expected auto-literal fast path for a plain pattern, got %T", m)
	}
}

func TestFactoryUsesRegexpWhenMetacharsPresent(t *testing.T) {
	f := NewFactory()
	m, err := f.Compile("cat.*dog", domain.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.(*regexpMatcher); !ok {
		t.Errorf("expected regexp matcher for a pattern with metacharacters, got %T", m)
	}
	got := m.FindAll("a cat and a dog")
	if len(got) != 1 {
		t.Errorf("FindAll = %v, want 1 match", got)
	}
}

func TestFactoryFixedStringsForcesLiteralEvenWithMetachars(t *testing.T) {
	f := NewFactory()
	m, err := f.Compile("a.b", domain.SearchOptions{FixedStrings: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.(*literalMatcher); !ok {
		t.Errorf("expected literal matcher when FixedStrings is set, got %T", m)
	}
	// "." should be treated literally, not as "any character".
	got := m.FindAll("a.b axb")
	want := []domain.Span{{Start: 0, End: 3}}
	if !spansEqual(got, want) {
		t.Errorf("FindAll = %v, want %v", got, want)
	}
}

func TestFactoryRegexpIgnoreCase(t *testing.T) {
	f := NewFactory()
	m, err := f.Compile("ca+t", domain.SearchOptions{IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	got := m.FindAll("CAAT and cat")
	if len(got) != 2 {
		t.Errorf("FindAll = %v, want 2 matches", got)
	}
}

func TestFactoryRegexpWholeWord(t *testing.T) {
	f := NewFactory()
	m, err := f.Compile("ca+t", domain.SearchOptions{WholeWord: true})
	if err != nil {
		t.Fatal(err)
	}
	got := m.FindAll("cat category scatter")
	want := []domain.Span{{Start: 0, End: 3}}
	if !spansEqual(got, want) {
		t.Errorf("FindAll = %v, want %v", got, want)
	}
}

func TestFactoryInvalidRegexpReturnsError(t *testing.T) {
	f := NewFactory()
	if _, err := f.Compile("(unclosed", domain.SearchOptions{}); err == nil {
		t.Error("expected an error for an invalid regexp pattern")
	}
}
