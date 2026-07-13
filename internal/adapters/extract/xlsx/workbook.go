package xlsx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// sheetRef is a fully resolved sheet: its human-readable name (as
// declared in workbook.xml) paired with the zip part path that actually
// holds its rows (resolved through workbook.xml.rels).
type sheetRef struct {
	Name   string
	Target string // zip entry path, e.g. "xl/worksheets/sheet3.xml"
}

// findZipFile returns the *zip.File with the given exact name, or nil.
func findZipFile(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// readWorkbookSheets parses xl/workbook.xml for the declared sheets (in
// tab order) and resolves each one's r:id to a concrete worksheet part
// path via xl/_rels/workbook.xml.rels. The sheet name -> part path
// mapping is deliberately indirect per the OOXML spec: a sheet's
// underlying file name (e.g. "sheet3.xml") need not match its
// declaration order or its display name, so we must go through the
// relationship id rather than guessing.
//
// A sheet whose r:id has no matching relationship (a malformed/dangling
// reference) is skipped rather than failing the whole file, matching
// this extractor's general policy of degrading gracefully on
// unexpected-but-survivable structure.
func readWorkbookSheets(zr *zip.Reader) ([]sheetRef, error) {
	wbFile := findZipFile(zr, "xl/workbook.xml")
	if wbFile == nil {
		return nil, fmt.Errorf("missing xl/workbook.xml")
	}

	type sheetDecl struct{ Name, RID string }
	var decls []sheetDecl

	rc, err := wbFile.Open()
	if err != nil {
		return nil, fmt.Errorf("opening xl/workbook.xml: %w", err)
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing xl/workbook.xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "sheet" {
			continue
		}
		var d sheetDecl
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "name":
				d.Name = a.Value
			case "id":
				// The r:id attribute; its namespace prefix resolves to
				// the officeDocument relationships namespace, but we
				// match on the local name alone since no other "id"
				// attribute is expected on a <sheet> element.
				d.RID = a.Value
			}
		}
		decls = append(decls, d)
	}

	rels, err := readRels(zr, "xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, err
	}

	out := make([]sheetRef, 0, len(decls))
	for _, d := range decls {
		target, ok := rels[d.RID]
		if !ok {
			continue
		}
		out = append(out, sheetRef{Name: d.Name, Target: resolvePartPath(target)})
	}
	return out, nil
}

// resolvePartPath resolves a relationship Target (as found in
// xl/_rels/workbook.xml.rels) into a full zip entry path. Per the OPC
// spec, a relative target in a part's rels file is relative to the
// directory containing that part itself (xl/workbook.xml lives in
// xl/), not to the _rels directory, so "worksheets/sheet3.xml" becomes
// "xl/worksheets/sheet3.xml". A target that's already package-absolute
// (leading "/") is used as-is (minus the leading slash).
func resolvePartPath(target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	return "xl/" + target
}

// readRels parses a .rels part (Relationships > Relationship elements)
// into a map of relationship Id -> Target. A missing rels part (no
// sheets could then be resolved) is not itself an error here; the
// caller ends up with an empty sheet list, which is a legitimate if
// unusual document.
func readRels(zr *zip.Reader, relsPath string) (map[string]string, error) {
	f := findZipFile(zr, relsPath)
	if f == nil {
		return map[string]string{}, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", relsPath, err)
	}
	defer rc.Close()

	rels := make(map[string]string)
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", relsPath, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			continue
		}
		var id, target string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "Id":
				id = a.Value
			case "Target":
				target = a.Value
			}
		}
		if id != "" {
			rels[id] = target
		}
	}
	return rels, nil
}

// readSharedStrings loads xl/sharedStrings.xml (if present) into an
// in-memory slice indexed exactly by shared-string index (0-based, per
// <si> declaration order). This part is small relative to the workbook
// as a whole (proportional to the shared-string table size, not to
// sheet row counts), so eager loading is acceptable even though sheet
// data itself must be streamed. A missing sharedStrings.xml (a workbook
// with no shared strings, e.g. all-numeric or all-inline-string) is not
// an error: it simply yields a nil/empty slice.
func readSharedStrings(zr *zip.Reader) ([]string, error) {
	f := findZipFile(zr, "xl/sharedStrings.xml")
	if f == nil {
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("opening xl/sharedStrings.xml: %w", err)
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	var strs []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing xl/sharedStrings.xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "si" {
			continue
		}
		text, err := captureRichText(dec)
		if err != nil {
			return nil, fmt.Errorf("parsing xl/sharedStrings.xml: %w", err)
		}
		strs = append(strs, text)
	}
	return strs, nil
}
