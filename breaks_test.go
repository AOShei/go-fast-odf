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
