package gofastodf

import (
	"testing"
	"time"
)

// Merged cells of a text-document table that each overlap one merge before
// them only at the far end of their row cost a scan of the row each. A
// few hundred bytes ask for hundreds of millions of steps unless the work
// is bounded, as it is for sheets.
func TestTableMergeOverlapBounded(t *testing.T) {
	const w = 8192
	row0 := `<table:table-row><table:table-cell table:number-columns-repeated="` + itoa(w-1) + `"><text:p>x</text:p></table:table-cell>` +
		`<table:table-cell table:number-rows-spanned="64"><text:p>y</text:p></table:table-cell></table:table-row>`
	rows := `<table:table-row table:number-rows-repeated="16"><table:table-cell table:number-columns-spanned="` + itoa(w) +
		`" table:number-columns-repeated="` + itoa(w-1) + `"><text:p>z</text:p></table:table-cell>` + cell("q") + `</table:table-row>`
	data := textDoc(t, "", `<table:table table:name="T">`+row0+rows+`</table:table>`)
	start := time.Now()
	doc := extract(t, data, nil)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	b := body(t, doc)
	if len(b) == 0 || b[0].Table == nil {
		t.Fatalf("no table: %s", summary(b))
	}
	// The first merge, down the last column, is kept.
	if m := b[0].Table.Merges; len(m) == 0 || m[0].Col != w-1 || m[0].RowSpan != 17 {
		t.Errorf("merges %+v", m[:min(len(m), 2)])
	}
}
