package registry

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/laraibg786/ogrep/internal/core/domain"
)

// fakeExtractor is a minimal ports.DocumentExtractor for testing
// registry dispatch without depending on real document formats.
type fakeExtractor struct {
	name  string
	exts  []string
	magic []byte // Sniff returns true iff the file starts with this
}

func (f fakeExtractor) Name() string         { return f.name }
func (f fakeExtractor) Extensions() []string { return f.exts }

func (f fakeExtractor) Sniff(path string, ra io.ReaderAt, size int64) bool {
	if len(f.magic) == 0 {
		return false
	}
	buf := make([]byte, len(f.magic))
	n, err := ra.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return false
	}
	return n >= len(f.magic) && bytes.Equal(buf, f.magic)
}

func (f fakeExtractor) Extract(ctx context.Context, ra io.ReaderAt, size int64) (<-chan domain.TextUnit, <-chan error) {
	units := make(chan domain.TextUnit)
	errc := make(chan error, 1)
	close(units)
	close(errc)
	return units, errc
}

func readerAt(data []byte) (io.ReaderAt, int64) {
	return bytes.NewReader(data), int64(len(data))
}

func TestRegistryDispatchByExtensionAndContent(t *testing.T) {
	r := New()
	docx := fakeExtractor{name: "docx", exts: []string{".docx"}, magic: []byte("PK\x03\x04DOCX")}
	xlsx := fakeExtractor{name: "xlsx", exts: []string{".xlsx"}, magic: []byte("PK\x03\x04XLSX")}
	r.Register(docx)
	r.Register(xlsx)

	ra, size := readerAt([]byte("PK\x03\x04DOCX rest of file..."))
	got, ok := r.For("report.docx", ra, size)
	if !ok || got.Name() != "docx" {
		t.Fatalf("expected docx extractor, got %v ok=%v", got, ok)
	}
}

func TestRegistryDispatchRenamedExtension(t *testing.T) {
	// A docx file renamed to .txt (or some unrelated extension) must
	// still be recognized via content sniffing, since extension-based
	// pre-filtering is only a fast-path optimization, not the source of
	// truth.
	r := New()
	docx := fakeExtractor{name: "docx", exts: []string{".docx"}, magic: []byte("PK\x03\x04DOCX")}
	r.Register(docx)

	ra, size := readerAt([]byte("PK\x03\x04DOCX rest of file..."))
	got, ok := r.For("report.renamed.txt", ra, size)
	if !ok || got.Name() != "docx" {
		t.Fatalf("expected docx extractor for renamed file, got %v ok=%v", got, ok)
	}
}

func TestRegistryDispatchWrongExtensionRightContentBeatsWrongContentRightExtension(t *testing.T) {
	// A file named "fake.xlsx" but actually containing docx magic bytes
	// should be recognized as docx, not xlsx, because Sniff (not the
	// extension) is authoritative.
	r := New()
	docx := fakeExtractor{name: "docx", exts: []string{".docx"}, magic: []byte("DOCXMAGIC")}
	xlsx := fakeExtractor{name: "xlsx", exts: []string{".xlsx"}, magic: []byte("XLSXMAGIC")}
	r.Register(docx)
	r.Register(xlsx)

	ra, size := readerAt([]byte("DOCXMAGIC..."))
	got, ok := r.For("fake.xlsx", ra, size)
	if !ok || got.Name() != "docx" {
		t.Fatalf("expected docx extractor despite .xlsx extension, got %v ok=%v", got, ok)
	}
}

func TestRegistryNoMatch(t *testing.T) {
	r := New()
	r.Register(fakeExtractor{name: "docx", exts: []string{".docx"}, magic: []byte("DOCXMAGIC")})

	ra, size := readerAt([]byte("plain text content"))
	_, ok := r.For("notes.txt", ra, size)
	if ok {
		t.Fatalf("expected no extractor to match plain content")
	}
}

func TestRegistryAll(t *testing.T) {
	r := New()
	r.Register(fakeExtractor{name: "a"})
	r.Register(fakeExtractor{name: "b"})
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 extractors, got %d", len(all))
	}
}
