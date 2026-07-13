// Package ports defines the interfaces ("ports" in hexagonal-architecture
// terms) that connect the core search use case to its adapters:
// document extractors (one per office format plus plain text), pattern
// matchers, the filesystem walker, and output sinks.
//
// This package is the stable contract that format plugins (docx, pptx,
// xlsx) are built against. Changing method signatures here requires
// coordinating with every adapter implementation.
package ports

import (
	"context"
	"io"

	"officegrep/internal/core/domain"
)

// DocumentExtractor knows how to recognize and extract text from one
// document format (e.g. docx, pptx, xlsx, or plain text).
//
// Implementations MUST stream their output via token-by-token XML
// decoding or other incremental parsing — they must never buffer a full
// in-memory DOM of the document. Extract must close the units channel
// when extraction is complete OR when ctx is cancelled, and must send at
// most one error on the error channel before closing it.
type DocumentExtractor interface {
	// Name returns a short identifier for the format, e.g. "docx".
	Name() string

	// Sniff reports whether the file at path (readable via ra, with
	// total size size) is a document this extractor can handle. Sniff
	// should inspect actual file contents (e.g. the zip central
	// directory, [Content_Types].xml, or specific internal part paths)
	// rather than relying solely on the file extension, since files may
	// be renamed.
	Sniff(path string, ra io.ReaderAt, size int64) bool

	// Extract streams the document's text as a sequence of TextUnits.
	// The returned channels are owned by Extract: it must close the
	// units channel when done (including on ctx cancellation) and send
	// at most one error before closing the error channel.
	Extract(ctx context.Context, ra io.ReaderAt, size int64) (<-chan domain.TextUnit, <-chan error)
}

// Matcher finds all matches of a compiled pattern within a string,
// returning their byte-offset spans.
type Matcher interface {
	FindAll(text string) []domain.Span
}

// MatcherFactory compiles a pattern string plus search options into a
// Matcher. Implementations choose the fastest matching strategy that
// satisfies the requested options (e.g. a literal byte-search fast path
// when FixedStrings is set or the pattern has no regex metacharacters).
type MatcherFactory interface {
	Compile(pattern string, opts domain.SearchOptions) (Matcher, error)
}

// FileWalker enumerates candidate file paths under the given root
// directories/files, honoring ignore rules and include/exclude globs
// from opts. It streams paths on the returned channel and must close it
// when done (including on ctx cancellation), sending at most one error
// on the error channel before closing it.
type FileWalker interface {
	Walk(ctx context.Context, roots []string, opts domain.SearchOptions) (<-chan string, <-chan error)
}

// OutputSink renders matches (and per-file summaries) to their
// destination (terminal, JSON lines, etc). WriteMatch and
// WriteFileSummary may be called concurrently from multiple goroutines
// by the orchestrator's per-file workers; implementations must
// synchronize internally if they are not otherwise safe for concurrent
// use.
type OutputSink interface {
	WriteMatch(m domain.Match) error
	WriteFileSummary(path string, matchCount int) error
	Flush() error
}
