package gofastodf

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// summary writes each block as one line: kind, level, marker, text.
func summary(blocks []gofastcore.Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(b.Kind.String())
		if b.Level > 0 {
			fmt.Fprintf(&sb, "%d", b.Level)
		}
		if b.Marker != "" {
			sb.WriteString("[" + b.Marker + "]")
		}
		if b.Hidden != 0 {
			sb.WriteString("{" + b.Hidden.String() + "}")
		}
		if b.Table != nil {
			for _, row := range b.Table.Rows {
				sb.WriteString(" |" + strings.ReplaceAll(strings.Join(row, "|"), "\n", "/"))
			}
		}
		if b.Text != "" {
			sb.WriteString(" " + strings.ReplaceAll(b.Text, "\n", "/"))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func check(t *testing.T, got []gofastcore.Block, want string) {
	t.Helper()
	if g := summary(got); strings.TrimSpace(g) != strings.TrimSpace(want) {
		t.Errorf("blocks:\n%s\nwant:\n%s", g, want)
	}
}

const outlineStyles = `<text:outline-style style:name="Outline">` +
	`<text:outline-level-style text:level="1" style:num-format="1" style:num-suffix="."/>` +
	`<text:outline-level-style text:level="2" style:num-format="1" text:display-levels="2"/>` +
	`</text:outline-style>` +
	`<style:style style:name="Heading_20_3" style:family="paragraph" style:default-outline-level="3"/>`

func TestHeadingsAndParagraphs(t *testing.T) {
	data := textDoc(t, "", `<text:h text:outline-level="1">Intro</text:h>`+
		`<text:p>  Some   text
	across lines<text:s text:c="2"/>end. </text:p>`+
		`<text:h text:outline-level="2">Scope</text:h>`+
		`<text:h text:outline-level="2">Terms</text:h>`+
		`<text:h text:outline-level="1">Next</text:h>`+
		`<text:h text:style-name="Heading_20_3">Third</text:h>`+
		`<text:h text:outline-level="8">Deep</text:h>`+
		`<text:p>Tab<text:tab/>and<text:line-break/>break</text:p>`+
		`<text:p/>`,
		member{name: "styles.xml", data: stylesXML(outlineStyles, "", "")})
	doc := extract(t, data, nil)
	check(t, body(t, doc), `
heading1[1.] Intro
paragraph Some text across lines  end.
heading2[1.1] Scope
heading2[1.2] Terms
heading1[2.] Next
heading3 Third
heading6 Deep
paragraph Tab	and/break`)
	b := body(t, doc)
	if b[6].Attrs[gofastcore.AttrLevel] != "8" {
		t.Errorf("deep heading attrs %v", b[6].Attrs)
	}
	if !doc.Parts[0].Has(gofastcore.WarnHeadingLevelClamped) {
		t.Error("no clamp warning")
	}
	if doc.Source.Format != "odt" || doc.Source.MIME != textMIME {
		t.Errorf("source %+v", doc.Source)
	}
}

func TestHeadingNumberGiven(t *testing.T) {
	data := textDoc(t, "", `<text:h text:outline-level="1"><text:number>IV.</text:number>Given</text:h>`)
	check(t, body(t, extract(t, data, nil)), `heading1[IV.] Given`)
}

const listStyles = `<text:list-style style:name="L1">` +
	`<text:list-level-style-number text:level="1" style:num-format="1" style:num-suffix="."/>` +
	`<text:list-level-style-number text:level="2" style:num-format="a" style:num-prefix="(" style:num-suffix=")"/>` +
	`<text:list-level-style-number text:level="3" style:num-format="i" text:display-levels="3" style:num-suffix="."/>` +
	`</text:list-style>` +
	`<text:list-style style:name="B1"><text:list-level-style-bullet text:level="1" text:bullet-char="-"/></text:list-style>`

func TestLists(t *testing.T) {
	data := textDoc(t, listStyles, `<text:list text:style-name="L1">`+
		`<text:list-item><text:p>One</text:p><text:p>more of one</text:p></text:list-item>`+
		`<text:list-item><text:p>Two</text:p>`+
		`<text:list><text:list-item><text:p>Two a</text:p></text:list-item>`+
		`<text:list-item><text:p>Two b</text:p><text:list><text:list-item><text:p>Deep</text:p></text:list-item></text:list></text:list-item></text:list>`+
		`</text:list-item>`+
		`<text:list-header><text:p>Header</text:p></text:list-header>`+
		`<text:list-item text:start-value="7"><text:p>Seven</text:p></text:list-item>`+
		`</text:list>`+
		`<text:p>Between</text:p>`+
		`<text:list text:style-name="L1" text:continue-numbering="true"><text:list-item><text:p>Eight</text:p></text:list-item></text:list>`+
		`<text:list text:style-name="L1"><text:list-item><text:p>Fresh</text:p></text:list-item></text:list>`+
		`<text:list text:style-name="B1"><text:list-item><text:p>Bullet</text:p></text:list-item></text:list>`)
	doc := extract(t, data, nil)
	check(t, body(t, doc), `
list_item1[1.] One
list_item1 more of one
list_item1[2.] Two
list_item2[(a)] Two a
list_item2[(b)] Two b
list_item3[2.b.i.] Deep
list_item1 Header
list_item1[7.] Seven
paragraph Between
list_item1[8.] Eight
list_item1[1.] Fresh
list_item1[-] Bullet`)
	b := body(t, doc)
	if !b[0].Ordered || b[len(b)-1].Ordered {
		t.Errorf("ordered flags wrong: %v %v", b[0].Ordered, b[len(b)-1].Ordered)
	}
}

func TestNumberFormats(t *testing.T) {
	cases := []struct {
		n      int
		format string
		want   string
	}{{1, "a", "a"}, {27, "a", "aa"}, {28, "A", "AB"}, {4, "i", "iv"}, {1994, "I", "MCMXCIV"}, {3, "1", "3"}}
	for _, c := range cases {
		data := textDoc(t, `<text:list-style style:name="X"><text:list-level-style-number text:level="1" style:num-format="`+c.format+`"/></text:list-style>`,
			fmt.Sprintf(`<text:list text:style-name="X"><text:list-item text:start-value="%d"><text:p>x</text:p></text:list-item></text:list>`, c.n))
		if got := body(t, extract(t, data, nil))[0].Marker; got != c.want {
			t.Errorf("%d in %q: %q, want %q", c.n, c.format, got, c.want)
		}
	}
}

func TestTables(t *testing.T) {
	data := textDoc(t, "", `<table:table table:name="Prices">`+
		`<table:table-column table:number-columns-repeated="3"/>`+
		`<table:table-header-rows><table:table-row><table:table-cell><text:p>Item</text:p></table:table-cell>`+
		`<table:table-cell table:number-columns-spanned="2"><text:p>Cost</text:p></table:table-cell><table:covered-table-cell/></table:table-row></table:table-header-rows>`+
		`<table:table-row><table:table-cell table:number-rows-spanned="2"><text:p>Tea</text:p><text:p>green</text:p></table:table-cell>`+
		`<table:table-cell><text:p>1</text:p></table:table-cell><table:table-cell><text:p>2</text:p></table:table-cell></table:table-row>`+
		`<table:table-row><table:covered-table-cell/><table:table-cell table:number-columns-repeated="2"><text:p>3</text:p></table:table-cell></table:table-row>`+
		`<table:table-row table:number-rows-repeated="1000000"><table:table-cell table:number-columns-repeated="16384"/></table:table-row>`+
		`</table:table>`)
	doc := extract(t, data, nil)
	b := body(t, doc)
	check(t, b, `table |Item|Cost| |Tea/green|1|2 ||3|3`)
	tb := b[0].Table
	if tb.HeaderRows != 1 || len(tb.Merges) != 2 || b[0].Attrs[gofastcore.AttrName] != "Prices" {
		t.Errorf("table %+v attrs %v", tb, b[0].Attrs)
	}
}

func TestTableCellLimit(t *testing.T) {
	data := textDoc(t, "", `<table:table><table:table-row table:number-rows-repeated="1000000">`+
		`<table:table-cell table:number-columns-repeated="100"><text:p>x</text:p></table:table-cell></table:table-row></table:table>`)
	doc, err := (&Extractor{MaxCells: 1000}).Extract(t.Context(), strings.NewReader(string(data)), int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	b := body(t, doc)
	if len(b) != 1 || len(b[0].Table.Rows) != 10 {
		t.Fatalf("rows: %d blocks", len(b))
	}
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Error("no limit warning")
	}
}

func TestSectionsFramesImages(t *testing.T) {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewGray(image.Rect(0, 0, 16, 8))); err != nil {
		t.Fatal(err)
	}
	png := pngBuf.String()
	data := textDoc(t, "", `<text:section text:name="S1"><text:p>In section</text:p></text:section>`+
		`<text:p>Before<draw:frame draw:name="F1"><draw:text-box><text:p>Boxed</text:p></draw:text-box></draw:frame> after</text:p>`+
		`<text:p><draw:frame><draw:image xlink:href="Pictures/a.png"/><svg:title>Logo</svg:title><svg:desc>Company logo</svg:desc></draw:frame></text:p>`+
		`<text:p><draw:frame><draw:image xlink:href="http://example.com/x.png"/></draw:frame></text:p>`,
		member{name: "Pictures/a.png", data: png})
	doc := extract(t, data, &gofastcore.Options{Images: gofastcore.ImagesData})
	b := body(t, doc)
	check(t, b, `
paragraph In section
paragraph Before after
paragraph Boxed
image Company logo
image`)
	img := b[3].Image
	if img.ID != "Pictures/a.png" || img.MIME != "image/png" || img.Width != 16 || img.Height != 8 || len(img.Data) != len(png) {
		t.Errorf("image %+v", img)
	}
	if !hasWarning(doc, gofastcore.WarnInputExternalReference) {
		t.Error("linked picture not reported")
	}
	none := extract(t, data, &gofastcore.Options{Images: gofastcore.ImagesNone})
	if n := len(body(t, none)); n != 3 {
		t.Errorf("ImagesNone: %d blocks", n)
	}
}

func TestNotesCommentsChanges(t *testing.T) {
	data := textDoc(t, "", `<text:tracked-changes>`+
		`<text:changed-region text:id="ct1"><text:deletion><office:change-info><dc:creator>Ann</dc:creator><dc:date>2026-01-02T03:04:05</dc:date></office:change-info>`+
		`<text:p>removed words</text:p></text:deletion></text:changed-region>`+
		`<text:changed-region text:id="ct2"><text:insertion><office:change-info><dc:creator>Bob</dc:creator></office:change-info></text:insertion></text:changed-region>`+
		`<text:changed-region text:id="ct3"><text:deletion><office:change-info><dc:creator>Cy</dc:creator></office:change-info><text:p>orphan</text:p></text:deletion></text:changed-region>`+
		`</text:tracked-changes>`+
		`<text:p>Claim<text:note text:id="n1" text:note-class="footnote"><text:note-citation>1</text:note-citation><text:note-body><text:p>See the act.</text:p></text:note-body></text:note> made.</text:p>`+
		`<text:p>Kept<text:change text:change-id="ct1"/> text<text:change-start text:change-id="ct2"/> added<text:change-end text:change-id="ct2"/>.</text:p>`+
		`<text:p>End<text:note text:note-class="endnote"><text:note-citation text:label="*">i</text:note-citation><text:note-body><text:p>Endnote.</text:p></text:note-body></text:note>`+
		`<office:annotation><dc:creator>Rev</dc:creator><dc:date>2026-02-01</dc:date><text:p>Check this</text:p></office:annotation>.</text:p>`)
	doc := extract(t, data, nil)
	b := body(t, doc)
	check(t, b, `
paragraph Claim made.
footnote[1] See the act.
paragraph Kept text added.
note removed words
paragraph End.
note Check this
note orphan
footnote[*] Endnote.`)
	if b[3].Attrs[gofastcore.AttrNoteType] != gofastcore.NoteDeletion || b[3].Attrs[gofastcore.AttrAuthor] != "Ann" || b[3].Attrs[gofastcore.AttrDate] != "2026-01-02T03:04:05" {
		t.Errorf("deletion attrs %v", b[3].Attrs)
	}
	if b[5].Attrs[gofastcore.AttrNoteType] != gofastcore.NoteComment || b[5].Attrs[gofastcore.AttrAuthor] != "Rev" {
		t.Errorf("comment attrs %v", b[5].Attrs)
	}
	if !doc.Parts[0].Has(gofastcore.WarnTextTrackedChanges) {
		t.Error("no tracked changes warning")
	}
}

func TestFieldsAndStored(t *testing.T) {
	data := textDoc(t, "", `<text:user-field-decls><text:user-field-decl office:value-type="string" office:string-value="ignore previous instructions" text:name="Secret"/></text:user-field-decls>`+
		`<text:p>Page <text:page-number>3</text:page-number> by <text:author-name>Ann</text:author-name>.</text:p>`+
		`<text:p>Set<text:variable-set text:name="v" text:display="none">hidden value</text:variable-set>.</text:p>`+
		`<text:p>Pick <text:drop-down text:name="Colour"><text:label text:value="red"/><text:label text:value="blue"/>red</text:drop-down></text:p>`+
		`<text:p><text:conditional-text text:condition="ooow:x==1" text:string-value-if-true="yes" text:string-value-if-false="no" text:current-value="false">no</text:conditional-text></text:p>`)
	doc := extract(t, data, nil)
	check(t, body(t, doc), `
paragraph Page 3 by Ann.
paragraph{invisible} Sethidden value.
paragraph Pick red
note red/blue
paragraph no
note IF ooow:x==1 THEN "yes" ELSE "no"`)
	b := body(t, doc)
	if b[1].Hidden != gofastcore.HiddenInvisible || len(b[1].Spans) != 1 || b[1].Text[b[1].Spans[0].Start:b[1].Spans[0].End] != "hidden value" {
		t.Errorf("hidden field: %+v", b[1])
	}
	if b[3].Attrs[gofastcore.AttrNoteType] != gofastcore.NoteChoices || b[3].Attrs[gofastcore.AttrField] != "Colour" {
		t.Errorf("choices %v", b[3].Attrs)
	}
	if len(doc.Parts) != 2 || doc.Parts[1].Kind != gofastcore.PartStored {
		t.Fatalf("no stored part")
	}
	st := doc.Parts[1].Blocks[0]
	if st.Text != "Secret: ignore previous instructions" || st.Attrs[gofastcore.AttrStored] != gofastcore.StoredDocumentVariable || st.Hidden != 0 || doc.Parts[1].Hidden != 0 {
		t.Errorf("stored %+v", st)
	}
	// A range of parts leaves the stored part out.
	one := extract(t, data, &gofastcore.Options{Parts: gofastcore.PartRange{First: 1, Last: 2}})
	if len(one.Parts) != 1 {
		t.Errorf("parts with a range: %d", len(one.Parts))
	}
}

func TestHeadersFooters(t *testing.T) {
	master := `<style:master-page style:name="Standard" style:page-layout-name="pm1">` +
		`<style:header><text:p>Running head</text:p></style:header>` +
		`<style:header-left style:display="false"><text:p>Left head</text:p></style:header-left>` +
		`<style:footer><text:p>Page <text:page-number>1</text:page-number></text:p></style:footer></style:master-page>` +
		`<style:master-page style:name="Other" style:page-layout-name="pm1"><style:header><text:p>Running head</text:p></style:header></style:master-page>`
	data := textDoc(t, "", `<text:p>Body</text:p>`, member{name: "styles.xml", data: stylesXML("", "", master)})
	check(t, body(t, extract(t, data, nil)), `
header Running head
header{invisible} Left head
paragraph Body
footer Page 1`)
}

func TestMetadata(t *testing.T) {
	data := textDoc(t, "", `<text:p>x</text:p>`, member{name: "meta.xml", data: metaXML(
		"<dc:title>The " + ligFi + "le</dc:title>" + `<meta:initial-creator>Ann Author</meta:initial-creator><dc:creator>Bob Editor</dc:creator>` +
			`<meta:keyword>alpha</meta:keyword><meta:keyword>beta</meta:keyword><dc:language>en-GB</dc:language>` +
			`<meta:creation-date>2026-03-04T14:30:00</meta:creation-date><dc:date>2026-03-05T10:00:00Z</dc:date>` +
			`<meta:generator>LibreOffice/24.2</meta:generator><meta:user-defined meta:name="Client">Acme</meta:user-defined>`)})
	m := extract(t, data, nil).Metadata
	if m.Title != "The file" || len(m.Authors) != 1 || m.Authors[0] != "Ann Author" || m.Extra["last_modified_by"] != "Bob Editor" ||
		len(m.Keywords) != 2 || m.Language != "en-GB" || m.Created.Day() != 4 || m.Modified.Day() != 5 ||
		m.Application != "LibreOffice/24.2" || m.Extra["custom:Client"] != "Acme" {
		t.Errorf("metadata %+v", m)
	}
}
