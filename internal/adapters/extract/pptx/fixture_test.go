package pptx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
)

// buildPptx writes parts (zip entry name -> raw content) into an
// in-memory zip archive and returns its bytes. Tests use this instead
// of checking in real binary .pptx files or shelling out to any
// external tool.
func buildPptx(t *testing.T, parts map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("creating zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("writing zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip writer: %v", err)
	}
	return buf.Bytes()
}

// baseParts returns the minimal-but-valid boilerplate parts every
// pptx package needs ([Content_Types].xml and the root
// _rels/.rels) that our own extractor never actually reads, but which
// keep the fixture package structurally plausible.
func baseParts() map[string]string {
	return map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>
</Relationships>`,
	}
}

// presentationXML renders ppt/presentation.xml with a <p:sldIdLst>
// listing the given r:id values in the given (document) order.
func presentationXML(rIDsInOrder []string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:sldIdLst>
`)
	id := 256
	for _, rid := range rIDsInOrder {
		fmt.Fprintf(&sb, "    <p:sldId id=\"%d\" r:id=\"%s\"/>\n", id, rid)
		id++
	}
	sb.WriteString(`  </p:sldIdLst>
</p:presentation>`)
	return sb.String()
}

// presentationRelsXML renders ppt/_rels/presentation.xml.rels mapping
// each rID to a slide Target path (relative to "ppt/").
func presentationRelsXML(rIDToTarget map[string]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
`)
	for rid, target := range rIDToTarget {
		fmt.Fprintf(&sb, "  <Relationship Id=%q Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide\" Target=%q/>\n", rid, target)
	}
	sb.WriteString(`</Relationships>`)
	return sb.String()
}

// shapeFixture describes one <p:sp> to render into a slide/notes part.
type shapeFixture struct {
	name       string // cNvPr name attribute; empty means omit cNvPr entirely
	paragraphs []string
}

// sldOrNotesXML renders either a slide part (root <p:sld>) or a notes
// part (root <p:notes>) containing the given shapes, mirroring the
// shared p:cSld > p:spTree > p:sp > p:txBody > a:p > a:r > a:t
// structure both part types use.
func sldOrNotesXML(root string, shapes []shapeFixture) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:%s xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:cSld>
    <p:spTree>
`, root)
	for i, sh := range shapes {
		sb.WriteString("      <p:sp>\n        <p:nvSpPr>\n")
		if sh.name != "" {
			fmt.Fprintf(&sb, "          <p:cNvPr id=\"%d\" name=%q/>\n", i+1, sh.name)
		}
		sb.WriteString("        </p:nvSpPr>\n        <p:txBody>\n")
		for _, ptext := range sh.paragraphs {
			var esc bytes.Buffer
			xml.EscapeText(&esc, []byte(ptext))
			fmt.Fprintf(&sb, "          <a:p><a:r><a:t>%s</a:t></a:r></a:p>\n", esc.String())
		}
		sb.WriteString("        </p:txBody>\n      </p:sp>\n")
	}
	fmt.Fprintf(&sb, "    </p:spTree>\n  </p:cSld>\n</p:%s>", root)
	return sb.String()
}

func slideXML(shapes []shapeFixture) string { return sldOrNotesXML("sld", shapes) }
func notesXML(shapes []shapeFixture) string { return sldOrNotesXML("notes", shapes) }

// slideRelsXML renders a slide's own .rels part with a single
// notesSlide relationship pointing at target (relative to the slide's
// own directory, e.g. "../notesSlides/notesSlideX.xml").
func slideRelsXML(target string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target=%q/>
</Relationships>`, target)
}
