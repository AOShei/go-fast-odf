package gofastodf

import (
	"testing"

	"github.com/AOShei/go-fast-core"
)

func TestThematicBreaks(t *testing.T) {
	common := `<style:style style:name="Horizontal_20_Line" style:display-name="Horizontal Line" style:family="paragraph"/>` +
		`<style:style style:name="Rule" style:family="paragraph"><style:paragraph-properties fo:border-bottom="0.06pt solid #000000"/></style:style>` +
		`<style:style style:name="RuleChild" style:family="paragraph" style:parent-style-name="Rule"/>` +
		`<style:style style:name="Boxed" style:family="paragraph"><style:paragraph-properties fo:border="0.06pt solid #000000"/></style:style>` +
		`<style:style style:name="Under" style:family="paragraph"><style:paragraph-properties fo:border="none" fo:border-bottom="1pt solid #ff0000"/></style:style>`
	content := `<text:p>One</text:p><text:p text:style-name="Rule"/><text:p>Two</text:p>` +
		`<text:p text:style-name="Horizontal_20_Line"/><text:p>Three</text:p>` +
		`<text:p text:style-name="RuleChild"/>` + // inherited, and next to the one below: a box
		`<text:p text:style-name="RuleChild">boxed text</text:p>` +
		`<text:p text:style-name="Boxed"/>` + // four sides: not a rule
		`<text:p text:style-name="Rule">ruled text</text:p>` + // has text
		`<text:p>* * *</text:p>` +
		`<text:section text:name="S"><text:p text:style-name="Under"/></text:section>` +
		`<table:table><table:table-row><table:table-cell><text:p text:style-name="Rule"/><text:p>cell</text:p></table:table-cell></table:table-row></table:table>` +
		`<text:p><draw:frame><draw:text-box><text:p text:style-name="Rule"/><text:p>boxed</text:p></draw:text-box></draw:frame></text:p>` +
		`<text:list><text:list-item><text:p text:style-name="Rule"/></text:list-item></text:list>`
	master := `<style:master-page style:name="Standard"><style:header><text:p text:style-name="Rule"/><text:p>Head</text:p></style:header></style:master-page>`
	data := textDoc(t, "", content, member{name: "styles.xml", data: stylesXML(common, "", master)})
	doc := extract(t, data, nil)
	b := body(t, doc)
	check(t, b, `
header Head
paragraph One
thematic_break
paragraph Two
thematic_break
paragraph Three
paragraph boxed text
paragraph ruled text
paragraph * * *
thematic_break
table |cell
paragraph boxed`)
	for _, x := range b {
		if x.Kind == gofastcore.KindThematicBreak && x.Origin != gofastcore.OriginInferred {
			t.Error("break not inferred")
		}
	}
	if md := doc.Markdown(); !contains(md, "One\n\n---\n\nTwo") {
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
