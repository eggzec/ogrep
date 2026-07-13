package xlsx_test

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildXlsx assembles an in-memory zip from the given part name ->
// content map, standing in for a real .xlsx file. Tests supply exactly
// the parts they need (which need not be a fully spec-compliant
// round-trippable xlsx -- just enough for this package's own reader to
// parse correctly), so no binary fixtures are checked into the repo and
// no external tool (Excel/LibreOffice/openpyxl) is required to produce
// them.
func buildXlsx(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("creating zip part %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("writing zip part %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

// Minimal boilerplate parts shared by most fixtures: the package's
// content-types and root relationship parts. Real Excel writes much
// more here, but this extractor never reads either of these parts, so
// they only need to be present/well-formed enough to not matter -- our
// reader keys off xl/workbook.xml, xl/_rels/workbook.xml.rels, and
// xl/sharedStrings.xml directly, regardless of these two parts'
// contents.
const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
</Types>`

const rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`
