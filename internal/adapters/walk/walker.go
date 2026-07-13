package walk

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"officegrep/internal/core/domain"
)

// vcsDirs are directories skipped by default, regardless of ignore
// files.
var vcsDirs = map[string]bool{
	".git": true,
}

// lockFilePattern matches MS Office's transient lock files, e.g.
// "~$report.docx", which are never real documents.
var lockFilePattern = regexp.MustCompile(`^~\$.*\.(docx|pptx|xlsx)$`)

// ignoreFileNames are the ignore files consulted in each directory,
// in load order. .officegrepignore is project-local and additional to
// .gitignore, not a replacement for it.
var ignoreFileNames = []string{".gitignore", ".officegrepignore"}

// Walker implements ports.FileWalker over the local filesystem.
type Walker struct{}

// New returns a ready-to-use Walker.
func New() *Walker { return &Walker{} }

// dirRules pairs a directory with the combined ignore rules that apply
// to entries directly inside it (its own ignore files plus everything
// inherited from ancestor directories).
type dirRules struct {
	dir  string
	sets []PatternSet
}

// Walk implements ports.FileWalker.
func (w *Walker) Walk(ctx context.Context, roots []string, opts domain.SearchOptions) (<-chan string, <-chan error) {
	paths := make(chan string)
	errc := make(chan error, 1)

	go func() {
		defer close(paths)
		defer close(errc)

		for _, root := range roots {
			if err := w.walkRoot(ctx, root, opts, paths); err != nil {
				select {
				case errc <- err:
				default:
				}
				return
			}
		}
	}()

	return paths, errc
}

func (w *Walker) walkRoot(ctx context.Context, root string, opts domain.SearchOptions, paths chan<- string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		// An explicitly named file is always searched, regardless of
		// ignore rules (matching rg's behavior for explicit
		// arguments).
		select {
		case paths <- root:
		case <-ctx.Done():
		}
		return nil
	}

	// Stack of ignore-rule scopes from the root down to the directory
	// currently being visited. filepath.WalkDir visits a directory
	// immediately before its children in lexical order, so we can
	// maintain this as a simple stack: pop entries that are no longer
	// an ancestor of the directory we just entered, then push the new
	// directory's own rules.
	var stack []dirRules

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// Unreadable entry (permissions, race with deletion,
			// etc): skip it rather than aborting the whole walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			base := d.Name()
			if path != root && vcsDirs[base] {
				return fs.SkipDir
			}

			for len(stack) > 0 && !isAncestorDir(stack[len(stack)-1].dir, path) {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, dirRules{dir: path, sets: loadDirIgnoreSets(path)})

			if path == root {
				return nil
			}

			if !opts.NoIgnore && isIgnored(stack, path, true) {
				return fs.SkipDir
			}
			if !matchesGlobFilters(opts, path, base, true) {
				return nil
			}
			return nil
		}

		// Regular file (or symlink etc.) candidate.
		base := d.Name()
		if lockFilePattern.MatchString(base) {
			return nil
		}
		if !opts.NoIgnore && isIgnored(stack, path, false) {
			return nil
		}
		if !matchesGlobFilters(opts, path, base, false) {
			return nil
		}

		select {
		case paths <- path:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
}

// isAncestorDir reports whether child is dir itself or nested under it.
func isAncestorDir(dir, child string) bool {
	if dir == child {
		return true
	}
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// loadDirIgnoreSets reads any ignore files present directly inside dir.
func loadDirIgnoreSets(dir string) []PatternSet {
	var sets []PatternSet
	for _, name := range ignoreFileNames {
		lines, err := readLines(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		ps := ParsePatternLines(lines)
		if !ps.Empty() {
			sets = append(sets, ps)
		}
	}
	return sets
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

// isIgnored evaluates path against every ignore-rule scope on the
// stack, from the root down to path's immediate parent, in order, so
// that a deeper (more specific) ignore file's rules are applied after
// (and can override, via negation) a shallower one's — matching how
// git itself layers nested .gitignore files.
func isIgnored(stack []dirRules, path string, isDir bool) bool {
	ignored := false
	for _, dr := range stack {
		rel, err := filepath.Rel(dr.dir, path)
		if err != nil || rel == "." {
			continue
		}
		rel = filepath.ToSlash(rel)
		for _, ps := range dr.sets {
			for _, p := range ps.patterns {
				if p.match(rel, isDir) {
					ignored = !p.negate
				}
			}
		}
	}
	return ignored
}

func matchesGlobFilters(opts domain.SearchOptions, path, base string, isDir bool) bool {
	if isDir {
		return true // never filter directories out of descent based on include/exclude
	}
	if len(opts.ExcludeGlobs) > 0 && matchAnyGlob(opts.ExcludeGlobs, path, base) {
		return false
	}
	if len(opts.IncludeGlobs) > 0 && !matchAnyGlob(opts.IncludeGlobs, path, base) {
		return false
	}
	return true
}

func matchAnyGlob(globs []string, path, base string) bool {
	for _, g := range globs {
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
	}
	return false
}
