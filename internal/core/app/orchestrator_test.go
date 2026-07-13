package app_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"officegrep/internal/adapters/extract/text"
	"officegrep/internal/adapters/match"
	"officegrep/internal/adapters/walk"
	"officegrep/internal/core/app"
	"officegrep/internal/core/domain"
	"officegrep/internal/core/ports"
	"officegrep/internal/registry"
)

// fakeSink collects matches in memory so tests can assert on them
// without depending on the terminal/json adapters.
type fakeSink struct {
	mu      sync.Mutex
	matches []domain.Match
	summary map[string]int
}

func newFakeSink() *fakeSink {
	return &fakeSink{summary: make(map[string]int)}
}

func (s *fakeSink) WriteMatch(m domain.Match) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.matches = append(s.matches, m)
	return nil
}

func (s *fakeSink) WriteFileSummary(path string, count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summary[path] = count
	return nil
}

func (s *fakeSink) Flush() error { return nil }

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestRegistry() *registry.Registry {
	r := registry.New()
	r.Register(text.Extractor{})
	return r
}

func TestOrchestratorEndToEndSearch(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "a.txt"), "hello world\nfoo bar\nHELLO again\n")
	writeFixture(t, filepath.Join(dir, "b.txt"), "nothing to see here\nfoo hello foo\n")
	writeFixture(t, filepath.Join(dir, "sub", "c.txt"), "hello from a subdirectory\n")

	sink := newFakeSink()
	orch := app.New(newTestRegistry(), walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), "hello", []string{dir}, domain.SearchOptions{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if stats.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3 (case-sensitive: a.txt line1, b.txt line2, sub/c.txt line1)", stats.TotalMatches)
	}
	if stats.FilesMatched != 3 {
		t.Errorf("FilesMatched = %d, want 3", stats.FilesMatched)
	}

	var gotPaths []string
	for _, m := range sink.matches {
		gotPaths = append(gotPaths, filepath.Base(m.Location.Path))
		if m.Location.Format != "text" {
			t.Errorf("match location format = %q, want %q", m.Location.Format, "text")
		}
		if m.Location.Path == "" {
			t.Error("expected orchestrator to fill in Location.Path")
		}
	}
	sort.Strings(gotPaths)
	want := []string{"a.txt", "b.txt", "c.txt"}
	if len(gotPaths) != len(want) {
		t.Fatalf("got paths %v, want %v", gotPaths, want)
	}
	for i := range want {
		if gotPaths[i] != want[i] {
			t.Fatalf("got paths %v, want %v", gotPaths, want)
		}
	}
}

func TestOrchestratorIgnoreCase(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "a.txt"), "hello\nHELLO\nHeLLo\nnope\n")

	sink := newFakeSink()
	orch := app.New(newTestRegistry(), walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), "hello", []string{dir}, domain.SearchOptions{IgnoreCase: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3", stats.TotalMatches)
	}
}

func TestOrchestratorNoMatches(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "a.txt"), "nothing relevant here\n")

	sink := newFakeSink()
	orch := app.New(newTestRegistry(), walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), "zzz-not-found", []string{dir}, domain.SearchOptions{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.TotalMatches != 0 || stats.FilesMatched != 0 {
		t.Errorf("expected no matches, got %+v", stats)
	}
	if stats.FilesSearched != 1 {
		t.Errorf("FilesSearched = %d, want 1", stats.FilesSearched)
	}
}

func TestOrchestratorInvertMatch(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "a.txt"), "hello\nworld\nhello again\n")

	sink := newFakeSink()
	orch := app.New(newTestRegistry(), walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), "hello", []string{dir}, domain.SearchOptions{InvertMatch: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.TotalMatches != 1 {
		t.Errorf("TotalMatches = %d, want 1 (only the 'world' line doesn't contain hello)", stats.TotalMatches)
	}
}

func TestOrchestratorRegexPattern(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "a.txt"), "cat\ncot\ncut\ndog\n")

	sink := newFakeSink()
	orch := app.New(newTestRegistry(), walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), "c[aou]t", []string{dir}, domain.SearchOptions{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3", stats.TotalMatches)
	}
}

func TestOrchestratorConcurrentFilesNotInterleaved(t *testing.T) {
	dir := t.TempDir()
	// Many files, each with several matching lines, searched with a
	// worker pool wider than 1, to exercise the "one file's matches
	// are written as one atomic batch" guarantee.
	for i := 0; i < 20; i++ {
		content := "target line 1\nnoise\ntarget line 2\ntarget line 3\n"
		writeFixture(t, filepath.Join(dir, "file"+string(rune('a'+i))+".txt"), content)
	}

	sink := newFakeSink()
	orch := app.New(newTestRegistry(), walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), "target", []string{dir}, domain.SearchOptions{Threads: 8})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.TotalMatches != 60 {
		t.Errorf("TotalMatches = %d, want 60", stats.TotalMatches)
	}

	// Verify matches for the same file are contiguous in the recorded
	// order (i.e. never interleaved with another file's matches).
	seen := make(map[string]bool)
	var lastPath string
	for _, m := range sink.matches {
		if m.Location.Path != lastPath {
			if seen[m.Location.Path] {
				t.Fatalf("file %s's matches were interleaved with another file's", m.Location.Path)
			}
			seen[m.Location.Path] = true
			lastPath = m.Location.Path
		}
	}
}

// TestOrchestratorTypeFilter verifies --type filtering (opts.Types):
// files recognized by an extractor not in the allowed list are skipped
// entirely, and not counted as FilesSearched.
func TestOrchestratorTypeFilter(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "a.txt"), "hello world\n")
	writeFixture(t, filepath.Join(dir, "b.txt"), "hello again\n")

	sink := newFakeSink()
	orch := app.New(newTestRegistry(), walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), "hello", []string{dir}, domain.SearchOptions{Types: []string{"docx"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.FilesSearched != 0 {
		t.Errorf("FilesSearched = %d, want 0 (both files are \"text\", filtered out by --type docx)", stats.FilesSearched)
	}
	if stats.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0", stats.TotalMatches)
	}

	stats, err = orch.Run(context.Background(), "hello", []string{dir}, domain.SearchOptions{Types: []string{"text"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.FilesSearched != 2 {
		t.Errorf("FilesSearched = %d, want 2", stats.FilesSearched)
	}
	if stats.TotalMatches != 2 {
		t.Errorf("TotalMatches = %d, want 2", stats.TotalMatches)
	}
}

// panicExtractor is a ports.DocumentExtractor whose Extract spawns a
// goroutine that panics, following the contract documented on
// ports.DocumentExtractor: it recovers from that panic INSIDE its own
// goroutine and reports it as a single error on the error channel,
// rather than letting the panic propagate (which it cannot do across
// goroutines anyway — recover() only unwinds the goroutine it's
// deferred in).
type panicExtractor struct{}

func (panicExtractor) Name() string { return "panic" }

func (panicExtractor) Sniff(path string, ra io.ReaderAt, size int64) bool { return true }

func (panicExtractor) Extract(ctx context.Context, ra io.ReaderAt, size int64) (<-chan domain.TextUnit, <-chan error) {
	units := make(chan domain.TextUnit)
	errc := make(chan error, 1)

	go func() {
		defer close(units)
		defer close(errc)
		defer func() {
			if r := recover(); r != nil {
				select {
				case errc <- fmt.Errorf("panic during extraction: %v", r):
				default:
				}
			}
		}()

		// Simulate the failure mode malformed XML/zip content is
		// expected to trigger in the docx/pptx/xlsx plugins: an
		// index-out-of-range or nil-dereference partway through
		// streaming decode.
		var bad []int
		_ = bad[5] // panics: index out of range
	}()

	return units, errc
}

// fakeLookup is a minimal ExtractorLookup that always resolves to a
// given extractor, regardless of path/contents.
type fakeLookup struct{ extractor ports.DocumentExtractor }

func (f fakeLookup) For(path string, ra io.ReaderAt, size int64) (ports.DocumentExtractor, bool) {
	return f.extractor, true
}

// TestOrchestratorSurvivesExtractorGoroutinePanic is a regression test
// for a bug where a panic inside a DocumentExtractor's own Extract
// goroutine could crash the whole process: a recover() deferred in a
// DIFFERENT goroutine (e.g. the orchestrator's per-file worker) cannot
// catch a panic raised in this one. The fix requires every extractor to
// recover inside its own goroutine and report the panic via the error
// channel instead (see ports.DocumentExtractor's doc comment and
// internal/adapters/extract/text/text.go for the reference
// implementation). This test exercises that contract end-to-end through
// the real SearchOrchestrator.Run and asserts the run completes
// normally, with the panic surfaced as a logged warning, instead of
// crashing the test binary.
func TestOrchestratorSurvivesExtractorGoroutinePanic(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "corrupt.docx"), "this content is irrelevant; panicExtractor always claims it")

	var stderr bytes.Buffer
	sink := newFakeSink()
	orch := app.New(fakeLookup{extractor: panicExtractor{}}, walk.New(), match.NewFactory(), sink)
	orch.Stderr = &stderr

	stats, err := orch.Run(context.Background(), "anything", []string{dir}, domain.SearchOptions{})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (per-file failures must not fail the whole run)", err)
	}
	if stats.TotalMatches != 0 {
		t.Errorf("TotalMatches = %d, want 0 (the panicking file produced no units)", stats.TotalMatches)
	}
	if !strings.Contains(stderr.String(), "panic") {
		t.Errorf("expected the panic to be logged as a warning to Stderr, got %q", stderr.String())
	}
}
