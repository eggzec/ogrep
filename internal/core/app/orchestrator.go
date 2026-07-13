// Package app implements officegrep's core use case: a parallel
// worker-pool search that ties together the FileWalker, the format
// Registry, extractor plugins, a compiled Matcher, and an OutputSink.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"officegrep/internal/core/domain"
	"officegrep/internal/core/ports"
)

// ExtractorLookup resolves the DocumentExtractor for a given file. In
// production this is registry.Registry.For; tests can supply a fake.
type ExtractorLookup interface {
	For(path string, ra io.ReaderAt, size int64) (ports.DocumentExtractor, bool)
}

// SearchOrchestrator implements the "search" use case: walk files in
// parallel, dispatch each to its extractor, match its extracted text,
// and write results to a single OutputSink without interleaving
// different files' output.
type SearchOrchestrator struct {
	Registry ExtractorLookup
	Walker   ports.FileWalker
	Matchers ports.MatcherFactory
	Sink     ports.OutputSink

	// Stderr receives warnings about per-file failures (unreadable
	// files, panics recovered from a misbehaving extractor, etc). If
	// nil, os.Stderr is used.
	Stderr io.Writer

	// writeMu serializes the whole per-file write-out (every WriteMatch
	// call for one file, plus its WriteFileSummary) so that two files'
	// results are never interleaved, even though many files are
	// processed concurrently. The OutputSink implementations are also
	// individually safe for concurrent use; this mutex additionally
	// guarantees a whole file's batch of matches is written atomically
	// as one unit.
	writeMu sync.Mutex
}

// New builds a SearchOrchestrator from its four collaborators.
func New(reg ExtractorLookup, walker ports.FileWalker, matchers ports.MatcherFactory, sink ports.OutputSink) *SearchOrchestrator {
	return &SearchOrchestrator{Registry: reg, Walker: walker, Matchers: matchers, Sink: sink}
}

// Stats summarizes one Run.
type Stats struct {
	FilesWalked   int64
	FilesSearched int64 // files recognized by an extractor and actually searched
	FilesMatched  int64
	TotalMatches  int64
}

// Run walks roots, searches every recognized file for pattern under
// opts, and writes matches to o.Sink. It returns aggregate Stats plus
// the first fatal error encountered (a walker error, or a Compile
// error); per-file problems are logged as warnings to o.Stderr and do
// not fail the whole run.
func (o *SearchOrchestrator) Run(ctx context.Context, pattern string, roots []string, opts domain.SearchOptions) (Stats, error) {
	var stats Stats

	matcher, err := o.Matchers.Compile(pattern, opts)
	if err != nil {
		return stats, fmt.Errorf("compiling pattern: %w", err)
	}

	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	threads := opts.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	if threads < 1 {
		threads = 1
	}

	// runCtx is cancelled either by the caller's ctx or once MaxCount
	// total matches have been found, giving early-exit behavior for a
	// future -m/max-total-matches flag without the walker or extractors
	// needing to know about it.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	paths, walkErrc := o.Walker.Walk(runCtx, roots, opts)

	var wg sync.WaitGroup
	var firstWalkErr error
	var walkErrOnce sync.Once

	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range paths {
				atomic.AddInt64(&stats.FilesWalked, 1)
				o.searchFile(runCtx, path, matcher, opts, &stats, stderr)
			}
		}()
	}

	wg.Wait()

	if err, ok := <-walkErrc; ok && err != nil {
		walkErrOnce.Do(func() { firstWalkErr = err })
	}

	if err := o.Sink.Flush(); err != nil {
		return stats, fmt.Errorf("flushing output: %w", err)
	}

	return stats, firstWalkErr
}

// searchFile handles exactly one file: extractor lookup, streaming
// extraction, matching, and a single atomic write-out of that file's
// results.
//
// The deferred recover() below only catches panics that happen
// synchronously in THIS goroutine — e.g. inside Registry.For/Sniff, or
// inside Matcher.FindAll while we range over units. It does NOT, and
// cannot, catch a panic raised inside an extractor's own Extract
// goroutine (see internal/adapters/extract/text/text.go), since Go's
// recover() only unwinds the goroutine it's deferred in. That case —
// the one most likely to be triggered by malformed XML/zip content in
// the docx/pptx/xlsx plugins — is the extractor's own responsibility to
// guard against, per the contract documented on
// ports.DocumentExtractor: implementations must recover inside their
// Extract goroutine and report the panic as an error on the error
// channel instead of letting it crash the process.
func (o *SearchOrchestrator) searchFile(ctx context.Context, path string, matcher ports.Matcher, opts domain.SearchOptions, stats *Stats, stderr io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "officegrep: warning: panic while searching %s: %v\n", path, r)
		}
	}()

	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "officegrep: warning: %s: %v\n", path, err)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		fmt.Fprintf(stderr, "officegrep: warning: %s: %v\n", path, err)
		return
	}
	size := info.Size()

	extractor, ok := o.Registry.For(path, f, size)
	if !ok {
		return // not a recognized format; silently skip, like rg skipping binaries
	}
	if len(opts.Types) > 0 && !typeAllowed(opts.Types, extractor.Name()) {
		// Filtered out by --type: treat exactly like an unrecognized
		// format (not counted as searched), rather than as a file that
		// was searched and simply produced no matches.
		return
	}
	atomic.AddInt64(&stats.FilesSearched, 1)

	// A per-file context lets us unblock (and let the extractor's
	// goroutine exit and close its channels) if we stop consuming units
	// early, e.g. because MaxCount was reached or the run is being
	// cancelled — without this, the extractor could block forever
	// trying to send its next unit to a reader that's gone away.
	fileCtx, fileCancel := context.WithCancel(ctx)
	defer fileCancel()

	units, extractErrc := extractor.Extract(fileCtx, f, size)

	var matches []domain.Match
	for unit := range units {
		if ctx.Err() != nil {
			break
		}
		spans := matcher.FindAll(unit.Text)
		matched := len(spans) > 0
		if opts.InvertMatch {
			if matched {
				continue
			}
			loc := unit.Location
			loc.Path = path
			matches = append(matches, domain.Match{Location: loc, Text: unit.Text})
			continue
		}
		if !matched {
			continue
		}
		loc := unit.Location
		loc.Path = path
		matches = append(matches, domain.Match{Location: loc, Text: unit.Text, Spans: spans})

		if opts.MaxCount > 0 && len(matches) >= opts.MaxCount {
			break
		}
	}

	if err, ok := <-extractErrc; ok && err != nil {
		fmt.Fprintf(stderr, "officegrep: warning: %s: %v\n", path, err)
	}

	if len(matches) == 0 {
		return
	}

	atomic.AddInt64(&stats.FilesMatched, 1)
	atomic.AddInt64(&stats.TotalMatches, int64(len(matches)))

	o.writeMu.Lock()
	for _, m := range matches {
		if werr := o.Sink.WriteMatch(m); werr != nil {
			fmt.Fprintf(stderr, "officegrep: warning: writing match for %s: %v\n", path, werr)
		}
	}
	if werr := o.Sink.WriteFileSummary(path, len(matches)); werr != nil {
		fmt.Fprintf(stderr, "officegrep: warning: writing summary for %s: %v\n", path, werr)
	}
	o.writeMu.Unlock()
}

// typeAllowed reports whether name (an extractor's Name(), e.g. "docx")
// is one of the values passed via --type.
func typeAllowed(types []string, name string) bool {
	for _, t := range types {
		if t == name {
			return true
		}
	}
	return false
}
