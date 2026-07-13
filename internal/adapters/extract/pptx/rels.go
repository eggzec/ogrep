package pptx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"
)

// OOXML namespace URIs used to recognize elements/attributes
// regardless of the prefix a document happens to bind them to. Go's
// encoding/xml resolves a prefixed name's Space to the declared
// namespace URI when an xmlns:<prefix> declaration is in scope; if a
// document is missing that declaration (technically invalid, but seen
// in the wild), Space instead falls back to the raw prefix string
// (verified against the standard library's actual behavior). nameIs
// checks both so we recognize well-formed documents authoritatively
// and still cope with sloppy ones.
const (
	nsPresentationMain = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsDrawingMain      = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsRelationships    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
)

// nameIs reports whether an xml.Name resolves to the given namespace
// and local name, tolerating the undeclared-prefix fallback described
// above.
func nameIs(n xml.Name, ns, prefixFallback, local string) bool {
	if n.Local != local {
		return false
	}
	return n.Space == ns || n.Space == prefixFallback
}

// relationship is one <Relationship> entry from a .rels part.
type relationship struct {
	ID, Type, Target string
}

// resolveSlideOrder returns the ordered (1-based by position), fully
// resolved zip part paths of every slide in the presentation, in the
// authoritative editor-visible order given by
// ppt/presentation.xml's <p:sldIdLst>/<p:sldId> sequence — NOT by
// sorting ppt/slides/slideN.xml filenames, which can disagree with
// the real order once slides have been reordered in the editor.
func resolveSlideOrder(files map[string]*zip.File) ([]string, error) {
	pf, ok := files[presentationPath]
	if !ok {
		return nil, fmt.Errorf("pptx: missing %s", presentationPath)
	}
	rc, err := pf.Open()
	if err != nil {
		return nil, fmt.Errorf("pptx: opening %s: %w", presentationPath, err)
	}
	ids, err := parseSldIDOrder(rc)
	rc.Close()
	if err != nil {
		return nil, fmt.Errorf("pptx: parsing %s: %w", presentationPath, err)
	}

	rels := map[string]relationship{}
	if rf, ok := files[presentationRelsPath]; ok {
		rrc, err := rf.Open()
		if err != nil {
			return nil, fmt.Errorf("pptx: opening %s: %w", presentationRelsPath, err)
		}
		rels, err = parseRelationships(rrc)
		rrc.Close()
		if err != nil {
			return nil, fmt.Errorf("pptx: parsing %s: %w", presentationRelsPath, err)
		}
	}

	order := make([]string, 0, len(ids))
	for _, id := range ids {
		rel, ok := rels[id]
		if !ok {
			// Dangling r:id with no matching relationship; skip
			// defensively rather than failing the whole extraction.
			continue
		}
		order = append(order, resolveRelTarget("ppt", rel.Target))
	}
	return order, nil
}

// parseSldIDOrder streams <p:sldId> elements in document order under
// ppt/presentation.xml's <p:sldIdLst>, returning each one's r:id
// attribute (the relationship id that resolves to the actual slide
// part — distinct from the plain numeric "id" attribute sldId also
// carries).
func parseSldIDOrder(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(r)
	var ids []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || !nameIs(se.Name, nsPresentationMain, "p", "sldId") {
			continue
		}
		for _, a := range se.Attr {
			if nameIs(a.Name, nsRelationships, "r", "id") {
				ids = append(ids, a.Value)
				break
			}
		}
	}
	return ids, nil
}

// parseRelationships fully parses a small .rels part (these are tiny
// lookup tables, not per-slide content, so unlike slide/notes XML
// there's no streaming constraint here) into a map keyed by
// relationship Id.
func parseRelationships(r io.Reader) (map[string]relationship, error) {
	dec := xml.NewDecoder(r)
	out := map[string]relationship{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			continue
		}
		var rel relationship
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "Id":
				rel.ID = a.Value
			case "Type":
				rel.Type = a.Value
			case "Target":
				rel.Target = a.Value
			}
		}
		if rel.ID != "" {
			out[rel.ID] = rel
		}
	}
	return out, nil
}

// resolveRelTarget resolves a relationship Target (which is relative
// to baseDir, the directory containing the part the .rels file
// belongs to — e.g. "ppt" for presentation.xml.rels, "ppt/slides" for
// a slide's own .rels — unless it starts with "/", in which case it's
// already package-root-relative) into a zip part path.
func resolveRelTarget(baseDir, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	return path.Clean(path.Join(baseDir, target))
}

// resolveNotesPath finds the speaker-notes part associated with the
// slide at slidePath by resolving it through the SLIDE's own
// relationships part (ppt/slides/_rels/slideN.xml.rels), looking for
// the relationship whose Type identifies it as a notesSlide
// relationship — rather than assuming a matching numeric suffix
// (slide7.xml -> notesSlide7.xml), which usually but not always holds.
// It reports ok=false (with no error) if the slide has no notes or its
// rels file can't be read/parsed, since a missing/corrupt rels file
// for one slide's notes shouldn't abort extraction of everything else.
func resolveNotesPath(files map[string]*zip.File, slidePath string) (notesPath string, ok bool) {
	dir := path.Dir(slidePath)
	base := path.Base(slidePath)
	relsPath := path.Join(dir, "_rels", base+".rels")

	rf, found := files[relsPath]
	if !found {
		return "", false
	}
	rc, err := rf.Open()
	if err != nil {
		return "", false
	}
	defer rc.Close()

	rels, err := parseRelationships(rc)
	if err != nil {
		return "", false
	}
	for _, rel := range rels {
		if strings.HasSuffix(rel.Type, "/notesSlide") {
			return resolveRelTarget(dir, rel.Target), true
		}
	}
	return "", false
}
