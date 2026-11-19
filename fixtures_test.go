package gofastodf

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// The fixtures are built here, in code: small OpenDocument packages that
// each show one thing.

const namespaces = ` xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"` +
	` xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"` +
	` xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"` +
	` xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"` +
	` xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"` +
	` xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"` +
	` xmlns:xlink="http://www.w3.org/1999/xlink"` +
	` xmlns:dc="http://purl.org/dc/elements/1.1/"` +
	` xmlns:meta="urn:oasis:names:tc:opendocument:xmlns:meta:1.0"` +
	` xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0"` +
	` xmlns:loext="urn:org:documentfoundation:names:experimental:office:xmlns:loext:1.0"` +
	` xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"`

const textMIME = "application/vnd.oasis.opendocument.text"

// member is a file in a package.
type member struct {
	name, data string
	// encrypted makes the manifest say the member is encrypted.
	encrypted bool
}

// content returns a content.xml with the given automatic styles and body.
func content(auto, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><office:document-content` + namespaces + ` office:version="1.3">` +
		`<office:automatic-styles>` + auto + `</office:automatic-styles>` +
		`<office:body><office:text>` + body + `</office:text></office:body></office:document-content>`
}

// stylesXML returns a styles.xml with the given common styles, automatic
// styles and master styles.
func stylesXML(common, auto, master string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><office:document-styles` + namespaces + ` office:version="1.3">` +
		`<office:styles>` + common + `</office:styles>` +
		`<office:automatic-styles>` + auto + `</office:automatic-styles>` +
		`<office:master-styles>` + master + `</office:master-styles></office:document-styles>`
}

func metaXML(inner string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><office:document-meta` + namespaces + ` office:version="1.3"><office:meta>` + inner + `</office:meta></office:document-meta>`
}

// pkg builds a package of the given media type: mimetype first and
// stored, a manifest listing the members, then the members.
func pkg(t testing.TB, mime string, members ...member) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(mime))
	var man strings.Builder
	man.WriteString(`<?xml version="1.0" encoding="UTF-8"?><manifest:manifest` + namespaces + ` manifest:version="1.3">`)
	man.WriteString(`<manifest:file-entry manifest:full-path="/" manifest:media-type="` + mime + `"/>`)
	for _, m := range members {
		mt := "text/xml"
		switch {
		case strings.HasSuffix(m.name, ".png"):
			mt = "image/png"
		case !strings.HasSuffix(m.name, ".xml"):
			mt = ""
		}
		man.WriteString(`<manifest:file-entry manifest:full-path="` + m.name + `" manifest:media-type="` + mt + `"`)
		if m.encrypted {
			man.WriteString(` manifest:size="100"><manifest:encryption-data manifest:checksum-type="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0#sha256-1k" manifest:checksum="AAAA">` +
				`<manifest:algorithm manifest:algorithm-name="http://www.w3.org/2001/04/xmlenc#aes256-cbc" manifest:initialisation-vector="AAAA"/>` +
				`<manifest:start-key-generation manifest:start-key-generation-name="http://www.w3.org/2000/09/xmldsig#sha256" manifest:key-size="32"/>` +
				`<manifest:key-derivation manifest:key-derivation-name="PBKDF2" manifest:key-size="32" manifest:iteration-count="100000" manifest:salt="AAAA"/>` +
				`</manifest:encryption-data></manifest:file-entry>`)
		} else {
			man.WriteString(`/>`)
		}
	}
	man.WriteString(`</manifest:manifest>`)
	all := append([]member{{name: "META-INF/manifest.xml", data: man.String()}}, members...)
	for _, m := range all {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(m.data))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// textDoc builds a text document from a body, with optional automatic
// styles and further members.
func textDoc(t testing.TB, auto, body string, more ...member) []byte {
	t.Helper()
	return pkg(t, textMIME, append([]member{{name: "content.xml", data: content(auto, body)}}, more...)...)
}

// extract extracts data and checks the result against the model's rules.
func extract(t testing.TB, data []byte, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return doc
}

// body returns the blocks of the body part.
func body(t testing.TB, doc *gofastcore.Document) []gofastcore.Block {
	t.Helper()
	if len(doc.Parts) == 0 || doc.Parts[0].Kind != gofastcore.PartBody {
		t.Fatalf("no body part: %+v", doc.Parts)
	}
	return doc.Parts[0].Blocks
}

func hasWarning(doc *gofastcore.Document, code string) bool {
	for _, w := range doc.AllWarnings() {
		if w.Code == code {
			return true
		}
	}
	return false
}
