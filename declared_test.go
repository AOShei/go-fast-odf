package gofastodf

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Declarations not covered elsewhere: a section recorded hidden, rows
// hidden by a filter, a collapsed column, a sheet with table:display
// "false". And what is not a declaration: a condition with no recorded
// result, and draw:display="printer".
func TestDeclaredMechanisms(t *testing.T) {
	text := textDoc(t, "",
		`<text:section text:name="A" text:display="condition" text:condition="ooow:x" text:is-hidden="true"><text:p>Recorded hidden</text:p></text:section>`+
			`<text:section text:name="B" text:display="condition" text:condition="ooow:x"><text:p>Only a condition</text:p></text:section>`+
			`<text:p>Shown<draw:frame draw:display="printer"><draw:text-box><text:p>Printed only</text:p></draw:text-box></draw:frame></text:p>`)
	check(t, body(t, extract(t, text, nil)), `
paragraph{hidden} Recorded hidden
paragraph Only a condition
paragraph Shown
paragraph Printed only`)

	auto := `<style:style style:name="ta-off" style:family="table"><style:table-properties table:display="false"/></style:style>`
	sheet := `<office:spreadsheet><table:table table:name="Data">` +
		`<table:table-column/><table:table-column table:visibility="collapse"/>` +
		`<table:table-row>` + cell("a") + cell("b") + `</table:table-row>` +
		`<table:table-row table:visibility="filter">` + cell("c") + cell("d") + `</table:table-row></table:table>` +
		`<table:table table:name="Off" table:style-name="ta-off"><table:table-row>` + cell("e") + `</table:table-row></table:table></office:spreadsheet>`
	doc := extract(t, bodyDoc(t, sheetMIME, auto, sheet), nil)
	tb := doc.Parts[0].Blocks[0]
	if tb.Table == nil || len(tb.Table.Display) != 2 {
		t.Fatalf("regions: %s", summary(doc.Parts[0].Blocks))
	}
	for _, d := range tb.Table.Display {
		if d.Display != gofastcore.DisplayHidden || !(d.Row == 1 && d.Col == 0 && d.ColSpan == 2 || d.Col == 1 && d.Row == 0 && d.RowSpan == 2) {
			t.Errorf("region %+v", d)
		}
	}
	if off := doc.Parts[1]; off.Name != "Off" || off.Display != gofastcore.DisplayHidden || off.Blocks[0].Display != gofastcore.DisplayHidden {
		t.Errorf("sheet %+v", off)
	}
	noRemovedWarnings(t, doc)
}

// R6: tag characters, a bidirectional override and a run of zero-width
// characters survive extraction unchanged, in .odt and in .ods, with no
// reason.
func TestCharactersKept(t *testing.T) {
	tags := string([]rune{0xE0001, 0xE0068, 0xE0069, 0xE007F})
	bidi := string(rune(0x202E)) + "txet" + string(rune(0x202C))
	zw := strings.Repeat(string(rune(0x200B))+string(rune(0x200C))+string(rune(0x200D))+string(rune(0x2060)), 3)
	vs := "a" + string(rune(0xFE0F))
	s := "start" + tags + " " + bidi + " " + zw + " " + vs + " end"

	doc := extract(t, textDoc(t, "", `<text:p>`+s+`</text:p>`), nil)
	if b := body(t, doc); len(b) != 1 || b[0].Text != s {
		t.Errorf("odt: %s", summary(b))
	}
	noDisplay(t, doc)

	sheet := `<office:spreadsheet><table:table table:name="S"><table:table-row>` + cell(s) + `</table:table-row></table:table></office:spreadsheet>`
	doc = extract(t, bodyDoc(t, sheetMIME, "", sheet), &gofastcore.Options{Cells: true})
	b := doc.Parts[0].Blocks
	if len(b) != 1 || b[0].Table == nil || b[0].Table.Rows[0][0] != s {
		t.Errorf("ods: %s", summary(b))
	}
	noDisplay(t, doc)
}
