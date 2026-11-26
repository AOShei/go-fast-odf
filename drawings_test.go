package gofastodf

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/AOShei/go-fast-core"
)

const drawMIME = "application/vnd.oasis.opendocument.graphics"

// A drawing is read as one part of kind page per draw:page, its shapes in
// reading order, with the text of its master page and what its layers
// declare.
func TestDrawing(t *testing.T) {
	styles := stylesXML(``,
		`<style:page-layout style:name="PM1"><style:page-layout-properties fo:page-width="21cm" fo:page-height="29.7cm"/></style:page-layout>`,
		`<draw:layer-set><draw:layer draw:name="layout"/><draw:layer draw:name="Notes" draw:display="none"/><draw:layer draw:name="Print" draw:display="printer"/></draw:layer-set>`+
			`<style:master-page style:name="Default" style:page-layout-name="PM1">`+
			`<draw:frame svg:x="1cm" svg:y="1cm"><draw:text-box><text:p>Company</text:p></draw:text-box></draw:frame>`+
			`<draw:custom-shape svg:x="1cm" svg:y="27cm"><text:p>Drawing 12</text:p></draw:custom-shape>`+
			`</style:master-page>`)
	body := `<office:drawing>` +
		`<draw:page draw:name="Plan" draw:master-page-name="Default">` +
		`<draw:custom-shape svg:x="10cm" svg:y="12cm"><text:p>Pump</text:p></draw:custom-shape>` +
		`<draw:rect svg:x="2cm" svg:y="12cm"><text:p>Tank</text:p></draw:rect>` +
		`<draw:frame svg:x="2cm" svg:y="3cm"><draw:text-box><text:p>Water system</text:p></draw:text-box></draw:frame>` +
		`<draw:connector svg:x1="4cm" svg:y1="12cm" svg:x2="10cm" svg:y2="12cm"><text:p>flow</text:p></draw:connector>` +
		`<draw:g><draw:ellipse svg:x="2cm" svg:y="16cm"><text:p>In a group</text:p></draw:ellipse></draw:g>` +
		`<draw:custom-shape draw:layer="Notes" svg:x="2cm" svg:y="20cm"><text:p>check later</text:p></draw:custom-shape>` +
		`<draw:custom-shape draw:layer="Print" svg:x="2cm" svg:y="22cm"><text:p>printed only</text:p></draw:custom-shape>` +
		`<draw:custom-shape svg:x="2cm" svg:y="25cm"><text:p>Last</text:p></draw:custom-shape>` +
		`</draw:page>` +
		`<draw:page draw:name="page2" draw:master-page-name="Default"/>` +
		`</office:drawing>`
	doc := extract(t, bodyDoc(t, drawMIME, "", body, member{name: "styles.xml", data: styles}), nil)
	if doc.Source.Format != "odg" || doc.Source.MIME != drawMIME || len(doc.Parts) != 2 {
		t.Fatalf("format %s %s, %d parts", doc.Source.Format, doc.Source.MIME, len(doc.Parts))
	}
	p1 := doc.Parts[0]
	if p1.Kind != gofastcore.PartPage || p1.Name != "Plan" || p1.Number != 1 {
		t.Errorf("page %q %v %d", p1.Name, p1.Kind, p1.Number)
	}
	// Shapes from the top down and, at one height, from the left; a
	// connector stands at its start point (it follows the title, which
	// is higher, and comes before the pump, to its right); a group's
	// shapes are read in it, the group placed where its first shape
	// stands (16 cm, not at the layered shape before it in the file). The master's shapes come after the page's own, as header and
	// footer by where they stand on its A4 page.
	check(t, p1.Blocks, `
paragraph Water system
paragraph Tank
paragraph flow
paragraph Pump
paragraph In a group
paragraph{hidden} check later
paragraph printed only
paragraph Last
header Company
footer Drawing 12`)
	// A layer that is not displayed hides its shapes; one shown only when
	// printing is shown.
	if p1.Blocks[5].Display != gofastcore.DisplayHidden || p1.Blocks[6].Display != 0 {
		t.Errorf("layers %v %v", p1.Blocks[5].Display, p1.Blocks[6].Display)
	}
	// A page with nothing of its own still shows its master's text.
	p2 := doc.Parts[1]
	if p2.Kind != gofastcore.PartPage || p2.Name != "page2" {
		t.Errorf("page 2 %q %v", p2.Name, p2.Kind)
	}
	check(t, p2.Blocks, `
header Company
footer Drawing 12`)

	// One page asked for.
	one := extract(t, bodyDoc(t, drawMIME, "", body, member{name: "styles.xml", data: styles}), &gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 2}})
	if len(one.Parts) != 1 || one.Parts[0].Number != 2 || one.Parts[0].Kind != gofastcore.PartPage {
		t.Errorf("page 2 alone: %+v", one.Parts)
	}
}

// Templates, flat files, a drawing whose media type is not said, and an
// empty drawing.
func TestDrawingVariants(t *testing.T) {
	page := `<office:drawing><draw:page draw:name="page1"><draw:custom-shape><text:p>Box</text:p></draw:custom-shape></draw:page></office:drawing>`
	cases := []struct {
		name, mime, wantMIME string
	}{
		{"drawing", drawMIME, drawMIME},
		{"template", "application/vnd.oasis.opendocument.graphics-template", "application/vnd.oasis.opendocument.graphics-template"},
		// A media type no standard names (as an old producer wrote): the
		// body says what the document is.
		{"unnamed", "application/vnd.oasis.opendocument.drawing", drawMIME},
		{"none", "", drawMIME},
	}
	for _, c := range cases {
		doc := extract(t, bodyDoc(t, c.mime, "", page), nil)
		if doc.Source.Format != "odg" || doc.Source.MIME != c.wantMIME || doc.Parts[0].Kind != gofastcore.PartPage {
			t.Errorf("%s: %s %s %v", c.name, doc.Source.Format, doc.Source.MIME, doc.Parts[0].Kind)
		}
		check(t, doc.Parts[0].Blocks, `paragraph Box`)
	}
	flat := []byte(`<?xml version="1.0" encoding="UTF-8"?><office:document` + namespaces + ` office:version="1.3" office:mimetype="` + drawMIME + `"><office:body>` + page + `</office:body></office:document>`)
	doc := extract(t, flat, nil)
	if doc.Source.Format != "fodg" || doc.Source.MIME != "application/vnd.oasis.opendocument.graphics-flat-xml" || doc.Parts[0].Kind != gofastcore.PartPage {
		t.Errorf("fodg: %+v %v", doc.Source, doc.Parts[0].Kind)
	}
	// No page at all: one empty page, said to be empty.
	empty := extract(t, bodyDoc(t, drawMIME, "", `<office:drawing/>`), nil)
	if len(empty.Parts) != 1 || empty.Parts[0].Kind != gofastcore.PartPage || !empty.Parts[0].Has(gofastcore.WarnPartNoText) {
		t.Errorf("empty drawing %+v", empty.Parts)
	}
	// No body: one part of the drawing's kind.
	bare := extract(t, pkg(t, drawMIME, member{name: "content.xml", data: "<x/>"}), nil)
	if bare.Source.Format != "odg" || bare.Parts[0].Kind != gofastcore.PartPage {
		t.Errorf("no body %s %v", bare.Source.Format, bare.Parts[0].Kind)
	}
	// A package that says it is a drawing and holds a presentation.
	if err := extractErr(bodyDoc(t, drawMIME, "", `<office:presentation><draw:page/></office:presentation>`), nil); err == nil {
		t.Error("a drawing holding a presentation was read")
	}
}

// A drawing embedded in a text document is handed to the caller's
// extractor, which reads it; one held whole in a flat file is said to be
// left unread.
func TestEmbeddedDrawing(t *testing.T) {
	inner := `<office:document-content` + namespaces + `><office:body><office:drawing><draw:page draw:name="page1">` +
		`<draw:custom-shape><text:p>Inside</text:p></draw:custom-shape></draw:page></office:drawing></office:body></office:document-content>`
	data := textDoc(t, "", `<text:p>Before</text:p><text:p><draw:frame><draw:object xlink:href="./Object 1"/></draw:frame></text:p>`,
		member{name: "Object 1/content.xml", data: inner})
	var mimes []string
	opts := &gofastcore.Options{Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		mimes = append(mimes, mime)
		return Extract(ctx, r, size, o)
	}}
	doc := extract(t, data, opts)
	if len(doc.Children) != 1 || doc.Children[0].Source.Format != "odg" {
		t.Fatalf("children %d %v", len(doc.Children), mimes)
	}
	if mimes[0] != drawMIME {
		t.Errorf("child media type %q", mimes[0])
	}
	check(t, doc.Children[0].Parts[0].Blocks, `paragraph Inside`)

	flat := []byte(`<office:document` + namespaces + ` office:mimetype="application/vnd.oasis.opendocument.text"><office:body><office:text>` +
		`<text:p>Before</text:p><text:p><draw:frame><draw:object><office:document><office:body><office:drawing><draw:page><draw:custom-shape><text:p>Held</text:p></draw:custom-shape></draw:page></office:drawing></office:body></office:document></draw:object></draw:frame></text:p>` +
		`</office:text></office:body></office:document>`)
	fdoc, err := Extract(context.Background(), bytes.NewReader(flat), int64(len(flat)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(fdoc, WarnUnreadContent) {
		t.Errorf("a drawing held in a flat file is not said to be unread: %+v", fdoc.AllWarnings())
	}
}
