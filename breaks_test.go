package gofastodf

import (
	"testing"

	"github.com/AOShei/go-fast-core"
)

// TestThematicBreaks: a break is emitted only where the file declares one,
// an empty paragraph in LibreOffice's "Horizontal Line" style (or one
// inheriting from it). An empty paragraph with a bottom border is a border.
func TestThematicBreaks(t *testing.T) {
	common := `<style:style style:name="Horizontal_20_Line" style:display-name="Horizontal Line" style:family="paragraph"/>` +
		`<style:style style:name="HRChild" style:family="paragraph" style:parent-style-name="Horizontal_20_Line"/>` +
		`<style:style style:name="Rule" style:family="paragraph"><style:paragraph-properties fo:border-bottom="0.06pt solid #000000"/></style:style>` +
		`<style:style style:name="Under" style:family="paragraph"><style:paragraph-properties fo:border="none" fo:border-bottom="1pt solid #ff0000"/></style:style>`
	content := `<text:p>One</text:p><text:p text:style-name="Rule"/><text:p>Two</text:p>` +
		`<text:p text:style-name="Rule"/><text:p text:style-name="Rule"/><text:p>Three</text:p>` +
		`<text:p text:style-name="Horizontal_20_Line"/><text:p>Four</text:p>` +
		`<text:p text:style-name="HRChild"/>` +
		`<text:p text:style-name="Horizontal_20_Line">ruled text</text:p>` + // has text
		`<text:section text:name="S"><text:p text:style-name="Under"/></text:section>` +
		`<text:p>* * *</text:p>`
	data := textDoc(t, "", content, member{name: "styles.xml", data: stylesXML(common, "", "")})
	doc := extract(t, data, nil)
	b := body(t, doc)
	check(t, b, `
paragraph One
paragraph Two
paragraph Three
thematic_break
paragraph Four
thematic_break
paragraph ruled text
paragraph * * *`)
	for _, x := range b {
		if x.Kind == gofastcore.KindThematicBreak && x.Origin != gofastcore.OriginNative {
			t.Errorf("break origin %v, want native", x.Origin)
		}
	}
	if md := doc.Markdown(); !contains(md, "Three\n\n---\n\nFour") {
		t.Errorf("markdown %q", md)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestThematicBreaksOutsideBody: a "Horizontal Line" paragraph is a break
// wherever paragraphs are read: list items, frames, footnotes, headers.
// In a table cell, whose paragraphs become the cell's text, it is not.
func TestThematicBreaksOutsideBody(t *testing.T) {
	common := `<style:style style:name="Horizontal_20_Line" style:display-name="Horizontal Line" style:family="paragraph"/>`
	hr := `<text:p text:style-name="Horizontal_20_Line"/>`
	master := `<style:master-page style:name="Standard"><style:header><text:p>Head</text:p>` + hr +
		`<text:p>Head two</text:p></style:header></style:master-page>`
	content := `<text:list><text:list-item><text:p>Item</text:p>` + hr + `</text:list-item></text:list>` +
		`<text:p>Frame <draw:frame svg:width="2cm" svg:height="1cm"><draw:text-box><text:p>In box</text:p>` + hr +
		`</draw:text-box></draw:frame></text:p>` +
		`<text:p>Noted<text:note text:note-class="footnote"><text:note-citation>1</text:note-citation>` +
		`<text:note-body><text:p>Note</text:p>` + hr + `</text:note-body></text:note></text:p>` +
		`<table:table><table:table-row><table:table-cell><text:p>Cell</text:p>` + hr +
		`</table:table-cell></table:table-row></table:table>` +
		`<text:p>End</text:p>`
	data := textDoc(t, "", content, member{name: "styles.xml", data: stylesXML(common, "", master)})
	doc := extract(t, data, nil)
	check(t, body(t, doc), `
header Head
thematic_break
header Head two
list_item1[•] Item
thematic_break
paragraph Frame
paragraph In box
thematic_break
paragraph Noted
footnote[1] Note
thematic_break
table |Cell
paragraph End`)
}

// A "Horizontal Line" paragraph inside a comment or a tracked deletion is
// not a break in the document: those notes keep only their text.
func TestNoBreakFromCommentOrDeletion(t *testing.T) {
	common := `<style:style style:name="Horizontal_20_Line" style:display-name="Horizontal Line" style:family="paragraph"/>`
	hr := `<text:p text:style-name="Horizontal_20_Line"/>`
	content := `<text:tracked-changes><text:changed-region text:id="c1"><text:deletion>` +
		`<office:change-info><dc:creator>A</dc:creator></office:change-info>` +
		`<text:p>Gone</text:p>` + hr + `</text:deletion></text:changed-region></text:tracked-changes>` +
		`<text:p>Kept<text:change text:change-id="c1"/></text:p>` +
		`<text:p>Commented<office:annotation><dc:creator>B</dc:creator><text:p>Remark</text:p>` + hr +
		`</office:annotation></text:p>` +
		`<text:p>End</text:p>`
	data := textDoc(t, "", content, member{name: "styles.xml", data: stylesXML(common, "", "")})
	doc := extract(t, data, nil)
	for _, b := range body(t, doc) {
		if b.Kind == gofastcore.KindThematicBreak {
			t.Fatalf("a break came from a comment or a deletion: %+v", body(t, doc))
		}
	}
}
