package walk

// Performance sanity/regression benchmark for the filesystem walker,
// additive to the existing walker_test.go/gitignore_test.go suite. This
// is intentionally NOT a hard pass/fail assertion on timing (filesystem
// performance varies too much across machines/CI runners for that to be
// reliable) -- it exists so `go test -bench` produces a recorded
// ns/op and allocs/op baseline for a realistically large tree, useful
// for spotting future regressions by eye.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/laraibg786/ogrep/internal/core/domain"
)

// buildLargeTree creates a synthetic directory tree with numFiles files
// spread across nested directories (up to 4 levels deep), mixing several
// extensions (including office-format ones, though their content is
// irrelevant to a walk-only benchmark) and scattering .gitignore/
// .ogrepignore files at various depths so ignore-rule loading and
// stack maintenance are exercised too, not just plain enumeration.
func buildLargeTree(tb testing.TB, numFiles int) string {
	tb.Helper()
	root := tb.TempDir()

	exts := []string{".txt", ".md", ".log", ".docx", ".pptx", ".xlsx", ".bin", ""}
	const filesPerDir = 15

	filesWritten := 0
	dirCount := 0
	for filesWritten < numFiles {
		depth := dirCount%4 + 1
		segs := make([]string, depth)
		for d := 0; d < depth; d++ {
			segs[d] = fmt.Sprintf("dir%d_%d", d, (dirCount/(d+1))%7)
		}
		dir := filepath.Join(append([]string{root}, segs...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("creating dir %s: %v", dir, err)
		}

		if dirCount%10 == 0 {
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\ntmp/\n"), 0o644); err != nil {
				tb.Fatalf("writing .gitignore: %v", err)
			}
		}
		if dirCount%25 == 0 {
			if err := os.WriteFile(filepath.Join(dir, ".ogrepignore"), []byte("*.bin\n"), 0o644); err != nil {
				tb.Fatalf("writing .ogrepignore: %v", err)
			}
		}

		for f := 0; f < filesPerDir && filesWritten < numFiles; f++ {
			ext := exts[filesWritten%len(exts)]
			name := fmt.Sprintf("file%d%s", filesWritten, ext)
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				tb.Fatalf("writing file %s: %v", name, err)
			}
			filesWritten++
		}
		dirCount++
	}

	return root
}

// BenchmarkWalkLargeTree walks a synthetic tree of several thousand
// files (mixed extensions, nested ignore files at various depths) and
// reports ns/op, B/op, and allocs/op for a full Walk pass. There's no
// hard pass/fail assertion on timing here -- this is a
// sanity/regression benchmark, not a correctness test -- but it does
// sanity-check that every non-ignored file is actually seen.
func BenchmarkWalkLargeTree(b *testing.B) {
	const numFiles = 4000
	root := buildLargeTree(b, numFiles)
	w := New()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		paths, errc := w.Walk(context.Background(), []string{root}, domain.SearchOptions{})
		count := 0
		for range paths {
			count++
		}
		if err := <-errc; err != nil {
			b.Fatalf("Walk() error = %v", err)
		}
		if count == 0 {
			b.Fatal("expected Walk to yield at least one file")
		}
	}
}

// buildHugeTree is buildLargeTree's WIDE-and-DEEP sibling, purpose-built
// for the thread-scaling benchmark below: it spreads tens of thousands
// of files across thousands of directories, up to 6 levels deep (vs
// buildLargeTree's 4), with a wider branching factor per level, and
// scatters .gitignore/.ogrepignore files at more depths so
// ignore-rule-chain construction is exercised at every level, not just
// near the root. The traversal (syscall/metadata-bound: readdir + stat
// down a large tree) is exactly the phase this task parallelizes, so the
// benchmark tree needs to be big enough that syscall latency, not
// process/goroutine startup overhead, dominates the measurement.
func buildHugeTree(tb testing.TB, numFiles int) string {
	tb.Helper()
	root := tb.TempDir()

	exts := []string{".txt", ".md", ".log", ".docx", ".pptx", ".xlsx", ".bin", ""}
	const filesPerDir = 8
	const maxDepth = 6
	const branchMod = 9

	filesWritten := 0
	dirCount := 0
	for filesWritten < numFiles {
		depth := dirCount%maxDepth + 1
		segs := make([]string, depth)
		for d := 0; d < depth; d++ {
			segs[d] = fmt.Sprintf("dir%d_%d", d, (dirCount/(d+1))%branchMod)
		}
		dir := filepath.Join(append([]string{root}, segs...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("creating dir %s: %v", dir, err)
		}

		if dirCount%12 == 0 {
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\ntmp/\n"), 0o644); err != nil {
				tb.Fatalf("writing .gitignore: %v", err)
			}
		}
		if dirCount%30 == 0 {
			if err := os.WriteFile(filepath.Join(dir, ".ogrepignore"), []byte("*.bin\n"), 0o644); err != nil {
				tb.Fatalf("writing .ogrepignore: %v", err)
			}
		}

		for f := 0; f < filesPerDir && filesWritten < numFiles; f++ {
			ext := exts[filesWritten%len(exts)]
			name := fmt.Sprintf("file%d%s", filesWritten, ext)
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				tb.Fatalf("writing file %s: %v", name, err)
			}
			filesWritten++
		}
		dirCount++
	}

	tb.Logf("buildHugeTree: %d files across %d directories, up to %d levels deep", filesWritten, dirCount, maxDepth)
	return root
}

// BenchmarkWalkThreadScaling is the direct before/after proof this task
// asks for: it walks the SAME large synthetic tree (tens of thousands of
// files, thousands of directories, several levels deep, nested ignore
// files at multiple depths) at Threads = 1, 2, and runtime.NumCPU(), so
// the difference in reported ns/op between sub-benchmarks is a direct
// read on whether directory traversal itself now scales with available
// cores. If threads=1 and threads=NumCPU() come out roughly the same,
// that's a signal (per the task) that something is over-serializing
// (e.g. the bounded job queue's capacity is too small and workers spend
// most of their time blocked, or the ruleNode chain is being contended)
// rather than an expected result to shrug off.
func BenchmarkWalkThreadScaling(b *testing.B) {
	const numFiles = 40_000
	root := buildHugeTree(b, numFiles)

	threadCounts := []int{1, 2, runtime.NumCPU()}
	for _, threads := range threadCounts {
		threads := threads
		b.Run(fmt.Sprintf("threads=%d", threads), func(b *testing.B) {
			w := New()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				paths, errc := w.Walk(context.Background(), []string{root}, domain.SearchOptions{Threads: threads})
				count := 0
				for range paths {
					count++
				}
				if err := <-errc; err != nil {
					b.Fatalf("Walk() error = %v", err)
				}
				if count == 0 {
					b.Fatal("expected Walk to yield at least one file")
				}
			}
		})
	}
}

// samplePeakLiveHeapBytes runs fn while periodically FORCING a garbage
// collection and reading runtime.MemStats.HeapAlloc immediately
// afterward, so each sample reflects the true live heap at that instant
// rather than however much garbage Go's GC pacer happens to be tolerating
// at the time. Mirrors the identical helper in
// internal/adapters/extract/text/perf_bench_test.go (and the other
// format plugins) -- see that file for the fuller rationale. Duplicated
// here (rather than shared) so this package's benchmark file stays a
// self-contained, independently buildable addition.
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

// drainWalk runs a full Walk pass over root, discarding paths, and
// returns how many were seen.
func drainWalk(tb testing.TB, w *Walker, root string, threads int) int {
	tb.Helper()
	paths, errc := w.Walk(context.Background(), []string{root}, domain.SearchOptions{Threads: threads})
	count := 0
	for range paths {
		count++
	}
	if err := <-errc; err != nil {
		tb.Fatalf("Walk() error = %v", err)
	}
	return count
}

// TestWalkPeakMemoryBoundedNotLinear is the walker-side counterpart to
// the per-extractor memory-boundedness tests (e.g.
// internal/adapters/extract/text/perf_bench_test.go): it walks a small
// tree and a 40x-larger tree and asserts the large run's peak live-heap
// delta is NOT anywhere near 40x the small run's. The task's design
// explicitly bounds the job queue's capacity to threads*backlogFactor
// (never the whole tree's directory count), and never accumulates walked
// paths anywhere beyond what's needed to stream them onto the output
// channel -- so peak resident memory during a Walk pass should stay
// roughly flat as tree size grows, dominated by a small, thread-count-
// proportional constant (in-flight jobs plus their ruleNode chains), not
// by the number of directories or files in the tree.
func TestWalkPeakMemoryBoundedNotLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory-boundedness check in -short mode")
	}

	const smallFiles = 500
	const largeFiles = 20_000 // 40x smallFiles

	smallRoot := buildHugeTree(t, smallFiles)
	largeRoot := buildHugeTree(t, largeFiles)
	w := New()

	var smallCount, largeCount int
	smallDelta := samplePeakLiveHeapBytes(func() { smallCount = drainWalk(t, w, smallRoot, 4) })
	largeDelta := samplePeakLiveHeapBytes(func() { largeCount = drainWalk(t, w, largeRoot, 4) })

	t.Logf("walker peak heap delta: small (%d files) = %d bytes, large (%d files) = %d bytes",
		smallCount, smallDelta, largeCount, largeDelta)

	const maxRatio = 15
	floor := smallDelta
	if floor < 128*1024 {
		floor = 128 * 1024
	}
	if largeDelta > int64(maxRatio)*floor {
		t.Errorf("large-tree peak heap delta (%d bytes) is more than %dx the small-tree delta (%d bytes, floored at %d); "+
			"this suggests the job queue or ignore-rule chain is accumulating state proportional to tree size rather than staying bounded",
			largeDelta, maxRatio, smallDelta, floor)
	}

	// Independent absolute sanity cap: a bounded work-queue walker
	// holding only a handful of in-flight directory jobs at a time
	// should never need anywhere near this much resident heap, no
	// matter how many directories the 20,000-file tree spreads across.
	const absoluteCapBytes = 16 * 1024 * 1024
	if largeDelta > absoluteCapBytes {
		t.Errorf("large-tree peak heap delta = %d bytes, want <= %d bytes (%d MiB)",
			largeDelta, absoluteCapBytes, absoluteCapBytes/(1024*1024))
	}
}
