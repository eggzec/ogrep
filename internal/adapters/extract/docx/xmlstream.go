package docx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"officegrep/internal/core/domain"
)

// send is the callback shape used throughout this file: it delivers one
// TextUnit and reports whether the caller should keep going (false means
// the context was cancelled, so the streaming loop should stop early
// without treating that as an error).
type send func(domain.TextUnit) bool

// readCharData accumulates character data up to (and consuming) the
// matching end element for the currently-open leaf element (identified
// by localName, e.g. "t"). Real WordprocessingML w:t elements never
// contain nested elements, so this simple loop — collect CharData,
// stop at the first EndElement — is sufficient without tracking depth.
func readCharData(dec *xml.Decoder, localName string) (string, error) {
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.EndElement:
			if t.Name.Local == localName {
				return sb.String(), nil
			}
		}
	}
}

// attrValue returns the value of the first attribute on t whose local
// name matches, ignoring namespace, or "" if absent.
func attrValue(t xml.StartElement, localName string) string {
	for _, a := range t.Attr {
		if a.Name.Local == localName {
			return a.Value
		}
	}
	return ""
}

// runTracker holds the small bit of state needed to build up the text of
// one in-progress paragraph while streaming: whether we're currently
// inside a w:r (run) — which is what makes w:t/w:tab/w:br/w:cr
// meaningful — and the builder for the paragraph currently open (nil
// when we're not inside a w:p at all).
type runTracker struct {
	inRun   bool
	curPara *strings.Builder
}

// handleStart processes a StartElement that is one of the "leaf content"
// elements common to every part (t/tab/br/cr/r), given the current
// paragraph state. It returns true if it consumed the token itself
// (i.e. the caller doesn't need to do anything else for it).
func (rt *runTracker) handleStart(dec *xml.Decoder, t xml.StartElement) error {
	switch t.Name.Local {
	case "r":
		rt.inRun = true
	case "t":
		if rt.inRun && rt.curPara != nil {
			txt, err := readCharData(dec, "t")
			if err != nil {
				return err
			}
			rt.curPara.WriteString(txt)
		}
	case "tab":
		// Only a run-content w:tab (an actual inserted tab character)
		// should be honored here. A w:tab can also appear inside
		// w:pPr/w:tabs as a tab-STOP definition, which is unrelated to
		// inserted text — tracking inRun (only true between w:r start
		// and end) is what keeps that case from being misread.
		if rt.inRun && rt.curPara != nil {
			rt.curPara.WriteString("\t")
		}
	case "br", "cr":
		if rt.inRun && rt.curPara != nil {
			rt.curPara.WriteString("\n")
		}
	}
	return nil
}

func (rt *runTracker) handleEnd(t xml.EndElement) {
	if t.Name.Local == "r" {
		rt.inRun = false
	}
}

// tableState tracks the current row/col cursor for one open w:tbl.
type tableState struct {
	num, row, col int
}

// cellState accumulates the (possibly multi-paragraph) text of one open
// w:tc, to be emitted as a single TextUnit when the cell closes.
type cellState struct {
	table, row, col int
	builder         strings.Builder
}

// extractDocumentBody streams word/document.xml, emitting one
// domain.UnitParagraph per top-level body paragraph and one
// domain.UnitTableCell per non-blank table cell (concatenating all of
// the cell's paragraphs). See the package doc comment for the numbering
// and double-counting rules this implements.
func extractDocumentBody(f *zip.File, out send) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	rt := &runTracker{}

	var paragraphNum int
	var tableCounter int
	var tableStack []tableState
	var cellStack []*cellState

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decoding word/document.xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				rt.curPara = &strings.Builder{}
			case "tbl":
				tableCounter++
				tableStack = append(tableStack, tableState{num: tableCounter})
			case "tr":
				if n := len(tableStack); n > 0 {
					tableStack[n-1].row++
					tableStack[n-1].col = 0
				}
			case "tc":
				if n := len(tableStack); n > 0 {
					tableStack[n-1].col++
					top := tableStack[n-1]
					cellStack = append(cellStack, &cellState{table: top.num, row: top.row, col: top.col})
				}
			default:
				if err := rt.handleStart(dec, t); err != nil {
					return fmt.Errorf("decoding word/document.xml: %w", err)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				text := ""
				if rt.curPara != nil {
					text = rt.curPara.String()
				}
				rt.curPara = nil
				if n := len(cellStack); n > 0 {
					cell := cellStack[n-1]
					if cell.builder.Len() > 0 {
						cell.builder.WriteString("\n")
					}
					cell.builder.WriteString(text)
				} else {
					paragraphNum++
					unit := domain.TextUnit{
						Kind: domain.UnitParagraph,
						Location: domain.Location{
							Format:    "docx",
							Paragraph: paragraphNum,
							Human:     fmt.Sprintf("Paragraph %d", paragraphNum),
						},
						Text: text,
					}
					if !out(unit) {
						return nil
					}
				}
			case "tc":
				if n := len(cellStack); n > 0 {
					cell := cellStack[n-1]
					cellStack = cellStack[:n-1]
					text := cell.builder.String()
					if strings.TrimSpace(text) != "" {
						unit := domain.TextUnit{
							Kind: domain.UnitTableCell,
							Location: domain.Location{
								Format: "docx",
								Table:  cell.table,
								Row:    cell.row,
								Col:    cell.col,
								Human:  fmt.Sprintf("Table %d, Row %d, Cell %d", cell.table, cell.row, cell.col),
							},
							Text: text,
						}
						if !out(unit) {
							return nil
						}
					}
				}
			case "tbl":
				if n := len(tableStack); n > 0 {
					tableStack = tableStack[:n-1]
				}
			default:
				rt.handleEnd(t)
			}
		}
	}

	return nil
}

// extractHeaderFooterPart streams a word/header*.xml or word/footer*.xml
// part, emitting one domain.UnitHeaderFooter per non-blank paragraph,
// all sharing the given label (e.g. "Header 1"). Table structure inside
// headers/footers is deliberately not tracked; see the package doc
// comment for why that's safe here.
func extractHeaderFooterPart(f *zip.File, label string, out send) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	rt := &runTracker{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decoding %s: %w", f.Name, err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				rt.curPara = &strings.Builder{}
			default:
				if err := rt.handleStart(dec, t); err != nil {
					return fmt.Errorf("decoding %s: %w", f.Name, err)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				text := ""
				if rt.curPara != nil {
					text = rt.curPara.String()
				}
				rt.curPara = nil
				if strings.TrimSpace(text) != "" {
					unit := domain.TextUnit{
						Kind: domain.UnitHeaderFooter,
						Location: domain.Location{
							Format: "docx",
							Human:  label,
						},
						Text: text,
					}
					if !out(unit) {
						return nil
					}
				}
			default:
				rt.handleEnd(t)
			}
		}
	}

	return nil
}

// extractFootnoteLike streams word/footnotes.xml or word/comments.xml,
// where elemName is "footnote" or "comment" respectively: each such
// element becomes one TextUnit of the given kind, concatenating all of
// its paragraphs (joined by "\n"). Location.Human uses the element's own
// w:id attribute ("Footnote 3"/"Comment 2"), falling back to a running
// counter if that attribute is missing or unparsable as a plain string
// (in practice w:id is always present on real documents; the fallback
// only guards against malformed input).
func extractFootnoteLike(f *zip.File, elemName string, kind domain.UnitKind, labelPrefix string, out send) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	rt := &runTracker{}

	var inItem bool
	var itemBuilder strings.Builder
	var itemID string
	var counter int

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decoding %s: %w", f.Name, err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case elemName:
				inItem = true
				itemBuilder.Reset()
				counter++
				itemID = attrValue(t, "id")
				if itemID == "" {
					itemID = fmt.Sprintf("%d", counter)
				}
			case "p":
				if inItem {
					rt.curPara = &strings.Builder{}
				}
			default:
				if err := rt.handleStart(dec, t); err != nil {
					return fmt.Errorf("decoding %s: %w", f.Name, err)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				if inItem && rt.curPara != nil {
					if itemBuilder.Len() > 0 {
						itemBuilder.WriteString("\n")
					}
					itemBuilder.WriteString(rt.curPara.String())
					rt.curPara = nil
				}
			case elemName:
				if inItem {
					text := itemBuilder.String()
					inItem = false
					if strings.TrimSpace(text) != "" {
						unit := domain.TextUnit{
							Kind: kind,
							Location: domain.Location{
								Format: "docx",
								Human:  fmt.Sprintf("%s %s", labelPrefix, itemID),
							},
							Text: text,
						}
						if !out(unit) {
							return nil
						}
					}
				}
			default:
				rt.handleEnd(t)
			}
		}
	}

	return nil
}
