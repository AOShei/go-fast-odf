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
