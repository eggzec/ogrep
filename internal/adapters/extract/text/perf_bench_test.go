package text

// Performance/memory verification for the plain-text extractor,
// additive to the existing text_test.go suite. See
// internal/adapters/extract/docx/perf_bench_test.go for the fuller
// rationale behind the two pieces added here: a testing.B benchmark
// over a realistically large (multi-megabyte) text file, and a regular
// Test that quantitatively checks peak resident memory during Extract
// stays roughly flat as file size grows, rather than scaling with it --
// confirming bufio.Scanner-based line-at-a-time streaming really does
// avoid holding the whole file's lines in memory at once.

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildTextFixture renders nLines lines of realistic filler text.
func buildTextFixture(nLines int) []byte {
	var sb strings.Builder
	sb.Grow(nLines * 48)
	for i := 0; i < nLines; i++ {
		fmt.Fprintf(&sb, "line %d contains some filler words, mentioning apple and orange but rarely the needle\n", i)
	}
	return []byte(sb.String())
}

// drainExtract runs Extract to completion, discarding line text, and
// returns the number of units emitted.
func drainExtract(tb testing.TB, data []byte) int {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	units, errc := (Extractor{}).Extract(ctx, bytes.NewReader(data), int64(len(data)))
	n := 0
	for range units {
		n++
	}
	if err := <-errc; err != nil {
		tb.Fatalf("unexpected extraction error: %v", err)
	}
	if ctx.Err() != nil {
		tb.Fatalf("extraction did not complete before the test timeout")
	}
	return n
}

// BenchmarkExtractLargeFile measures a full Extract pass over a
// multi-megabyte text file.
func BenchmarkExtractLargeFile(b *testing.B) {
	const nLines = 300_000 // ~15MB
	data := buildTextFixture(nLines)
	b.SetBytes(int64(len(data)))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n := drainExtract(b, data); n != nLines {
			b.Fatalf("got %d lines, want %d", n, nLines)
		}
	}
}

// samplePeakLiveHeapBytes runs fn while periodically FORCING a garbage
// collection and reading runtime.MemStats.HeapAlloc immediately
// afterward, so each sample reflects the true live heap at that instant
// rather than however much garbage Go's GC pacer happens to be tolerating
// at the time. See the docx package's identical helper
// (internal/adapters/extract/docx/perf_bench_test.go) for the fuller
// rationale (including the empirically-observed false positive this
// fixes -- without forced per-sample GCs, a large already-live fixture
// object makes the default GOGC=100 pacer tolerate a "peak" that tracks
// the fixture's own size, not what the operation under test actually
// needs). Duplicated here rather than shared so each format plugin's
// benchmark file stays a self-contained, independently buildable
// addition.
func samplePeakLiveHeapBytes(fn func()) int64 {
	liveHeapBytes := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}

	runtime.GC()
	runtime.GC()
	baseline := liveHeapBytes()
	peak := baseline

	stop := make(chan struct{})
	done := make(chan uint64)
	go func() {
		localPeak := baseline
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if v := liveHeapBytes(); v > localPeak {
					localPeak = v
				}
			case <-stop:
				done <- localPeak
				return
			}
		}
	}()

	fn()

	close(stop)
	if v := <-done; v > peak {
		peak = v
	}

	if peak < baseline {
		return 0
	}
	return int64(peak - baseline)
}

// TestExtractPeakMemoryBoundedNotLinear runs Extract on a ~300-line file
// and a ~300,000-line file (1000x) and asserts the large run's peak
// heap-objects delta is NOT anywhere near 1000x the small run's -- which
// is what an implementation that read the whole file into memory (e.g.
// io.ReadAll + strings.Split) before returning any lines would show.
//
// Threshold reasoning: mirrors the docx/pptx/xlsx packages' identical
// tests. bufio.Scanner-based line streaming's peak resident memory is
// dominated by its internal buffer (bounded by maxLineSize, a constant)
// plus the one line currently being handed to the caller, so we'd
// expect the ratio to be close to 1x, not 1000x. We use 50x as the
// pass/fail line: generous enough to absorb GC-scheduling and
// sampler-polling noise (especially given a small-run baseline that can
// be just tens of KB), while still cleanly rejecting anything close to
// the ~1000x a whole-file-buffering implementation would produce. The
// small-run baseline is floored before computing the ratio so a
// near-zero measurement can't make the ratio blow up spuriously, and an
// absolute cap is asserted too, independent of the (noisier) small-run
// baseline.
func TestExtractPeakMemoryBoundedNotLinear(t *testing.T) {
	const smallLines = 300
	const largeLines = 300_000 // 1000x smallLines

	smallData := buildTextFixture(smallLines)
	largeData := buildTextFixture(largeLines)

	var smallUnits, largeUnits int
	smallDelta := samplePeakLiveHeapBytes(func() { smallUnits = drainExtract(t, smallData) })
	largeDelta := samplePeakLiveHeapBytes(func() { largeUnits = drainExtract(t, largeData) })

	t.Logf("text peak heap-objects delta: small (%d units, %d bytes file) = %d bytes, large (%d units, %d bytes file) = %d bytes",
		smallUnits, len(smallData), smallDelta, largeUnits, len(largeData), largeDelta)

	const maxRatio = 50
	floor := smallDelta
	if floor < 64*1024 {
		floor = 64 * 1024
	}
	if largeDelta > int64(maxRatio)*floor {
		t.Errorf("large-file peak heap delta (%d bytes) is more than %dx the small-file delta (%d bytes, floored at %d); "+
			"this suggests memory use scales with file size rather than staying bounded",
			largeDelta, maxRatio, smallDelta, floor)
	}

	// Independent absolute sanity cap: a line-streaming extractor
	// processing a ~15MB, 300,000-line file should never need anywhere
	// near this much resident heap at any single point in time. Reading
	// the whole file into memory first (io.ReadAll/strings.Split) would
	// need at least the file's own size (~15MB) resident simultaneously,
	// well past this cap.
	const absoluteCapBytes = 8 * 1024 * 1024
	if largeDelta > absoluteCapBytes {
		t.Errorf("large-file peak heap delta = %d bytes, want <= %d bytes (%d MiB)",
			largeDelta, absoluteCapBytes, absoluteCapBytes/(1024*1024))
	}
}
