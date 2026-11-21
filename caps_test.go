package gofastodf

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// The fixed bounds that leave content out say so: limit.exceeded, once
// per bound on the part it concerns, with how much was left out.

// limitsOn returns the messages of the limit.exceeded warnings of a part
// (by its index), or of the document itself with part -1.
func limitsOn(doc *gofastcore.Document, part int) []string {
	ws := doc.Warnings
	if part >= 0 {
		ws = doc.Parts[part].Warnings
	}
	var out []string
	for _, w := range ws {
		if w.Code == gofastcore.WarnLimitExceeded {
			out = append(out, w.Message)
		}
	}
	return out
}

// saysOnce checks that exactly one limit.exceeded warning of the part (or
// of the document, with part -1) contains each of want, and that the
// warning has severity error.
func saysOnce(t *testing.T, doc *gofastcore.Document, part int, want ...string) {
	t.Helper()
	n := 0
	var all []string
	for _, m := range limitsOn(doc, part) {
		all = append(all, m)
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(m, w)
		}
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want one limit.exceeded with %q on part %d, have %q", want, part, all)
	}
	for _, w := range doc.AllWarnings() {
		if w.Code == gofastcore.WarnLimitExceeded && w.Severity != gofastcore.SeverityError {
			t.Errorf("severity %v: %q", w.Severity, w.Message)
		}
	}
}

// noLimits checks that nothing was said to be left out.
func noLimits(t *testing.T, doc *gofastcore.Document) {
	t.Helper()
	for _, w := range doc.AllWarnings() {
		if w.Code == gofastcore.WarnLimitExceeded {
			t.Errorf("unexpected limit.exceeded: %q", w.Message)
		}
	}
}

func sheetDoc(t *testing.T, tables string) []byte {
	t.Helper()
	return bodyDoc(t, sheetMIME, "", `<office:spreadsheet>`+tables+`</office:spreadsheet>`)
}

// A package whose media type is that of an OpenOffice.org 1 format is
// refused, as is one whose media type is not OpenDocument's and whose
// content has no OpenDocument body; one whose content has such a body is
// read by its body.
func TestNotReadIsRefused(t *testing.T) {
	ooo := `<office:document-content xmlns:office="http://openoffice.org/2000/office" xmlns:text="http://openoffice.org/2000/text">` +
		`<office:body><text:p>old</text:p></office:body></office:document-content>`
	for _, mime := range []string{"writer", "writer.template", "writer.global", "calc", "calc.template",
		"impress", "impress.template", "draw", "draw.template", "math"} {
		data := pkg(t, "application/vnd.sun.xml."+mime, member{name: "content.xml", data: ooo})
		if _, err := ExtractBytes(t.Context(), data, nil); !errors.Is(err, gofastcore.ErrUnsupported) {
			t.Errorf("%s: %v", mime, err)
		}
	}
	for name, data := range map[string][]byte{
		"unknown type, old content": pkg(t, "application/x-other", member{name: "content.xml", data: ooo}),
		"no type, old content":      pkg(t, "", member{name: "content.xml", data: ooo}),
		"no type, other XML":        pkg(t, "", member{name: "content.xml", data: `<?xml version="1.0"?><html><body><p>x</p></body></html>`}),
	} {
		if _, err := ExtractBytes(t.Context(), data, nil); !errors.Is(err, gofastcore.ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	doc := extract(t, pkg(t, "application/x-other", member{name: "content.xml", data: content("", `<text:p>new</text:p>`)}), nil)
	if doc.Source.Format != "odt" || len(body(t, doc)) != 1 {
		t.Errorf("OpenDocument body under another type: %+v", doc.Source)
	}
	// A content.xml that cannot be read as far as its body says nothing
	// of what the file is: the document is read as a damaged text
	// document, its headers and metadata kept, with a warning.
	styles := member{name: "styles.xml", data: stylesXML("", "", `<style:master-page style:name="Standard"><style:header><text:p>Head</text:p></style:header></style:master-page>`)}
	meta := member{name: "meta.xml", data: metaXML(`<dc:title>Kept</dc:title>`)}
	for name, c := range map[string]string{
		"not XML":   "not xml at all <<<",
		"cut short": content("", `<text:p>body</text:p>`)[:250],
	} {
		doc := extract(t, pkg(t, "", member{name: "content.xml", data: c}, styles, meta), nil)
		if doc.Source.Format != "odt" || doc.Metadata.Title != "Kept" || !hasWarning(doc, gofastcore.WarnPartFailed) {
			t.Errorf("%s: %+v, title %q, warnings %+v", name, doc.Source, doc.Metadata.Title, doc.AllWarnings())
		}
		if b := body(t, doc); len(b) == 0 || b[0].Text != "Head" {
			t.Errorf("%s: blocks %+v", name, b)
		}
	}
}

func TestTableColumnsCap(t *testing.T) {
	row := `<table:table-row table:number-rows-repeated="3"><table:table-cell table:number-columns-repeated="16384"/>` + cell("lost") + cell("lost too") + `<table:table-cell/></table:table-row>`
	doc := extract(t, textDoc(t, "", `<table:table>`+row+`</table:table><text:p>after</text:p>`), nil)
	saysOnce(t, doc, 0, "past column 16384", "(6 cells)")
	// Empty cells past the bound lose nothing.
	row = `<table:table-row><table:table-cell table:number-columns-repeated="16384"/><table:table-cell table:number-columns-repeated="9"/></table:table-row>`
	noLimits(t, extract(t, textDoc(t, "", `<table:table>`+row+`</table:table><text:p>after</text:p>`), nil))
}

func TestTableRowRepeatCap(t *testing.T) {
	row := `<table:table-row table:number-rows-repeated="1048581">` + cell("x") + `</table:table-row>`
	data := textDoc(t, "", `<table:table>`+row+`</table:table>`)
	doc, err := (&Extractor{MaxCells: 10}).Extract(t.Context(), bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	saysOnce(t, doc, 0, "repetitions of a table row past 1048576", "(5 rows)")
}

// A sheet's rows keep their addresses past the bound on other
// repetitions; rows past the sheet's own bound are said.
func TestSheetRowsCap(t *testing.T) {
	doc := extract(t, sheetDoc(t, `<table:table table:name="S"><table:table-row table:number-rows-repeated="2000000"><table:table-cell/></table:table-row>`+
		`<table:table-row>`+cell("end")+`</table:table-row></table:table>`), nil)
	noLimits(t, doc)
	if b := doc.Parts[0].Blocks; len(b) != 1 || b[0].Loc.Range != "A2000001" {
		t.Errorf("blocks %+v", b)
	}
	tables := `<table:table table:name="S"><table:table-row>` + cell("first") + `</table:table-row>` +
		`<table:table-row table:number-rows-repeated="16777215"><table:table-cell/></table:table-row>` +
		`<table:table-row table:number-rows-repeated="4">` + cell("lost") + `</table:table-row>` +
		`<table:table-row table:number-rows-repeated="7"><table:table-cell table:number-columns-repeated="3"/></table:table-row></table:table>`
	doc = extract(t, sheetDoc(t, tables), nil)
	saysOnce(t, doc, 0, "rows past row 16777216", "(4 rows)")
	// A row with content that runs over the bound: the rows past it.
	tables = `<table:table table:name="S"><table:table-row table:number-rows-repeated="16777214"><table:table-cell/></table:table-row>` +
		`<table:table-row table:number-rows-repeated="5">` + cell("x") + `</table:table-row></table:table>`
	data := sheetDoc(t, tables)
	doc, err := (&Extractor{MaxCells: 100}).Extract(t.Context(), bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	saysOnce(t, doc, 0, "rows past row 16777216", "(3 rows)")
}

func TestSheetColumnsCap(t *testing.T) {
	row := `<table:table-row table:number-rows-repeated="3"><table:table-cell table:number-columns-repeated="65535"/>` +
		cell("kept", `table:number-columns-repeated="2"`) + cell("lost") + `<table:table-cell/><table:covered-table-cell/></table:table-row>`
	doc := extract(t, sheetDoc(t, `<table:table table:name="S">`+row+`</table:table>`), nil)
	saysOnce(t, doc, 0, "past column 65536", "(6 cells)")
	if b := doc.Parts[0].Blocks; len(b) != 1 || b[0].Loc.Range != "CRXP1:CRXP3" {
		t.Errorf("blocks %+v", b)
	}
	// Cells past the bound that hold nothing (no cell in the sheet with
	// as little would be kept) lose nothing.
	row = `<table:table-row><table:table-cell table:number-columns-repeated="65536"/><table:table-cell table:number-columns-repeated="4"/>` +
		`<table:table-cell><text:p/></table:table-cell><table:table-cell office:value-type="string"><text:p> </text:p></table:table-cell></table:table-row><table:table-row>` + cell("x") + `</table:table-row>` +
		`<table:table-row table:number-rows-repeated="16777214"/><table:table-row table:number-rows-repeated="3">` +
		`<table:table-cell><text:p/></table:table-cell></table:table-row>`
	noLimits(t, extract(t, sheetDoc(t, `<table:table table:name="S">`+row+`</table:table>`), nil))

	// A cell past the bound that only spans, and one whose long value is
	// left out with it: the cell is counted, its value is not cut.
	row = `<table:table-row><table:table-cell table:number-columns-repeated="65535"/>` +
		`<table:table-cell table:number-columns-spanned="2" table:number-columns-repeated="3"/>` +
		`<table:table-cell office:value-type="float" office:value="` + strings.Repeat("9", 70000) + `"/></table:table-row><table:table-row>` + cell("x") + `</table:table-row>`
	doc = extract(t, sheetDoc(t, `<table:table table:name="S">`+row+`</table:table>`), nil)
	saysOnce(t, doc, 0, "past column 65536", "(1 cells)")
	if n := len(limitsOn(doc, 0)); n != 1 {
		t.Errorf("%q", limitsOn(doc, 0))
	}
	// Cells past the bound are counted for the rows given, not those a
	// limit stopped before.
	row = `<table:table-row table:number-rows-repeated="10"><table:table-cell table:number-columns-repeated="65535"/>` +
		cell("in") + cell("out") + `</table:table-row>`
	data := sheetDoc(t, `<table:table table:name="S">`+row+`</table:table>`)
	doc, err := (&Extractor{MaxCells: 3}).Extract(t.Context(), bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	saysOnce(t, doc, 0, "past column 65536", "(3 cells)")
}

// A cell's stored value, given as its text, and its formula are content:
// they are kept past the length of a label, and cut, with a warning, only
// past 64 KiB.
func TestCellValueCap(t *testing.T) {
	long := strings.Repeat("1", 600)
	formula := "of:=" + strings.Repeat("[.A1]+", 200) + "1"
	row := `<table:table-row><table:table-cell office:value-type="float" office:value="` + long + `"/>` +
		cell("3", `table:formula="`+formula+`" office:value-type="float" office:value="3"`) + `</table:table-row>`
	doc := extract(t, sheetDoc(t, `<table:table table:name="S">`+row+`</table:table>`), &gofastcore.Options{Cells: true})
	noLimits(t, doc)
	tb := doc.Parts[0].Blocks[0].Table
	if tb.Rows[0][0] != long || len(tb.Cells) == 0 || tb.Cells[len(tb.Cells)-1].Formula != formula {
		t.Errorf("cells %q %+v", tb.Rows[0], tb.Cells)
	}

	huge := strings.Repeat("2", 70000)
	row = `<table:table-row><table:table-cell office:value-type="float" office:value="` + huge + `"/>` +
		cell("3", `table:formula="of:=`+huge+`" office:value-type="float" office:value="3"`) + `</table:table-row>`
	doc = extract(t, sheetDoc(t, `<table:table table:name="S">`+row+`</table:table>`), &gofastcore.Options{Cells: true})
	saysOnce(t, doc, 0, "cell values longer than 65536 bytes", "(1 values)")
	saysOnce(t, doc, 0, "cell formulas longer than 65536 bytes", "(1 formulas)")
	if got := doc.Parts[0].Blocks[0].Table.Rows[0][0]; len(got) != 65536 {
		t.Errorf("value of %d bytes", len(got))
	}
}

func TestValidationsCap(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a document of several MB")
	}
	var sb strings.Builder
	sb.WriteString(`<table:content-validations>`)
	for i := range 16384 + 2 {
		fmt.Fprintf(&sb, `<table:content-validation table:name="v%d" table:condition="of:cell-content-is-in-list(&quot;a&quot;;&quot;b&quot;)"/>`, i)
	}
	sb.WriteString(`</table:content-validations><table:table table:name="S"><table:table-row>` + cell("x", `table:content-validation-name="v16385"`) + `</table:table-row></table:table>`)
	doc := extract(t, sheetDoc(t, sb.String()), nil)
	saysOnce(t, doc, -1, "content validations past the first 16384", "(2 validations)")
}

func TestFormulaCap(t *testing.T) {
	obj := func(math string) string {
		return `<text:p><draw:frame draw:name="F"><draw:object><math:math><math:semantics>` + math + `</math:semantics></math:math></draw:object></draw:frame></text:p>`
	}
	long := strings.Repeat("m", 70000)
	doc := extract(t, textDoc(t, "", obj(`<math:mrow><math:mi>`+long+`</math:mi><math:mi>tail</math:mi></math:mrow>`)), nil)
	saysOnce(t, doc, 0, "formula text longer than 65536 bytes", "(1 formulas)")
	// A StarMath source over the bound is kept up to it, and is the text
	// when the MathML gives none.
	star := strings.Repeat("x+", 35000) + "y"
	doc = extract(t, textDoc(t, "", obj(`<math:mrow/><math:annotation encoding="StarMath 5.0">`+star+`</math:annotation>`)), nil)
	saysOnce(t, doc, 0, "formula text longer than 65536 bytes", "(1 formulas)")
	if b := body(t, doc); len(b) != 1 || b[0].Kind != gofastcore.KindFormula || len(b[0].Text) != 65536 || b[0].Attrs["starmath"] != b[0].Text {
		t.Errorf("blocks %d", len(b))
	}
	// Under the bound, nothing is said.
	doc = extract(t, textDoc(t, "", obj(`<math:mrow/><math:annotation encoding="StarMath 5.0">`+star[:60000]+`</math:annotation>`)), nil)
	noLimits(t, doc)
}

func TestChartCaps(t *testing.T) {
	chart := func(extra, rows string) string {
		return `<text:p><draw:frame draw:name="C"><draw:object><chart:chart chart:class="chart:bar">` + extra +
			`<table:table table:name="local-table">` + rows + `</table:table></chart:chart></draw:object></draw:frame></text:p>`
	}
	doc := extract(t, textDoc(t, "", chart("", `<table:table-row table:number-rows-repeated="16390">`+cell("r")+`</table:table-row>`+
		`<table:table-row>`+cell("after")+`</table:table-row><table:table-row table:number-rows-repeated="9"><table:table-cell/></table:table-row>`)), nil)
	saysOnce(t, doc, 0, "rows of a chart's table past 16384", "(7 rows)")
	doc = extract(t, textDoc(t, "", chart("", `<table:table-row table:number-rows-repeated="2">`+cell("c", `table:number-columns-repeated="1030"`)+`<table:table-cell table:number-columns-repeated="5"/></table:table-row>`)), nil)
	saysOnce(t, doc, 0, "cells of a chart's table past column 1024", "(12 cells)")
	doc = extract(t, textDoc(t, "", chart("", `<table:table-row>`+cell(strings.Repeat("v", 70000))+`</table:table-row>`)), nil)
	saysOnce(t, doc, 0, "cells of a chart's table longer than 65536 bytes", "(1 cells)")
	if got := body(t, doc)[0].Table.Rows[0][0]; len(got) != 65536 {
		t.Errorf("cell of %d bytes", len(got))
	}
	// A long cell in a row past the last row kept is counted with the
	// row, not as a cell cut.
	doc = extract(t, textDoc(t, "", chart("", `<table:table-row table:number-rows-repeated="16384">`+cell("r")+`</table:table-row>`+
		`<table:table-row>`+cell(strings.Repeat("v", 70000))+`</table:table-row>`)), nil)
	saysOnce(t, doc, 0, "rows of a chart's table past 16384", "(1 rows)")
	if n := len(limitsOn(doc, 0)); n != 1 {
		t.Errorf("%d warnings: %q", n, limitsOn(doc, 0))
	}
	axis := `<chart:plot-area><chart:axis chart:dimension="x"><chart:title><text:p>` + strings.Repeat("X", 600) + `</text:p></chart:title></chart:axis>` +
		`<chart:axis chart:dimension="y"><chart:title><text:p>` + strings.Repeat("Y", 600) + `</text:p></chart:title></chart:axis></chart:plot-area>`
	doc = extract(t, textDoc(t, "", chart(axis, `<table:table-row>`+cell("1")+`</table:table-row>`)), nil)
	saysOnce(t, doc, 0, "labels longer than 512 bytes", "(2 labels)")
}

func TestLabelAndAddressCaps(t *testing.T) {
	long := strings.Repeat("C", 700)
	b := `<text:p>body<text:note text:id="n1" text:note-class="footnote"><text:note-citation>` + long + `</text:note-citation>` +
		`<text:note-body><text:p>note</text:p></text:note-body></text:note></text:p>` +
		`<text:list><text:list-item><text:number>` + long + `</text:number><text:p>item</text:p></text:list-item></text:list>` +
		`<text:p><text:a xlink:href="https://example.com/` + strings.Repeat("q", 9000) + `">link</text:a></text:p>`
	doc := extract(t, textDoc(t, "", b), &gofastcore.Options{Spans: true})
	saysOnce(t, doc, 0, "labels longer than 512 bytes", "(2 labels)")
	saysOnce(t, doc, 0, "link addresses longer than 8192 bytes", "(1 addresses)")
	// Without spans no address is kept, and none is cut.
	doc = extract(t, textDoc(t, "", b), nil)
	if len(limitsOn(doc, 0)) != 1 {
		t.Errorf("without spans: %q", limitsOn(doc, 0))
	}
}

func TestListIDsCap(t *testing.T) {
	if testing.Short() {
		t.Skip("builds documents of several MB")
	}
	const what = "lists continue one whose identifier or style name was not remembered"
	// LibreOffice gives every list an xml:id: lists past the bound that
	// nothing continues lose nothing.
	var sb strings.Builder
	for i := range 17000 {
		fmt.Fprintf(&sb, `<text:list xml:id="list%d" text:style-name="L"><text:list-item><text:p>p</text:p></text:list-item></text:list>`, i)
	}
	lists := sb.String()
	noLimits(t, extract(t, textDoc(t, "", lists), nil))
	// One that continues a list whose identifier was not kept; one that
	// names a list there never was is not counted.
	doc := extract(t, textDoc(t, "", lists+
		`<text:list text:continue-list="list16999" text:style-name="L"><text:list-item><text:p>q</text:p></text:list-item></text:list>`+
		`<text:list text:continue-list="nowhere" text:style-name="L"><text:list-item><text:p>r</text:p></text:list-item></text:list>`), nil)
	saysOnce(t, doc, 0, what, "(1 lists)")

	// Numbered paragraphs: a list identifier met again after it was not
	// kept.
	sb.Reset()
	for i := range 16384 + 1 {
		fmt.Fprintf(&sb, `<text:numbered-paragraph text:list-id="L%d"><text:p>p</text:p></text:numbered-paragraph>`, i)
	}
	noLimits(t, extract(t, textDoc(t, "", sb.String()), nil))
	sb.WriteString(`<text:numbered-paragraph text:list-id="L16384"><text:p>again</text:p></text:numbered-paragraph>`)
	saysOnce(t, extract(t, textDoc(t, "", sb.String()), nil), 0, what, "(1 lists)")

	// Style names: one remembered is updated when the map is full; a
	// list that continues the numbering of a style not kept is counted.
	sb.Reset()
	for i := range 16384 {
		fmt.Fprintf(&sb, `<text:list text:style-name="S%d"><text:list-item><text:p>p</text:p></text:list-item></text:list>`, i)
	}
	sb.WriteString(`<text:list text:style-name="S0" text:continue-numbering="true"><text:list-item><text:p>again</text:p></text:list-item></text:list>`)
	sb.WriteString(`<text:list text:style-name="Snew"><text:list-item><text:p>new</text:p></text:list-item></text:list>`)
	noLimits(t, extract(t, textDoc(t, "", sb.String()), nil))
	sb.WriteString(`<text:list text:style-name="Snew" text:continue-numbering="true"><text:list-item><text:p>on</text:p></text:list-item></text:list>`)
	saysOnce(t, extract(t, textDoc(t, "", sb.String()), nil), 0, what, "(1 lists)")
}

func TestFormCaps(t *testing.T) {
	var sb strings.Builder
	if !testing.Short() { // a document of several MB
		sb.WriteString(`<office:forms><form:form>`)
		for i := range 16384 + 3 {
			fmt.Fprintf(&sb, `<form:text form:id="c%d" form:current-value="v"/>`, i)
		}
		sb.WriteString(`</form:form></office:forms><text:p>body</text:p>`)
		doc := extract(t, textDoc(t, "", sb.String()), nil)
		saysOnce(t, doc, 0, "form controls past the first 16384", "(3 controls)")
	}

	sb.Reset()
	sb.WriteString(`<office:forms><form:form><form:listbox form:id="lb">`)
	for i := range 4096 + 4 {
		fmt.Fprintf(&sb, `<form:option form:label="o%d"/>`, i)
	}
	sb.WriteString(`</form:listbox></form:form></office:forms><text:p><draw:control draw:control="lb"/></text:p>`)
	doc := extract(t, textDoc(t, "", sb.String()), nil)
	saysOnce(t, doc, 0, "options of a form list past the first 4096", "(4 options)")

	sb.Reset()
	sb.WriteString(`<text:p><text:drop-down text:name="d">`)
	for i := range 4096 + 5 {
		fmt.Fprintf(&sb, `<text:label text:value="v%d"/>`, i)
	}
	sb.WriteString(`v0</text:drop-down></text:p>`)
	doc = extract(t, textDoc(t, "", sb.String()), nil)
	saysOnce(t, doc, 0, "values of a drop-down field past the first 4096", "(5 values)")
}

func TestChildrenCap(t *testing.T) {
	var sb strings.Builder
	members := []member{}
	for i := range 4096 + 4 {
		name := fmt.Sprintf("Obj%d", i)
		fmt.Fprintf(&sb, `<text:p><draw:frame><draw:object-ole xlink:href="./%s"/></draw:frame></text:p>`, name)
		members = append(members, member{name: name, data: "x"})
	}
	doc := extract(t, textDoc(t, "", sb.String(), members...), nil)
	if len(doc.Children) != 4096 {
		t.Errorf("%d children", len(doc.Children))
	}
	saysOnce(t, doc, 0, "embedded documents past the first 4096", "(4 documents)")
}

// A picture held in a flat file whose bytes the decompression limit cannot
// hold is said, as one in a package is.
func TestInlinePictureBudget(t *testing.T) {
	flat := `<?xml version="1.0" encoding="UTF-8"?><office:document` + namespaces + ` office:version="1.3" office:mimetype="application/vnd.oasis.opendocument.text">` +
		`<office:body><office:text><text:p>text</text:p><text:p><draw:frame><draw:image><office:binary-data>` + strings.Repeat("QUJD", 100) +
		`</office:binary-data></draw:image></draw:frame></text:p></office:text></office:body></office:document>`
	doc := extract(t, []byte(flat), &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 100}})
	saysOnce(t, doc, 0, "limit on decompressed bytes", "(1 pictures)")
	noLimits(t, extract(t, []byte(flat), nil))
}

func TestStylesAndManifestCaps(t *testing.T) {
	if testing.Short() {
		t.Skip("builds documents of several MB")
	}
	var sb strings.Builder
	for i := range 1<<16 + 1 {
		fmt.Fprintf(&sb, `<style:style style:name="s%d" style:family="paragraph"/>`, i)
	}
	doc := extract(t, textDoc(t, "", `<text:p>x</text:p>`, member{name: "styles.xml", data: stylesXML(sb.String(), "", "")}), nil)
	saysOnce(t, doc, -1, "more than 65536 styles")

	// The manifest, written directly: pkg lists only real members.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	w.Write([]byte(textMIME))
	w, _ = zw.Create("content.xml")
	w.Write([]byte(content("", `<text:p>x</text:p>`)))
	w, _ = zw.Create("META-INF/manifest.xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><manifest:manifest` + namespaces + `>`))
	for i := range 1<<17 + 1 {
		fmt.Fprintf(w, `<manifest:file-entry manifest:full-path="m%d" manifest:media-type=""/>`, i)
	}
	w.Write([]byte(`</manifest:manifest>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	doc = extract(t, buf.Bytes(), nil)
	saysOnce(t, doc, -1, "manifest.xml has more than 131072 entries")
	if hasWarning(doc, WarnPartDamaged) {
		t.Error("the manifest is said damaged")
	}
}

// A bound is said on the part where content was lost, and only there; a
// part not read loses nothing and says nothing.
func TestCapWarningsOnTheirPart(t *testing.T) {
	over := `<table:table-row><table:table-cell table:number-columns-repeated="65536"/>` + cell("lost") + `</table:table-row>`
	sheets := `<table:table table:name="A">` + over + `</table:table>` +
		`<table:table table:name="B"><table:table-row>` + cell("b") + `</table:table-row></table:table>` +
		`<table:table table:name="C">` + over + over + `</table:table>`
	doc := extract(t, sheetDoc(t, sheets), nil)
	saysOnce(t, doc, 0, "past column 65536", "(1 cells)")
	saysOnce(t, doc, 2, "past column 65536", "(2 cells)")
	if len(limitsOn(doc, 1)) != 0 || len(limitsOn(doc, -1)) != 0 {
		t.Errorf("sheet B %q, document %q", limitsOn(doc, 1), limitsOn(doc, -1))
	}
	// Only sheet 3: its warning, and none of sheet 1's.
	doc = extract(t, sheetDoc(t, sheets), &gofastcore.Options{Parts: gofastcore.PartRange{First: 3, Last: 3}})
	if len(doc.Parts) != 1 || doc.Parts[0].Number != 3 {
		t.Fatalf("parts %+v", doc.Parts)
	}
	saysOnce(t, doc, 0, "past column 65536", "(2 cells)")
	if len(limitsOn(doc, -1)) != 0 || len(doc.Parts[0].Warnings) != 1 {
		t.Errorf("document %q, sheet %+v", limitsOn(doc, -1), doc.Parts[0].Warnings)
	}

	// Slides: a cut label on slide 2; a cut label in a master page, said
	// on each slide that shows it (1 and 3).
	long := strings.Repeat("N", 700)
	master := `<style:master-page style:name="M"><draw:frame svg:y="1cm"><draw:text-box><text:list><text:list-item><text:number>` + long +
		`</text:number><text:p>m</text:p></text:list-item></text:list></draw:text-box></draw:frame></style:master-page><style:master-page style:name="P"/>`
	slide := func(m, text string) string {
		return `<draw:page draw:master-page-name="` + m + `"><draw:frame><draw:text-box>` + text + `</draw:text-box></draw:frame></draw:page>`
	}
	cutLabel := `<text:list><text:list-item><text:number>` + long + `</text:number><text:p>two</text:p></text:list-item></text:list>`
	body := `<office:presentation>` + slide("M", `<text:p>one</text:p>`) + slide("P", cutLabel) +
		slide("M", `<text:p>three</text:p>`) + slide("M", cutLabel) + `</office:presentation>`
	doc = extract(t, bodyDoc(t, slideMIME, "", body, member{name: "styles.xml", data: stylesXML("", "", master)}), nil)
	for i := range 3 {
		saysOnce(t, doc, i, "labels longer than 512 bytes", "(1 labels)")
	}
	// A slide's own cut and its master's: one warning for the bound.
	saysOnce(t, doc, 3, "labels longer than 512 bytes", "(2 labels)")
	if n := len(limitsOn(doc, 3)); n != 1 {
		t.Errorf("slide 4: %q", limitsOn(doc, 3))
	}
	if len(limitsOn(doc, -1)) != 0 {
		t.Errorf("document %q", limitsOn(doc, -1))
	}
}
