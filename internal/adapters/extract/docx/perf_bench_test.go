package docx

// This file adds performance/memory verification for the docx
// extractor, additive to the existing docx_test.go/integration_test.go
// suite (it introduces no new exported behavior and modifies nothing).
// It has two jobs:
//
//  1. A conventional testing.B benchmark over a realistically large
//     synthetic document (several thousand paragraphs, some inside a
//     table), reporting ns/op and, via b.ReportAllocs(), B/op and
//     allocs/op.
//  2. A regular Test that quantitatively checks the package doc
//     comment's claim ("All parsing is streamed via encoding/xml's
//     token API -- no part is ever unmarshalled into a full in-memory
//     DOM"): peak resident heap during Extract must stay roughly flat
//     as document size grows, not scale linearly with it. See
//     samplePeakHeapObjectBytes and TestExtractPeakMemoryBoundedNotLinear
//     below for the method and the chosen threshold's justification.

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// zipFromPartsTB is a copy of docx_test.go's buildDocx, retyped to
// accept testing.TB instead of *testing.T, so it can be called from
// both benchmarks (*testing.B) and the memory-boundedness test
// (*testing.T) below. buildDocx itself can't be reused directly here
// since it's declared against the concrete *testing.T type.
func zipFromPartsTB(tb testing.TB, parts map[string]string) []byte {
	tb.Helper()

	all := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
	}
	for name, content := range parts {
		all[name] = content
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range all {
		w, err := zw.Create(name)
		if err != nil {
			tb.Fatalf("creating zip part %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			tb.Fatalf("writing zip part %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("closing zip writer: %v", err)
	}
	return buf.Bytes()
}

// buildLargeDocxBody renders word/document.xml with nPara plain body
// paragraphs (split evenly before/after) plus one small 5x2 table
// inserted at the midpoint, so both the plain-paragraph path and the
// table/cell-tracking path are exercised at scale.
func buildLargeDocxBody(nPara int) string {
	var sb strings.Builder
	sb.Grow(nPara*100 + 4096)
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document ` + wNS + `><w:body>`)

	half := nPara / 2
	for i := 0; i < half; i++ {
		fmt.Fprintf(&sb, `<w:p><w:r><w:t>paragraph number %d contains some filler words to search through, mentioning apple and orange but rarely the needle</w:t></w:r></w:p>`, i)
	}
	sb.WriteString(`<w:tbl>`)
	for r := 0; r < 5; r++ {
		sb.WriteString(`<w:tr>`)
		for c := 0; c < 2; c++ {
			fmt.Fprintf(&sb, `<w:tc><w:p><w:r><w:t>cell %d-%d filler text</w:t></w:r></w:p></w:tc>`, r, c)
		}
		sb.WriteString(`</w:tr>`)
	}
	sb.WriteString(`</w:tbl>`)
	for i := half; i < nPara; i++ {
		fmt.Fprintf(&sb, `<w:p><w:r><w:t>paragraph number %d contains some filler words to search through, mentioning apple and orange but rarely the needle</w:t></w:r></w:p>`, i)
	}

	sb.WriteString(`</w:body></w:document>`)
	return sb.String()
}

// buildDocxFixture builds a full docx zip package whose body has nPara
// paragraphs (plus the small fixed table from buildLargeDocxBody).
func buildDocxFixture(tb testing.TB, nPara int) []byte {
	tb.Helper()
	return zipFromPartsTB(tb, map[string]string{"word/document.xml": buildLargeDocxBody(nPara)})
}

// drainExtract runs Extract to completion, discarding unit text, and
// returns the number of units emitted. A generous timeout means a bug
// that deadlocks extraction fails the test/benchmark instead of hanging
// the whole suite.
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

// BenchmarkExtractLargeDocument measures a full Extract pass (draining
// the units channel, discarding text) over a docx body with several
// thousand paragraphs, some inside a table -- the realistic-scale
// workload this package's streaming design is meant to handle cheaply.
func BenchmarkExtractLargeDocument(b *testing.B) {
	const nPara = 6000
	data := buildDocxFixture(b, nPara)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n := drainExtract(b, data); n == 0 {
			b.Fatal("expected at least one unit")
		}
	}
}

// samplePeakLiveHeapBytes runs fn while periodically FORCING a garbage
// collection and reading runtime.MemStats.HeapAlloc immediately
// afterward, so each sample reflects the true live heap at that instant
// rather than however much garbage Go's GC pacer happens to be tolerating
// at the time. That distinction matters here: with the default GOGC=100
// pacer, the runtime lets HeapAlloc grow to roughly 2x the live heap size
// measured at the last collection before collecting again -- so without
// forcing collections on every sample, a test whose baseline already
// includes a large resident object (e.g. our own synthetic docx fixture,
// which for the "large" case is several megabytes) would see a "peak"
// dominated by that pacer slack (which scales with the SIZE OF THE
// ALREADY-LIVE FIXTURE, not with what Extract itself needs to hold at
// once) -- a false positive for exactly the failure mode this test
// exists to catch. (This was verified empirically while writing this
// test: without forced per-sample GCs, the large-fixture run showed a
// multi-megabyte "peak" that tracked the fixture's own size almost
// exactly, and vanished once collections were forced before each
// sample.) Forcing a collection before every sample costs some
// benchmark wall-clock time, an acceptable trade in a correctness test.
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

// TestExtractPeakMemoryBoundedNotLinear is the quantitative check behind
// this package's doc-comment claim that extraction never buffers a full
// in-memory DOM. It runs Extract on a 100-paragraph and a
// 100,000-paragraph docx (a 1000x size increase) and asserts the large
// run's peak heap-objects delta over its own baseline is NOT anywhere
// near 1000x the small run's -- which is what a full-DOM (e.g.
// xml.Unmarshal-based) implementation would show, since it would need
// to hold ~1000x as many live paragraph nodes at once.
//
// Threshold reasoning: a properly streaming implementation's peak
// resident memory is dominated by roughly constant-size state (one open
// zip reader/flate decompressor, the in-progress paragraph/table
// builders, and small internal buffers) that does not grow with
// paragraph count -- so we'd expect the ratio to be close to 1x, not
// 1000x. We use 50x as the pass/fail line: generous enough to absorb
// GC-scheduling jitter and the sampler's own polling noise (especially
// on a small-run baseline that can be just a few tens of KB, where
// ordinary allocator/GC noise can look like a large relative swing),
// while still cleanly rejecting anything remotely close to the ~1000x a
// linear-scaling implementation would produce. A floor is applied to
// the small run's baseline before computing the ratio so a
// near-zero small-run measurement can't make the ratio blow up
// spuriously. An absolute cap is asserted too, as a second, independent
// check that doesn't depend on the (noisier) small-run baseline at all.
func TestExtractPeakMemoryBoundedNotLinear(t *testing.T) {
	const smallParagraphs = 100
	const largeParagraphs = 100_000 // 1000x smallParagraphs

	smallData := buildDocxFixture(t, smallParagraphs)
	largeData := buildDocxFixture(t, largeParagraphs)

	var smallUnits, largeUnits int
	smallDelta := samplePeakLiveHeapBytes(func() { smallUnits = drainExtract(t, smallData) })
	largeDelta := samplePeakLiveHeapBytes(func() { largeUnits = drainExtract(t, largeData) })

	t.Logf("docx peak heap-objects delta: small (%d units) = %d bytes, large (%d units) = %d bytes",
		smallUnits, smallDelta, largeUnits, largeDelta)

	const maxRatio = 50
	floor := smallDelta
	if floor < 64*1024 {
		floor = 64 * 1024
	}
	if largeDelta > int64(maxRatio)*floor {
		t.Errorf("large-document peak heap delta (%d bytes) is more than %dx the small-document delta (%d bytes, floored at %d); "+
			"this suggests memory use scales with document size rather than staying bounded (a 1000x paragraph-count increase should not produce anywhere close to a 1000x memory increase)",
			largeDelta, maxRatio, smallDelta, floor)
	}

	// Independent absolute sanity cap: regardless of the small-run
	// measurement's noise, a streaming extractor processing a
	// 100,000-paragraph document should never need anywhere near this
	// much resident heap at any single point in time. A full-DOM
	// implementation holding ~100,000 paragraph nodes (each with
	// Go-runtime string/slice/struct overhead well above the raw text
	// size) would comfortably blow past this.
	const absoluteCapBytes = 20 * 1024 * 1024
	if largeDelta > absoluteCapBytes {
		t.Errorf("large-document peak heap delta = %d bytes, want <= %d bytes (%d MiB)",
			largeDelta, absoluteCapBytes, absoluteCapBytes/(1024*1024))
	}
}
