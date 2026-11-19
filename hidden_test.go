package gofastodf

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

const hidingStyles = `<style:style style:name="Hide" style:family="text"><style:text-properties text:display="none"/></style:style>` +
	`<style:style style:name="HidePara" style:family="paragraph"><style:text-properties text:display="none"/></style:style>` +
	`<style:style style:name="White" style:family="text"><style:text-properties fo:color="#ffffff"/></style:style>` +
	`<style:style style:name="Cream" style:family="text"><style:text-properties fo:color="#fffff0"/></style:style>` +
	`<style:style style:name="Tiny" style:family="text"><style:text-properties fo:font-size="1pt"/></style:style>` +
	`<style:style style:name="Shrunk" style:family="text"><style:text-properties fo:font-size="10%"/></style:style>` +
	`<style:style style:name="Narrow" style:family="text"><style:text-properties style:text-scale="10%"/></style:style>` +
	`<style:style style:name="Dark" style:family="paragraph"><style:paragraph-properties fo:background-color="#000000"/></style:style>` +
	`<style:style style:name="Marked" style:family="text"><style:text-properties fo:color="#ffffff" fo:background-color="#000000"/></style:style>` +
	`<style:style style:name="DarkCell" style:family="table-cell"><style:table-cell-properties fo:background-color="#000000"/></style:style>` +
	`<style:style style:name="Black" style:family="text"><style:text-properties fo:color="#000000"/></style:style>`

// hiddenOf returns the text of the hidden spans of a block, or the whole
// text when the reasons cover all of it.
func hiddenOf(b gofastcore.Block) (string, gofastcore.Hidden) {
	if len(b.Spans) == 0 {
		return b.Text, b.Hidden
	}
	var out string
	var h gofastcore.Hidden
	for _, sp := range b.Spans {
		if sp.Hidden != 0 {
			out += b.Text[sp.Start:sp.End]
			h |= sp.Hidden
		}
	}
	return out, h
}

func TestHiddenText(t *testing.T) {
	data := textDoc(t, hidingStyles,
		`<text:p>Visible <text:span text:style-name="Hide">secret</text:span> end.</text:p>`+
			`<text:p text:style-name="HidePara">All hidden</text:p>`+
			`<text:p>Gone<text:hidden-paragraph text:condition="ooow:true" text:is-hidden="true"/></text:p>`+
			`<text:section text:name="S" text:display="none"><text:p>In hidden section</text:p></text:section>`+
			`<text:p>See <text:span text:style-name="White">white words</text:span>.</text:p>`+
			`<text:p>See <text:span text:style-name="Cream">cream words</text:span>.</text:p>`+
			`<text:p text:style-name="Dark">On dark <text:span text:style-name="White">white is fine</text:span>.</text:p>`+
			`<text:p>Marked <text:span text:style-name="Marked">white on black</text:span>.</text:p>`+
			`<text:p>Small <text:span text:style-name="Tiny">tiny</text:span> and <text:span text:style-name="Shrunk">shrunk</text:span> and <text:span text:style-name="Narrow">narrow</text:span>.</text:p>`+
			"<text:p>Zero"+strings.Repeat(zwsp, 4)+"width</text:p>"+
			`<text:p>If <text:hidden-text text:condition="ooow:1" text:string-value="cond" text:is-hidden="true">cond</text:hidden-text> shown.</text:p>`+
			`<table:table><table:table-row><table:table-cell><text:p>open</text:p></table:table-cell>`+
			`<table:table-cell><text:p><text:span text:style-name="Hide">closed</text:span></text:p></table:table-cell></table:table-row>`+
			`<table:table-row table:visibility="collapse"><table:table-cell><text:p>collapsed row</text:p></table:table-cell></table:table-row></table:table>`+
			`<table:table><table:table-row><table:table-cell table:style-name="DarkCell"><text:p><text:span text:style-name="Black">black on black</text:span></text:p></table:table-cell></table:table-row></table:table>`)
	doc := extract(t, data, nil)
	b := body(t, doc)
	want := []struct {
		text   string
		hidden gofastcore.Hidden
	}{
		{"secret", gofastcore.HiddenInvisible},
		{"All hidden", gofastcore.HiddenInvisible},
		{"Gone", gofastcore.HiddenInvisible},
		{"In hidden section", gofastcore.HiddenInvisible},
		{"white words", gofastcore.HiddenLowContrast},
		{"cream words", gofastcore.HiddenLowContrast},
		{"", 0},
		{"", 0},
		{"tinyshrunknarrow", gofastcore.HiddenTiny},
		{"", gofastcore.HiddenChars},
		{"cond", gofastcore.HiddenInvisible},
	}
	if len(b) < len(want)+2 {
		t.Fatalf("%d blocks:\n%s", len(b), summary(b))
	}
	for i, w := range want {
		got, h := hiddenOf(b[i])
		if w.hidden == gofastcore.HiddenChars {
			if b[i].Hidden != gofastcore.HiddenChars {
				t.Errorf("block %d %q: hidden %v, want invisible_chars", i, b[i].Text, b[i].Hidden)
			}
			continue
		}
		if b[i].Hidden != w.hidden || (w.hidden != 0 && got != w.text) || h != w.hidden {
			t.Errorf("block %d %q: hidden %q (%v), block %v; want %q (%v)", i, b[i].Text, got, h, b[i].Hidden, w.text, w.hidden)
		}
	}
	tb := b[len(want)]
	if tb.Table == nil || len(tb.Table.Hidden) != 2 || tb.Hidden != gofastcore.HiddenInvisible {
		t.Errorf("table hidden: %+v %+v", tb.Hidden, tb.Table)
	}
	dark := b[len(want)+1]
	if dark.Table == nil || dark.Hidden != gofastcore.HiddenLowContrast {
		t.Errorf("black on a black cell: %v", dark.Hidden)
	}
	if !doc.Parts[0].Has(gofastcore.WarnTextHidden) {
		t.Error("no text.hidden warning")
	}
	if doc.HiddenChecked != gofastcore.HiddenInvisible|gofastcore.HiddenTiny|gofastcore.HiddenLowContrast|gofastcore.HiddenChars {
		t.Errorf("checked %v", doc.HiddenChecked)
	}
}

func TestHiddenInNotesAndCells(t *testing.T) {
	data := textDoc(t, hidingStyles, `<text:p>Claim<text:note text:note-class="footnote"><text:note-citation>1</text:note-citation><text:note-body>`+
		`<text:p>Seen <text:span text:style-name="Hide">unseen</text:span></text:p><text:p text:style-name="HidePara">all unseen</text:p><text:p>seen again</text:p></text:note-body></text:note></text:p>`)
	b := body(t, extract(t, data, nil))
	if len(b) != 2 || b[1].Kind != gofastcore.KindFootnote {
		t.Fatalf("%s", summary(b))
	}
	if got, h := hiddenOf(b[1]); got != "unseenall unseen" || h != gofastcore.HiddenInvisible {
		t.Errorf("footnote hidden %q %v: %+v", got, h, b[1])
	}
}

func TestPageBackground(t *testing.T) {
	styles := stylesXML(`<style:style style:name="Black" style:family="text"><style:text-properties fo:color="#000000"/></style:style>`,
		`<style:page-layout style:name="pm1"><style:page-layout-properties fo:background-color="#000000"/></style:page-layout>`,
		`<style:master-page style:name="Standard" style:page-layout-name="pm1"/>`)
	data := textDoc(t, "", `<text:p>Plain <text:span text:style-name="Black">black on a black page</text:span>.</text:p>`,
		member{name: "styles.xml", data: styles})
	b := body(t, extract(t, data, nil))
	if got, h := hiddenOf(b[0]); h != gofastcore.HiddenLowContrast || got != "black on a black page" {
		t.Errorf("got %q %v", got, h)
	}
}

func TestInheritedHiding(t *testing.T) {
	// Hidden through the parent of the paragraph's style, shown again by
	// a span's style.
	common := `<style:style style:name="Base" style:family="paragraph"><style:text-properties text:display="none"/></style:style>` +
		`<style:style style:name="Child" style:family="paragraph" style:parent-style-name="Base"/>` +
		`<style:style style:name="Loop1" style:family="paragraph" style:parent-style-name="Loop2"/>` +
		`<style:style style:name="Loop2" style:family="paragraph" style:parent-style-name="Loop1"/>` +
		`<style:style style:name="Show" style:family="text"><style:text-properties text:display="true"/></style:style>`
	data := textDoc(t, "", `<text:p text:style-name="Child">hidden <text:span text:style-name="Show">shown</text:span></text:p>`+
		`<text:p text:style-name="Loop1">cycle</text:p>`,
		member{name: "styles.xml", data: stylesXML(common, "", "")})
	b := body(t, extract(t, data, nil))
	if got, h := hiddenOf(b[0]); got != "hidden " || h != gofastcore.HiddenInvisible || b[0].Hidden != gofastcore.HiddenInvisible {
		t.Errorf("got %q %v", got, h)
	}
	if b[1].Hidden != 0 {
		t.Errorf("cycle: %v", b[1].Hidden)
	}
}
