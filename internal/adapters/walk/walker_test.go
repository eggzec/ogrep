package walk

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"officegrep/internal/core/domain"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func collectPaths(t *testing.T, w *Walker, root string, opts domain.SearchOptions) []string {
	t.Helper()
	paths, errc := w.Walk(context.Background(), []string{root}, opts)
	var got []string
	for p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, filepath.ToSlash(rel))
	}
	if err, ok := <-errc; ok && err != nil {
		t.Fatalf("walk error: %v", err)
	}
	sort.Strings(got)
	return got
}

func TestWalkerSkipsGitDirAndLockFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "notes.txt"), "hello")
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main")
	writeFile(t, filepath.Join(root, "~$report.docx"), "lock file placeholder")
	writeFile(t, filepath.Join(root, "report.docx"), "PK\x03\x04 fake docx")

	got := collectPaths(t, New(), root, domain.SearchOptions{})
	want := []string{"notes.txt", "report.docx"}
	assertPathsEqual(t, got, want)
}

func TestWalkerRespectsGitignore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "*.log\nbuild/\n")
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "debug.log"), "ignored")
	writeFile(t, filepath.Join(root, "build", "output.txt"), "ignored dir contents")

	got := collectPaths(t, New(), root, domain.SearchOptions{})
	want := []string{".gitignore", "keep.txt"}
	assertPathsEqual(t, got, want)
}

func TestWalkerNoIgnoreOverride(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "debug.log"), "normally ignored")

	got := collectPaths(t, New(), root, domain.SearchOptions{NoIgnore: true})
	want := []string{".gitignore", "debug.log", "keep.txt"}
	assertPathsEqual(t, got, want)
}

func TestWalkerOfficegrepIgnoreIsAdditional(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(root, ".officegrepignore"), "*.tmp\n")
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "debug.log"), "ignored via gitignore")
	writeFile(t, filepath.Join(root, "scratch.tmp"), "ignored via officegrepignore")

	got := collectPaths(t, New(), root, domain.SearchOptions{})
	want := []string{".gitignore", ".officegrepignore", "keep.txt"}
	assertPathsEqual(t, got, want)
}

func TestWalkerIncludeExcludeGlobs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a")
	writeFile(t, filepath.Join(root, "b.md"), "b")
	writeFile(t, filepath.Join(root, "c.txt"), "c")

	got := collectPaths(t, New(), root, domain.SearchOptions{IncludeGlobs: []string{"*.txt"}})
	assertPathsEqual(t, got, []string{"a.txt", "c.txt"})

	got = collectPaths(t, New(), root, domain.SearchOptions{ExcludeGlobs: []string{"*.md"}})
	assertPathsEqual(t, got, []string{"a.txt", "c.txt"})
}

func TestWalkerExplicitFileArgumentAlwaysSearched(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored.txt\n")
	target := filepath.Join(root, "ignored.txt")
	writeFile(t, target, "content")

	w := New()
	paths, errc := w.Walk(context.Background(), []string{target}, domain.SearchOptions{})
	var got []string
	for p := range paths {
		got = append(got, p)
	}
	if err, ok := <-errc; ok && err != nil {
		t.Fatalf("walk error: %v", err)
	}
	if len(got) != 1 || got[0] != target {
		t.Fatalf("expected explicit file argument to always be searched, got %v", got)
	}
}

func TestWalkerNestedGitignore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "sub", ".gitignore"), "local.tmp\n")
	writeFile(t, filepath.Join(root, "sub", "local.tmp"), "ignored only within sub/")
	writeFile(t, filepath.Join(root, "sub", "keep2.txt"), "keep2")
	// A file with the same name outside sub/ is not affected by sub's
	// nested .gitignore.
	writeFile(t, filepath.Join(root, "local.tmp"), "not ignored at root")

	got := collectPaths(t, New(), root, domain.SearchOptions{})
	want := []string{"keep.txt", "local.tmp", "sub/.gitignore", "sub/keep2.txt"}
	assertPathsEqual(t, got, want)
}

func assertPathsEqual(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
