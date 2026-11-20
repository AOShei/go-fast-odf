package gofastodf

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

const (
	sheetMIME = "application/vnd.oasis.opendocument.spreadsheet"
	slideMIME = "application/vnd.oasis.opendocument.presentation"
)

// bodyDoc builds a package whose content holds the given automatic styles
// and office:body content.
func bodyDoc(t testing.TB, mime, auto, body string, more ...member) []byte {
	t.Helper()
	c := `<?xml version="1.0" encoding="UTF-8"?><office:document-content` + namespaces + ` office:version="1.3">` +
		`<office:automatic-styles>` + auto + `</office:automatic-styles><office:body>` + body + `</office:body></office:document-content>`
	return pkg(t, mime, append([]member{{name: "content.xml", data: c}}, more...)...)
}

func cell(text string, attrs ...string) string {
	return `<table:table-cell ` + strings.Join(attrs, " ") + `><text:p>` + text + `</text:p></table:table-cell>`
}

func TestSpreadsheet(t *testing.T) {
	auto := `<style:style style:name="ta-hidden" style:family="table"><style:table-properties table:display="false"/></style:style>` +
		`<style:style style:name="ce-white" style:family="table-cell"><style:text-properties fo:color="#ffffff"/></style:style>`
	body := `<office:spreadsheet>` +
		`<table:content-validations><table:content-validation table:name="v1" table:condition="of:cell-content-is-in-list(&quot;Yes&quot;;&quot;No&quot;)"/>` +
		`<table:content-validation table:name="v2" table:condition="of:cell-content-is-in-list([.$Lists.A1:A3])"/></table:content-validations>` +
		`<table:table table:name="Data">` +
		`<table:table-column table:number-columns-repeated="2"/><table:table-column table:visibility="collapse"/><table:table-column table:number-columns-repeated="16381"/>` +
		`<table:table-row>` + cell("Name") + cell("When") + cell("Secret") + `</table:table-row>` +
		`<table:table-row>` + cell("Ann") + `<table:table-cell office:value-type="date" office:date-value="2026-03-04"><text:p>04/03/26</text:p></table:table-cell>` +
		`<table:table-cell office:value-type="float" office:value="3" table:formula="of:=1+2"><text:p>3</text:p></table:table-cell></table:table-row>` +
		`<table:table-row table:visibility="collapse">` + cell("Bob") + `<table:table-cell office:value-type="time" office:time-value="PT14H30M00S"><text:p>2:30 PM</text:p></table:table-cell>` +
		cell("white", `table:style-name="ce-white"`) + `</table:table-row>` +
		`<table:table-row table:number-rows-repeated="3"><table:table-cell table:number-columns-repeated="16384"/></table:table-row>` +
		`<table:table-row><table:table-cell table:number-columns-repeated="4"/>` + cell("Total", `table:number-columns-spanned="2"`) + `<table:covered-table-cell/></table:table-row>` +
		`<table:table-row><table:table-cell table:number-columns-repeated="4"/>` + cell("Yes", `table:content-validation-name="v1"`) +
		`<table:table-cell table:content-validation-name="v2"><office:annotation><dc:creator>Rev</dc:creator><text:p>pick one</text:p></office:annotation></table:table-cell></table:table-row>` +
		`<table:table-row table:number-rows-repeated="1048000"><table:table-cell table:number-columns-repeated="16384"/></table:table-row>` +
		`</table:table>` +
		`<table:table table:name="Lists" table:style-name="ta-hidden"><table:table-row>` + cell("a") + `</table:table-row>` +
		`<table:named-expressions><table:named-range table:name="Local" table:cell-range-address="$Lists.$A$1"/></table:named-expressions></table:table>` +
		`<table:named-expressions><table:named-range table:name="People" table:cell-range-address="$Data.$A$1:.$A$3"/><table:named-expression table:name="Rate" table:expression="of:=0.2"/></table:named-expressions>` +
		`<table:database-ranges><table:database-range table:name="DB" table:target-range-address="Data.A1:Data.C3"/></table:database-ranges>` +
		`</office:spreadsheet>`
	doc := extract(t, bodyDoc(t, sheetMIME, auto, body), &gofastcore.Options{Cells: true})
	if doc.Source.Format != "ods" || len(doc.Parts) != 3 {
		t.Fatalf("format %s, %d parts", doc.Source.Format, len(doc.Parts))
	}
	data := doc.Parts[0]
	if data.Kind != gofastcore.PartSheet || data.Name != "Data" || data.Hidden != 0 {
		t.Errorf("sheet %+v", data)
	}
	check(t, data.Blocks, `
table{invisible+low_contrast} |Name|When|Secret |Ann|2026-03-04|3 |Bob|14:30:00|white
table |Total| |Yes|
note pick one
note Yes/No
note =.$Lists.A1:A3`)
	b := data.Blocks
	if b[0].Loc.Range != "A1:C3" || b[1].Loc.Range != "E7:F8" || len(b[1].Table.Merges) != 1 {
		t.Errorf("ranges %q %q merges %v", b[0].Loc.Range, b[1].Loc.Range, b[1].Table.Merges)
	}
	if b[0].Origin != gofastcore.OriginInferred {
		t.Error("region not inferred")
	}
	var formula, date bool
	for _, c := range b[0].Table.Cells {
		formula = formula || c.Formula == "of:=1+2"
		date = date || c.Type == gofastcore.CellDate
	}
	if !formula {
		t.Errorf("cells %+v", b[0].Table.Cells)
	}
	if !date {
		t.Errorf("no date type: %+v", b[0].Table.Cells)
	}
	if b[2].Loc.Range != "F8" || b[2].Attrs[gofastcore.AttrNoteType] != gofastcore.NoteComment || b[2].Attrs[gofastcore.AttrAuthor] != "Rev" {
		t.Errorf("comment %+v", b[2])
	}
	if b[3].Attrs[gofastcore.AttrNoteType] != gofastcore.NoteChoices || b[3].Loc.Range != "E8" || b[3].Attrs[gofastcore.AttrField] != "v1" {
		t.Errorf("choices %+v", b[3])
	}
	hid := map[string]bool{}
	for _, h := range b[0].Table.Hidden {
		hid[strings.Join([]string{string(rune('0' + h.Row)), string(rune('0' + h.Col)), string(rune('0' + h.RowSpan)), string(rune('0' + h.ColSpan))}, ",")] = true
	}
	if !hid["2,0,1,3"] || !hid["0,2,3,1"] || !hid["2,2,1,1"] {
		t.Errorf("hidden regions %+v", b[0].Table.Hidden)
	}
	lists := doc.Parts[1]
	if lists.Name != "Lists" || lists.Hidden != gofastcore.HiddenInvisible || !lists.Has(gofastcore.WarnTextHidden) {
		t.Errorf("hidden sheet %+v", lists)
	}
	stored := doc.Parts[2]
	if stored.Kind != gofastcore.PartStored || summary(stored.Blocks) != "paragraph Lists!Local: $Lists.$A$1\nparagraph People: $Data.$A$1:.$A$3\nparagraph Rate: of:=0.2\nparagraph DB: Data.A1:Data.C3\n" {
		t.Errorf("stored:\n%s", summary(stored.Blocks))
	}
	// A range of parts gives those sheets and no stored part.
	two := extract(t, bodyDoc(t, sheetMIME, auto, body), &gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 2}})
	if len(two.Parts) != 1 || two.Parts[0].Number != 2 {
		t.Errorf("range: %d parts", len(two.Parts))
	}
}

// LibreOffice's hidden copy of an externally linked sheet is routine and
// not flagged; another hidden sheet is.
func TestLinkCacheSheet(t *testing.T) {
	auto := `<style:style style:name="ta_extref" style:family="table"><style:table-properties table:display="false"/></style:style>`
	body := `<office:spreadsheet><table:table table:name="Main"><table:table-row>` + cell("=x") + `</table:table-row></table:table>` +
		`<table:table table:name="&apos;file:///data/source.ods&apos;#Data" table:print="false" table:style-name="ta_extref"><table:table-row>` + cell("cached") + `</table:table-row></table:table>` +
		`<table:table table:name="Secret" table:style-name="ta_extref"><table:table-row>` + cell("hidden") + `</table:table-row></table:table></office:spreadsheet>`
	doc := extract(t, bodyDoc(t, sheetMIME, auto, body), nil)
	if doc.Parts[1].Hidden != 0 || doc.Parts[1].Blocks[0].Hidden != 0 || doc.Parts[2].Hidden != gofastcore.HiddenInvisible {
		t.Errorf("link cache %v %v, hidden sheet %v", doc.Parts[1].Hidden, doc.Parts[1].Blocks[0].Hidden, doc.Parts[2].Hidden)
	}
}

// LibreOffice writes loext:opacity="0%" beside the automatic font colour;
// it means nothing there. With a definite colour it is transparency.
func TestOpacityWithAutomaticColour(t *testing.T) {
	common := `<style:default-style style:family="paragraph"><style:text-properties style:use-window-font-color="true" loext:opacity="0%" fo:font-size="12pt"/></style:default-style>` +
		`<style:style style:name="Red" style:family="text"><style:text-properties fo:color="#ff0000"/></style:style>` +
		`<style:style style:name="Clear" style:family="text"><style:text-properties fo:color="#ff0000" loext:opacity="0%"/></style:style>`
	data := textDoc(t, "", `<text:p>Plain <text:span text:style-name="Red">red</text:span> and <text:span text:style-name="Clear">clear</text:span></text:p>`,
		member{name: "styles.xml", data: stylesXML(common, "", "")})
	b := body(t, extract(t, data, nil))
	if got, h := hiddenOf(b[0]); got != "clear" || h != gofastcore.HiddenTransparent {
		t.Errorf("got %q %v", got, h)
	}
}

func TestSpreadsheetLimits(t *testing.T) {
	// One cell with text repeated over a million rows and many columns.
	body := `<office:spreadsheet><table:table table:name="S"><table:table-row table:number-rows-repeated="1048576">` +
		cell("x", `table:number-columns-repeated="1000"`) + `</table:table-row></table:table></office:spreadsheet>`
	data := bodyDoc(t, sheetMIME, "", body)
	doc, err := (&Extractor{MaxCells: 5000}).Extract(t.Context(), strings.NewReader(string(data)), int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("no limit warning: %+v", doc.Parts[0].Warnings)
	}
	doc = extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 1 << 20}})
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("text limit: no warning")
	}
	// Thinly spread cells: a diagonal.
	var sb strings.Builder
	sb.WriteString(`<office:spreadsheet><table:table table:name="D">`)
	for i := 0; i < 100; i++ {
		sb.WriteString(`<table:table-row><table:table-cell table:number-columns-repeated="` + itoa(i) + `"/>` + cell("d") + `</table:table-row>`)
	}
	sb.WriteString(`</table:table></office:spreadsheet>`)
	doc = extract(t, bodyDoc(t, sheetMIME, "", sb.String()), nil)
	if len(doc.Parts[0].Blocks) != 100 || !doc.Parts[0].Has(WarnRegionSplit) {
		t.Errorf("diagonal: %d blocks", len(doc.Parts[0].Blocks))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "1"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}

func TestPresentation(t *testing.T) {
	auto := `<style:style style:name="dp-hidden" style:family="drawing-page"><style:drawing-page-properties presentation:visibility="hidden"/></style:style>` +
		`<style:style style:name="T1" style:family="text"><style:text-properties fo:color="#ffffff"/></style:style>`
	body := `<office:presentation>` +
		`<draw:page draw:name="page1">` +
		`<draw:frame svg:x="2cm" svg:y="10cm"><draw:text-box><text:p>Lower left</text:p></draw:text-box></draw:frame>` +
		`<draw:frame svg:x="12cm" svg:y="5cm"><draw:text-box><text:p>Upper right</text:p></draw:text-box></draw:frame>` +
		`<draw:frame svg:x="2cm" svg:y="5cm"><draw:text-box><text:list><text:list-item><text:p>Point</text:p></text:list-item></text:list></draw:text-box></draw:frame>` +
		`<draw:frame presentation:class="title" svg:x="2cm" svg:y="1cm"><draw:text-box><text:p>The Title</text:p></draw:text-box></draw:frame>` +
		`<draw:custom-shape svg:x="2cm" svg:y="15cm"><text:p>Shape <text:span text:style-name="T1">ghost</text:span></text:p></draw:custom-shape>` +
		`<draw:frame presentation:class="footer" svg:x="2cm" svg:y="18cm"><draw:text-box><text:p>Footer text</text:p></draw:text-box></draw:frame>` +
		`<presentation:notes><draw:page-thumbnail/><draw:frame presentation:class="notes"><draw:text-box><text:p>Say hello.</text:p><text:p>Then go on.</text:p></draw:text-box></draw:frame></presentation:notes>` +
		`</draw:page>` +
		`<draw:page draw:name="Backup" draw:style-name="dp-hidden"><draw:frame><draw:text-box><text:p>Hidden slide</text:p></draw:text-box></draw:frame></draw:page>` +
		`</office:presentation>`
	doc := extract(t, bodyDoc(t, slideMIME, auto, body), nil)
	if doc.Source.Format != "odp" || len(doc.Parts) != 2 {
		t.Fatalf("format %s, %d parts", doc.Source.Format, len(doc.Parts))
	}
	s1 := doc.Parts[0]
	if s1.Kind != gofastcore.PartSlide || s1.Name != "The Title" {
		t.Errorf("slide %q %v", s1.Name, s1.Kind)
	}
	check(t, s1.Blocks, `
heading1 The Title
list_item1[•] Point
paragraph Upper right
paragraph Lower left
paragraph{low_contrast} Shape ghost
footer Footer text
note Say hello./Then go on.`)
	if s1.Blocks[6].Attrs[gofastcore.AttrNoteType] != gofastcore.NoteSpeaker {
		t.Errorf("notes %+v", s1.Blocks[6])
	}
	if got, h := hiddenOf(s1.Blocks[4]); got != "ghost" || h != gofastcore.HiddenLowContrast {
		t.Errorf("ghost %q %v", got, h)
	}
	s2 := doc.Parts[1]
	if s2.Hidden != gofastcore.HiddenInvisible || s2.Name != "Backup" || s2.Blocks[0].Hidden != gofastcore.HiddenInvisible {
		t.Errorf("hidden slide %+v", s2)
	}
}

func TestFlatFiles(t *testing.T) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewGray(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw.Bytes())
	// Written in lines, as producers do.
	var lines strings.Builder
	for len(b64) > 20 {
		lines.WriteString(b64[:20] + "\n")
		b64 = b64[20:]
	}
	lines.WriteString(b64)
	flat := func(mime, body string) []byte {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?><office:document` + namespaces + ` office:version="1.3" office:mimetype="` + mime + `">` +
			`<office:meta><dc:title>Flat</dc:title></office:meta>` +
			`<office:styles><style:style style:name="Hide" style:family="text"><style:text-properties text:display="none"/></style:style></office:styles>` +
			`<office:master-styles><style:master-page style:name="Standard"><style:header><text:p>Head</text:p></style:header></style:master-page></office:master-styles>` +
			`<office:body>` + body + `</office:body></office:document>`)
	}
	doc := extract(t, flat("application/vnd.oasis.opendocument.text", `<office:text><text:p>Body <text:span text:style-name="Hide">hid</text:span></text:p>`+
		`<office:annotation><dc:creator>Not an author</dc:creator><text:p>c</text:p></office:annotation>`+
		`<text:p><draw:frame><draw:image><office:binary-data>`+lines.String()+`</office:binary-data></draw:image><svg:desc>dot</svg:desc></draw:frame></text:p></office:text>`),
		&gofastcore.Options{Images: gofastcore.ImagesData})
	if doc.Source.Format != "fodt" || doc.Metadata.Title != "Flat" || len(doc.Metadata.Authors) != 0 {
		t.Errorf("source %+v meta %+v", doc.Source, doc.Metadata)
	}
	check(t, body(t, doc), `
header Head
paragraph{invisible} Body hid
note c
image dot`)
	img := body(t, doc)[3].Image
	if img.MIME != "image/png" || img.Width != 2 || img.Height != 3 || len(img.Data) == 0 || !strings.HasPrefix(img.ID, "inline-") {
		t.Errorf("image %+v", img)
	}
	ods := extract(t, flat("application/vnd.oasis.opendocument.spreadsheet", `<office:spreadsheet><table:table table:name="T"><table:table-row>`+cell("v")+`</table:table-row></table:table></office:spreadsheet>`), nil)
	if ods.Source.Format != "fods" || ods.Parts[0].Name != "T" {
		t.Errorf("fods %+v", ods.Source)
	}
	odp := extract(t, flat("", `<office:presentation><draw:page><draw:frame presentation:class="title"><draw:text-box><text:p>T</text:p></draw:text-box></draw:frame></draw:page></office:presentation>`), nil)
	if odp.Source.Format != "fodp" || odp.Parts[0].Name != "T" {
		t.Errorf("fodp %+v", odp.Source)
	}
}
