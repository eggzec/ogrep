package xlsx_test

import (
	"bytes"
	"testing"

	"github.com/laraibg786/ogrep/internal/adapters/extract/xlsx"
)

const minimalWorkbookXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Sheet1" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>`

const minimalWorkbookRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`

const minimalSheetXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1"><v>hello</v></c>
    </row>
  </sheetData>
</worksheet>`

func minimalXlsxParts() map[string]string {
	return map[string]string{
		"[Content_Types].xml":        contentTypesXML,
		"_rels/.rels":                rootRelsXML,
		"xl/workbook.xml":            minimalWorkbookXML,
		"xl/_rels/workbook.xml.rels": minimalWorkbookRelsXML,
		"xl/worksheets/sheet1.xml":   minimalSheetXML,
	}
}

func TestNameAndExtensions(t *testing.T) {
	e := xlsx.Extractor{}
	if e.Name() != "xlsx" {
		t.Errorf("Name() = %q, want %q", e.Name(), "xlsx")
	}
	exts := e.Extensions()
	if len(exts) != 1 || exts[0] != ".xlsx" {
		t.Errorf("Extensions() = %v, want [.xlsx]", exts)
	}
}

func TestSniffValidXlsx(t *testing.T) {
	data := buildXlsx(t, minimalXlsxParts())
	ra := bytes.NewReader(data)
	e := xlsx.Extractor{}
	if !e.Sniff("workbook.xlsx", ra, int64(len(data))) {
		t.Error("Sniff() = false for a valid xlsx fixture, want true")
	}
}

func TestSniffRenamedExtension(t *testing.T) {
	// A real xlsx file renamed to an unrelated extension should still be
	// recognized by content, not by its file name.
	data := buildXlsx(t, minimalXlsxParts())
	ra := bytes.NewReader(data)
	e := xlsx.Extractor{}
	if !e.Sniff("mystery-file.bin", ra, int64(len(data))) {
		t.Error("Sniff() = false for a renamed xlsx fixture, want true (content-based detection)")
	}
}

func TestSniffMissingWorkbookPart(t *testing.T) {
	// A zip that doesn't contain xl/workbook.xml at all (e.g. some other
	// kind of zip, or a different OOXML format) must not be claimed.
	data := buildXlsx(t, map[string]string{
		"[Content_Types].xml": contentTypesXML,
		"some/other/part.xml": "<root/>",
	})
	ra := bytes.NewReader(data)
	e := xlsx.Extractor{}
	if e.Sniff("notreally.xlsx", ra, int64(len(data))) {
		t.Error("Sniff() = true for a zip without xl/workbook.xml, want false")
	}
}

func TestSniffCorruptNonZip(t *testing.T) {
	// Encrypted OOXML (an OLE/CFB container) or plain garbage is not a
	// zip at all; Sniff must return false, not panic or error.
	garbage := []byte("this is definitely not a zip file, just plain garbage bytes\x00\x01\x02")
	ra := bytes.NewReader(garbage)
	e := xlsx.Extractor{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Sniff() panicked on corrupt input: %v", r)
		}
	}()

	if e.Sniff("corrupt.xlsx", ra, int64(len(garbage))) {
		t.Error("Sniff() = true for corrupt non-zip input, want false")
	}
}

func TestSniffEmptyFile(t *testing.T) {
	ra := bytes.NewReader(nil)
	e := xlsx.Extractor{}
	if e.Sniff("empty.xlsx", ra, 0) {
		t.Error("Sniff() = true for an empty file, want false")
	}
}
