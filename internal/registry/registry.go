// Package registry implements content-sniffing dispatch from a file to
// the DocumentExtractor that can handle it.
//
// OOXML formats (docx/pptx/xlsx) are all zip packages, so dispatching on
// file extension alone is unreliable (a renamed file, or a mislabeled
// one, would be missed or mis-handled). The registry instead: (1)
// fast-path filters candidate extractors by file extension as a cheap
// pre-filter, then (2) calls each candidate's Sniff, which is expected
// to peek at real file contents (e.g. zip central directory entries
// or [Content_Types].xml) to confirm the format. If no extension-based
// candidate matches, every registered extractor is tried as a fallback,
// so a renamed file can still be recognized.
package registry

import (
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/laraibg786/ogrep/internal/core/ports"
)

// extHint lets an extractor advertise which file extensions it is
// typically associated with, purely as a fast-path filter. It is
// optional: extractors that don't implement it are always tried as a
// fallback candidate.
type extHint interface {
	Extensions() []string
}

// Registry holds the set of known DocumentExtractors and dispatches a
// given file to the right one via extension pre-filtering plus
// content sniffing.
type Registry struct {
	mu         sync.RWMutex
	extractors []ports.DocumentExtractor
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{}
}

// Register adds an extractor to the registry. It is safe to call from
// multiple goroutines (typically from package init() functions), though
// in practice registration happens sequentially during program startup.
func (r *Registry) Register(e ports.DocumentExtractor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extractors = append(r.extractors, e)
}

// All returns a snapshot of the currently registered extractors.
func (r *Registry) All() []ports.DocumentExtractor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ports.DocumentExtractor, len(r.extractors))
	copy(out, r.extractors)
	return out
}

// For returns the extractor that recognizes the file at path (whose
// contents are readable via ra, with total size size), or false if none
// do.
//
// Dispatch order: extractors whose advertised extensions match the
// file's extension are tried first (cheap common case), followed by any
// remaining extractors as a fallback (handles renamed/mislabeled
// files). Within each group, extractors are tried in registration
// order, and the first whose Sniff returns true wins.
func (r *Registry) For(path string, ra io.ReaderAt, size int64) (ports.DocumentExtractor, bool) {
	all := r.All()
	ext := strings.ToLower(filepath.Ext(path))

	var primary, rest []ports.DocumentExtractor
	for _, e := range all {
		if hinter, ok := e.(extHint); ok && hasExt(hinter.Extensions(), ext) {
			primary = append(primary, e)
		} else {
			rest = append(rest, e)
		}
	}

	for _, e := range primary {
		if e.Sniff(path, ra, size) {
			return e, true
		}
	}
	for _, e := range rest {
		if e.Sniff(path, ra, size) {
			return e, true
		}
	}
	return nil, false
}

func hasExt(exts []string, ext string) bool {
	for _, e := range exts {
		if strings.EqualFold(e, ext) {
			return true
		}
	}
	return false
}

// Default is the process-wide registry that self-registering extractor
// plugins add themselves to via init(). cmd/ogrep and the
// orchestrator use this instance unless a test constructs its own via
// New().
var Default = New()
