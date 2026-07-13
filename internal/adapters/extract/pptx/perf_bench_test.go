package pptx

// Performance/memory verification for the pptx extractor, additive to
// the existing pptx_test.go/fixture_test.go/integration_test.go suite.
// See internal/adapters/extract/docx/perf_bench_test.go for the fuller
// rationale behind the two pieces added here: a testing.B benchmark over
// a realistically large deck, and a regular Test that quantitatively
// checks peak resident memory during Extract stays roughly flat as the
// deck grows, rather than scaling with it (proving the streamParagraphs
// token-by-token design in content.go actually delivers the bounded
// memory it's documented to).

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
)

// buildPptxTB is a copy of fixture_test.go's buildPptx, retyped to
// accept testing.TB instead of *testing.T so it can be called from both
// benchmarks (*testing.B) and the memory-boundedness test (*testing.T).
func buildPptxTB(tb testing.TB, parts map[string]string) []byte {
	tb.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			tb.Fatalf("creating zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			tb.Fatalf("writing zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("closing zip writer: %v", err)
	}
	return buf.Bytes()
}

// buildPptxFixture builds a full pptx package with nSlides slides, each
// containing shapesPerSlide shapes with parasPerShape paragraphs apiece,
// reusing fixture_test.go's baseParts/presentationXML/
// presentationRelsXML/slideXML/shapeFixture helpers (same package, so
// directly callable) for structural correctness.
func buildPptxFixture(tb testing.TB, nSlides, shapesPerSlide, parasPerShape int) []byte {
	tb.Helper()

	parts := baseParts()
	rids := make([]string, nSlides)
	relMap := make(map[string]string, nSlides)

	for i := 0; i < nSlides; i++ {
		rid := fmt.Sprintf("rId%d", i+1)
		rids[i] = rid
		target := fmt.Sprintf("slides/slide%d.xml", i+1)
		relMap[rid] = target

		shapes := make([]shapeFixture, 0, shapesPerSlide)
		for s := 0; s < shapesPerSlide; s++ {
			paras := make([]string, parasPerShape)
			for p := range paras {
				paras[p] = fmt.Sprintf("slide %d shape %d paragraph %d filler words, mentioning apple and orange but rarely the needle", i, s, p)
			}
			shapes = append(shapes, shapeFixture{name: fmt.Sprintf("Shape %d", s), paragraphs: paras})
		}
		parts["ppt/"+target] = slideXML(shapes)
	}

	parts["ppt/presentation.xml"] = presentationXML(rids)
	parts["ppt/_rels/presentation.xml.rels"] = presentationRelsXML(relMap)

	return buildPptxTB(tb, parts)
}

// buildSingleSlideFixture builds a pptx package with exactly ONE slide
// containing one shape with nParas paragraphs. Unlike buildPptxFixture
// (which scales the number of SLIDES, i.e. the number of separate zip
// parts/archive entries -- an axis that inherently carries some
// archive/zip central-directory overhead proportional to part count,
// regardless of how the content of any one part is parsed), this holds
// the part count fixed at one and scales the paragraph count WITHIN
// that single part's streamParagraphs token loop. That isolates exactly
// the claim under test in
// TestExtractPeakMemoryBoundedNotLinear/single-slide below: whether
// streamParagraphs (content.go) buffers a whole slide's paragraphs, or
// truly processes them one token/paragraph at a time.
func buildSingleSlideFixture(tb testing.TB, nParas int) []byte {
	tb.Helper()

	paras := make([]string, nParas)
	for i := range paras {
		paras[i] = fmt.Sprintf("paragraph %d filler words, mentioning apple and orange but rarely the needle", i)
	}

	parts := baseParts()
	parts["ppt/presentation.xml"] = presentationXML([]string{"rId1"})
	parts["ppt/_rels/presentation.xml.rels"] = presentationRelsXML(map[string]string{"rId1": "slides/slide1.xml"})
	parts["ppt/slides/slide1.xml"] = slideXML([]shapeFixture{{name: "Body", paragraphs: paras}})

	return buildPptxTB(tb, parts)
}

// drainExtract runs Extract to completion, discarding unit text, and
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

// BenchmarkExtractLargeDeck measures a full Extract pass over a deck
// with hundreds of slides, each with a few shapes -- the realistic-scale
// workload described in the architecture doc.
func BenchmarkExtractLargeDeck(b *testing.B) {
	const nSlides, shapesPerSlide, parasPerShape = 300, 4, 3 // ~3600 paragraphs total
	data := buildPptxFixture(b, nSlides, shapesPerSlide, parasPerShape)

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

// TestExtractPeakMemoryBoundedNotLinear has two subtests, deliberately
// isolating two different scaling axes a pptx file can grow along:
//
//   - "single-slide" scales paragraph count WITHIN one slide's one shape
//     (1000x: 100 -> 100,000 paragraphs), holding the zip part count
//     fixed at one slide. This is the direct analog of the docx/xlsx
//     packages' memory tests and isolates exactly the claim made in
//     content.go's doc comment: streamParagraphs never buffers more than
//     one in-progress paragraph's text. This subtest is a hard
//     pass/fail assertion.
//
//   - "many-slides" scales the number of SLIDES (1000x: 5 -> 5,000),
//     i.e. the number of separate zip archive parts. This is reported
//     for the record but NOT asserted on with the same tight ratio:
//     archive/zip's Reader decodes every part's central-directory record
//     up front in NewReader (before any of our streaming code runs), so
//     memory proportional to PART COUNT is an inherent, expected cost of
//     using the zip format with many small parts -- a different concern
//     from "does this plugin buffer a full in-memory DOM of a document's
//     content", which is what the architecture doc's streaming claim is
//     actually about. It's still logged so a regression here (e.g. an
//     unexpectedly large multiplier) would be visible in the test
//     output for future investigation.
func TestExtractPeakMemoryBoundedNotLinear(t *testing.T) {
	t.Run("single-slide", func(t *testing.T) {
		const smallParas = 100
		const largeParas = 100_000 // 1000x smallParas

		smallData := buildSingleSlideFixture(t, smallParas)
		largeData := buildSingleSlideFixture(t, largeParas)

		var smallUnits, largeUnits int
		smallDelta := samplePeakLiveHeapBytes(func() { smallUnits = drainExtract(t, smallData) })
		largeDelta := samplePeakLiveHeapBytes(func() { largeUnits = drainExtract(t, largeData) })

		t.Logf("pptx single-slide peak heap-objects delta: small (%d units) = %d bytes, large (%d units) = %d bytes",
			smallUnits, smallDelta, largeUnits, largeDelta)

		// Threshold reasoning: mirrors the docx package's identical
		// test. A streaming implementation's peak resident memory here
		// is dominated by roughly constant-size state (one open zip
		// reader, the in-progress paragraph builder, a tiny shape-name
		// stack) that doesn't grow with paragraph count, so we expect
		// the ratio to be close to 1x. We use 50x as the pass/fail line:
		// generous enough to absorb GC-scheduling and sampler-polling
		// noise (especially given a small-run baseline that can be just
		// tens of KB), while still cleanly rejecting anything close to
		// the ~1000x a linear-scaling (e.g. xml.Unmarshal-based)
		// implementation would produce. The small-run baseline is
		// floored before computing the ratio so a near-zero measurement
		// can't make the ratio blow up spuriously, and an absolute cap
		// is asserted too, independent of the (noisier) small-run
		// baseline.
		const maxRatio = 50
		floor := smallDelta
		if floor < 64*1024 {
			floor = 64 * 1024
		}
		if largeDelta > int64(maxRatio)*floor {
			t.Errorf("large-paragraph-count peak heap delta (%d bytes) is more than %dx the small one's (%d bytes, floored at %d); "+
				"this suggests memory use scales with paragraph count rather than staying bounded",
				largeDelta, maxRatio, smallDelta, floor)
		}

		const absoluteCapBytes = 20 * 1024 * 1024
		if largeDelta > absoluteCapBytes {
			t.Errorf("large-paragraph-count peak heap delta = %d bytes, want <= %d bytes (%d MiB)",
				largeDelta, absoluteCapBytes, absoluteCapBytes/(1024*1024))
		}
	})

	t.Run("many-slides", func(t *testing.T) {
		const shapesPerSlide, parasPerShape = 2, 2
		const smallSlides = 5
		const largeSlides = 5000 // 1000x smallSlides

		smallData := buildPptxFixture(t, smallSlides, shapesPerSlide, parasPerShape)
		largeData := buildPptxFixture(t, largeSlides, shapesPerSlide, parasPerShape)

		var smallUnits, largeUnits int
		smallDelta := samplePeakLiveHeapBytes(func() { smallUnits = drainExtract(t, smallData) })
		largeDelta := samplePeakLiveHeapBytes(func() { largeUnits = drainExtract(t, largeData) })

		t.Logf("pptx many-slides peak heap-objects delta: small (%d units) = %d bytes, large (%d units) = %d bytes "+
			"(not asserted on with the tight streaming-ratio threshold -- see the doc comment on this test for why part-count growth is a different, expected axis)",
			smallUnits, smallDelta, largeUnits, largeDelta)

		// Even so, a full deck of 5000 slides should not need an
		// unreasonable amount of resident memory (this is a much looser
		// cap than the single-slide subtest's, specifically to
		// accommodate the expected archive/zip central-directory
		// overhead proportional to part count).
		const absoluteCapBytes = 64 * 1024 * 1024
		if largeDelta > absoluteCapBytes {
			t.Errorf("many-slides peak heap delta = %d bytes, want <= %d bytes (%d MiB)",
				largeDelta, absoluteCapBytes, absoluteCapBytes/(1024*1024))
		}
	})
}
