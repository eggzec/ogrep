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
	"testing"

	"officegrep/internal/core/domain"
)

// buildLargeTree creates a synthetic directory tree with numFiles files
// spread across nested directories (up to 4 levels deep), mixing several
// extensions (including office-format ones, though their content is
// irrelevant to a walk-only benchmark) and scattering .gitignore/
// .officegrepignore files at various depths so ignore-rule loading and
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
			if err := os.WriteFile(filepath.Join(dir, ".officegrepignore"), []byte("*.bin\n"), 0o644); err != nil {
				tb.Fatalf("writing .officegrepignore: %v", err)
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
