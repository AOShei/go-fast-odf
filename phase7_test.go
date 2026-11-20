package gofastodf

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Formulas stored without a result show nothing; one warning per sheet
// says how many, and the formulas stay in Table.Cells.
func TestFormulaNoValue(t *testing.T) {
	body := `<office:spreadsheet><table:table table:name="S">` +
		`<table:table-row>` + cell("x") + `<table:table-cell table:formula="of:=A1*2"/>` +
		`<table:table-cell table:formula="of:=1" table:number-columns-repeated="2"/>` +
		`<table:table-cell office:value-type="float" office:value="0" table:formula="of:=0"/>` +
		`<table:table-cell table:formula="of:=&quot;a&quot;"><text:p>a</text:p></table:table-cell></table:table-row>` +
		`</table:table><table:table table:name="T"><table:table-row>` +
		`<table:table-cell office:value-type="float" office:value="3" table:formula="of:=1+2"/></table:table-row></table:table></office:spreadsheet>`
	doc := extract(t, bodyDoc(t, sheetMIME, "", body), &gofastcore.Options{Cells: true})
	w := doc.Parts[0].Warnings
	if len(w) != 1 || w[0].Code != WarnFormulaNoValue || !strings.HasPrefix(w[0].Message, "3 ") {
		t.Fatalf("warnings %+v", w)
	}
	if len(doc.Parts[1].Warnings) != 0 {
		t.Errorf("sheet with values: %+v", doc.Parts[1].Warnings)
	}
	n := 0
	for _, c := range doc.Parts[0].Blocks[0].Table.Cells {
		if c.Formula != "" {
			n++
		}
	}
	if n != 5 {
		t.Errorf("%d formulas in cells", n)
	}
}

// Text a master page shows of its own is given once per master, on the
// first slide that uses it (the first shown one); its placeholders are
// not content.
func TestMasterPageText(t *testing.T) {
	auto := `<style:page-layout style:name="PM1"><style:page-layout-properties fo:page-width="28cm" fo:page-height="20cm"/></style:page-layout>`
	hidden := `<style:style style:name="dp-hidden" style:family="drawing-page"><style:drawing-page-properties presentation:visibility="hidden"/></style:style>`
	frame := func(attrs, text string) string {
		return `<draw:frame ` + attrs + `><draw:text-box><text:p>` + text + `</text:p></draw:text-box></draw:frame>`
	}
	master := `<style:master-page style:name="Default" style:page-layout-name="PM1">` +
		frame(`presentation:class="title" svg:y="1cm"`, "Click to add Title") +
		frame(`presentation:class="outline" svg:y="5cm"`, "Click to add Text") +
		frame(`presentation:class="footer" svg:y="18cm"`, "Master footer") +
		frame(`presentation:class="page-number" svg:y="18cm"`, "&lt;number&gt;") +
		frame(`svg:x="20cm" svg:y="18cm"`, "ACME Ltd") +
		frame(`svg:x="1cm" svg:y="0.5cm"`, "Confidential") +
		`<presentation:notes><draw:frame presentation:class="notes"><draw:text-box><text:p>Notes master</text:p></draw:text-box></draw:frame></presentation:notes>` +
		`</style:master-page>` +
		`<style:master-page style:name="Unused" style:page-layout-name="PM1">` + frame(`svg:y="1cm"`, "Never shown") + `</style:master-page>`
	page := func(attrs, text string) string {
		return `<draw:page draw:master-page-name="Default" ` + attrs + `>` + frame(`svg:y="5cm"`, text) +
			`<presentation:notes>` + frame(`presentation:class="notes"`, "notes "+text) + `</presentation:notes></draw:page>`
	}
	body := `<office:presentation>` + page(`draw:style-name="dp-hidden"`, "one") + page("", "two") + page("", "three") + `</office:presentation>`
	data := bodyDoc(t, slideMIME, hidden, body, member{name: "styles.xml", data: stylesXML("", auto, master)})
	doc := extract(t, data, nil)
	if len(doc.Parts) != 3 {
		t.Fatalf("%d parts", len(doc.Parts))
	}
	check(t, doc.Parts[0].Blocks, `
paragraph{hidden} one
note{hidden} notes one`)
	check(t, doc.Parts[1].Blocks, `
paragraph two
header Confidential
footer ACME Ltd
note notes two`)
	check(t, doc.Parts[2].Blocks, `
paragraph three
note notes three`)
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
}

// Header rows come from what the file declares: table:table-header-rows
// (print titles) and database ranges that contain a header.
func TestSheetHeaderRows(t *testing.T) {
	row := func(a, b string) string { return `<table:table-row>` + cell(a) + cell(b) + `</table:table-row>` }
	gap := `<table:table-row><table:table-cell/></table:table-row>`
	body := `<office:spreadsheet>` +
		`<table:table table:name="Print">` + gap +
		`<table:table-header-rows>` + row("H1", "H2") + `</table:table-header-rows>` + row("a", "b") + row("c", "d") + `</table:table>` +
		`<table:table table:name="My DB">` + row("Name", "Age") + row("Ann", "3") +
		gap + row("x", "y") + row("z", "w") + gap + row("p", "q") + row("r", "s") + `</table:table>` +
		`<table:table table:name="Plain">` + row("Bold", "Head") + row("1", "2") + `</table:table>` +
		`<table:database-ranges>` +
		`<table:database-range table:name="D1" table:target-range-address="'My DB'.A1:'My DB'.B2"/>` +
		`<table:database-range table:name="D2" table:contains-header="false" table:target-range-address="'My DB'.A4:.B5"/>` +
		`<table:database-range table:name="D3" table:target-range-address="'My DB'.A7:'My DB'.B9"/>` +
		`</table:database-ranges></office:spreadsheet>`
	doc := extract(t, bodyDoc(t, sheetMIME, "", body), nil)
	want := [][]int{{1}, {1, 0, 0}, {0}}
	for i := range want {
		p := doc.Parts[i]
		var got []int
		for _, b := range p.Blocks {
			if b.Kind == gofastcore.KindTable {
				got = append(got, b.Table.HeaderRows)
			}
		}
		if len(got) != len(want[i]) {
			t.Fatalf("sheet %d: header rows %v, want %v", i, got, want[i])
		}
		for j := range got {
			if got[j] != want[i][j] {
				t.Errorf("sheet %d: header rows %v, want %v", i, got, want[i])
			}
		}
	}
	if err := doc.Validate(); err != nil {
		t.Error(err)
	}
}
