package gofastodf

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Files written around 2005 open their manifest and their formulas with a
// document type declaration that names an external DTD and declares
// nothing. It is skipped: nothing is said damaged and the formula is read.
// One that declares entities is refused.
func TestDocumentTypeDeclarations(t *testing.T) {
	build := func(contentXML string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		add := func(name, data string) {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			w.Write([]byte(data))
		}
		add("mimetype", textMIME)
		add("content.xml", contentXML)
		add("Object 1/content.xml", `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
			`<!DOCTYPE math:math PUBLIC "-//OpenOffice.org//DTD Modified W3C MathML 1.01//EN" "math.dtd">`+"\n"+
			`<math:math xmlns:math="http://www.w3.org/1998/Math/MathML">`+mathML[len(`<math:math>`):])
		add("META-INF/manifest.xml", `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
			`<!DOCTYPE manifest:manifest PUBLIC "-//OpenOffice.org//DTD Manifest 1.0//EN" "Manifest.dtd">`+"\n"+
			`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0">`+
			`<manifest:file-entry manifest:media-type="`+textMIME+`" manifest:full-path="/"/>`+
			`<manifest:file-entry manifest:media-type="text/xml" manifest:full-path="content.xml"/>`+
			`<manifest:file-entry manifest:media-type="application/vnd.oasis.opendocument.formula" manifest:full-path="Object 1/"/>`+
			`<manifest:file-entry manifest:media-type="text/xml" manifest:full-path="Object 1/content.xml"/>`+
			`</manifest:manifest>`)
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	paras := `<text:p>Before</text:p><text:p><draw:frame><draw:object xlink:href="./Object 1"/></draw:frame></text:p>`
	doc := extract(t, build(content("", paras)), nil)
	check(t, body(t, doc), `
paragraph Before
formula a^2 + x/(y - 1) = sqrt(z)`)
	for _, w := range doc.AllWarnings() {
		t.Errorf("warning %+v", w)
	}

	// An internal subset is where entities are declared: refused.
	withSubset := strings.Replace(content("", paras), "<office:document-content",
		`<!DOCTYPE office:document-content [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;">]><office:document-content`, 1)
	data := build(withSubset)
	doc, err := Extract(context.Background(), bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summary(body(t, doc)), "lol") {
		t.Errorf("an entity declared in an internal subset was expanded: %s", summary(body(t, doc)))
	}
	if !hasWarning(doc, gofastcore.WarnPartFailed) {
		t.Errorf("content with an internal subset is not said failed")
	}
}
