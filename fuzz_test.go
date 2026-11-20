package gofastodf

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"math/rand"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// The fuzz targets bound what one input may cost (decompressed bytes,
// text, cells) so that a run holds little memory, and check every
// document they get with Validate.
//
// A target's input is the inside of one stream's root element, not the
// whole stream: the target writes the root with its namespace
// declarations around it. That keeps inputs to a few hundred bytes. It
// matters because the fuzzing engine minimises every input that finds new
// coverage, at a cost that grows with the square of its length; with
// inputs of two kilobytes (the declarations alone are one) a worker spent
// its whole time minimising and ran a few inputs a minute.

var fuzzLimits = gofastcore.Limits{MaxDecodedBytes: 8 << 20, MaxTextBytes: 4 << 20}

func fuzzExtract(t *testing.T, data []byte, opts *gofastcore.Options) {
	if opts == nil {
		opts = &gofastcore.Options{}
	}
	opts.Limits = fuzzLimits
	e := &Extractor{MaxCells: 100_000}
	// An input that takes long is a failure the fuzzer keeps.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	doc, err := e.Extract(ctx, bytes.NewReader(data), int64(len(data)), opts)
	if ctx.Err() != nil {
		t.Fatalf("took more than 10s")
	}
	if err != nil {
		return
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// stream is a stream the fuzz targets write into a package.
type stream struct {
	name, root string
}

var (
	contentStream  = stream{"content.xml", "office:document-content"}
	stylesStream   = stream{"styles.xml", "office:document-styles"}
	metaStream     = stream{"meta.xml", "office:document-meta"}
	manifestStream = stream{"META-INF/manifest.xml", "manifest:manifest"}
)

// fuzzPackage puts a fragment, wrapped in its stream's root element, in a
// sound package of the given media type.
//
// The members are stored, not compressed: a compressor costs most of a
// megabyte to set up, and an input costs it each time.
func fuzzPackage(t testing.TB, mime string, st stream, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	create := func(name string) io.Writer {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		return w
	}
	w := create("mimetype")
	w.Write([]byte(mime))
	if st.name != "content.xml" {
		w = create("content.xml")
		w.Write([]byte(content("", `<text:p>body</text:p>`)))
	}
	w = create(st.name)
	w.Write([]byte("<" + st.root + namespaces + ">"))
	w.Write(data)
	w.Write([]byte("</" + st.root + ">"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Seeds: short fragments, each showing a few features.
var (
	textSeeds = []string{
		`<office:automatic-styles><style:style style:name="H" style:family="text"><style:text-properties text:display="none" fo:color="#fff"/></style:style></office:automatic-styles>` +
			`<office:body><office:text><text:p>a <text:span text:style-name="H">b</text:span></text:p><text:section text:display="none"><text:h>c</text:h></text:section></office:text></office:body>`,
		`<office:automatic-styles><text:list-style style:name="L"><text:list-level-style-number text:level="1" style:num-format="i"/></text:list-style></office:automatic-styles>` +
			`<office:body><office:text><text:list text:style-name="L"><text:list-item><text:p>a</text:p><text:list><text:list-item><text:p>b</text:p></text:list-item></text:list></text:list-item></text:list></office:text></office:body>`,
		`<office:body><office:text><table:table><table:table-row table:number-rows-repeated="5"><table:table-cell table:number-columns-spanned="2"><text:p>x</text:p></table:table-cell></table:table-row></table:table></office:text></office:body>`,
		`<office:body><office:text><text:tracked-changes><text:changed-region text:id="c"><text:deletion><text:p>g</text:p></text:deletion></text:changed-region></text:tracked-changes>` +
			`<text:p>a<text:change text:change-id="c"/><text:note><text:note-citation>1</text:note-citation><text:note-body><text:p>n</text:p></text:note-body></text:note><office:annotation><text:p>c</text:p></office:annotation></text:p></office:text></office:body>`,
		`<office:body><office:text><text:p><draw:frame><draw:text-box><text:p>b</text:p></draw:text-box><draw:image xlink:href="P/x.png"/><svg:desc>d</svg:desc></draw:frame>` +
			`<text:drop-down><text:label text:value="a"/>a</text:drop-down><text:s text:c="9"/></text:p><text:user-field-decls><text:user-field-decl text:name="a" office:string-value="b"/></text:user-field-decls></office:text></office:body>`,
	}
	objectSeeds = []string{
		`<office:body><office:text><office:forms><form:form><form:listbox form:id="a" form:name="n"><form:option form:label="x" form:current-selected="true"/></form:listbox><form:hidden form:id="h" form:value="v"/></form:form></office:forms>` +
			`<text:p><draw:control draw:control="a"/><draw:frame><draw:object><math:math><math:mfrac><math:mi>a</math:mi><math:msqrt><math:mn>2</math:mn></math:msqrt></math:mfrac><math:annotation encoding="StarMath 5.0">a over sqrt 2</math:annotation></math:math></draw:object></draw:frame>` +
			`<draw:frame><draw:object><chart:chart chart:class="chart:line"><chart:title><text:p>t</text:p></chart:title><table:table><table:table-row table:number-rows-repeated="2"><table:table-cell table:number-columns-repeated="2"><text:p>1</text:p></table:table-cell></table:table-row></table:table></chart:chart></draw:object></draw:frame></text:p>` +
			`<text:p text:style-name="Horizontal_20_Line"/></office:text></office:body>`,
	}
	stylesSeeds = []string{
		`<office:styles><text:outline-style><text:outline-level-style text:level="1" style:num-format="1"/></text:outline-style></office:styles>` +
			`<office:automatic-styles><style:page-layout style:name="p"><style:page-layout-properties fo:background-color="#000000"/></style:page-layout></office:automatic-styles>` +
			`<office:master-styles><style:master-page style:name="S" style:page-layout-name="p"><style:header><text:p>h</text:p></style:header></style:master-page></office:master-styles>`,
	}
	metaSeeds = []string{
		`<office:meta><dc:title>t</dc:title><meta:keyword>k</meta:keyword><meta:user-defined meta:name="n">v</meta:user-defined><meta:creation-date>2026-01-01T00:00:00</meta:creation-date></office:meta>`,
	}
	manifestSeeds = []string{
		`<manifest:file-entry manifest:full-path="/" manifest:media-type="` + textMIME + `"/>` +
			`<manifest:file-entry manifest:full-path="content.xml"><manifest:encryption-data><manifest:algorithm manifest:algorithm-name="x"/></manifest:encryption-data></manifest:file-entry>`,
	}
)

func fuzzStream(f *testing.F, mime string, st stream, seeds []string, opts func() *gofastcore.Options) {
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzExtract(t, fuzzPackage(t, mime, st, data), opts())
	})
}

func noOpts() *gofastcore.Options { return nil }

func richOpts() *gofastcore.Options {
	return &gofastcore.Options{Spans: true, Cells: true, Images: gofastcore.ImagesData}
}

// FuzzContent fuzzes the content of a text document: the body reader and
// automatic styles.
func FuzzContent(f *testing.F) {
	fuzzStream(f, textMIME, contentStream, append(append([]string(nil), textSeeds...), objectSeeds...), richOpts)
}

// FuzzStyles fuzzes styles.xml: styles, list styles, master pages.
func FuzzStyles(f *testing.F) { fuzzStream(f, textMIME, stylesStream, stylesSeeds, noOpts) }

// FuzzMeta fuzzes meta.xml.
func FuzzMeta(f *testing.F) { fuzzStream(f, textMIME, metaStream, metaSeeds, noOpts) }

// FuzzManifest fuzzes the manifest.
func FuzzManifest(f *testing.F) { fuzzStream(f, textMIME, manifestStream, manifestSeeds, noOpts) }

// smallPackage is a whole file of a few hundred bytes: the seed of the
// target that fuzzes the archive itself.
func smallPackage(t testing.TB) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("mimetype")
	w.Write([]byte(textMIME))
	w, _ = zw.Create("content.xml")
	w.Write([]byte(`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">` +
		`<office:body><office:text><text:h>T</text:h><text:p>a</text:p></office:text></office:body></office:document-content>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// FuzzExtract feeds whole files.
func FuzzExtract(f *testing.F) {
	f.Add(smallPackage(f))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzExtract(t, data, nil)
	})
}

var (
	sheetSeeds = []string{
		`<office:automatic-styles><style:style style:name="t" style:family="table"><style:table-properties table:display="false"/></style:style></office:automatic-styles><office:body><office:spreadsheet>` +
			`<table:content-validations><table:content-validation table:name="v" table:condition="of:cell-content-is-in-list(&quot;a&quot;;&quot;b&quot;)"/></table:content-validations>` +
			`<table:table table:name="S" table:style-name="t"><table:table-column table:visibility="collapse" table:number-columns-repeated="2"/>` +
			`<table:table-row table:number-rows-repeated="3"><table:table-cell table:number-columns-repeated="2" table:content-validation-name="v"><text:p>x</text:p></table:table-cell>` +
			`<table:table-cell office:value-type="time" office:time-value="PT1H2M3S" table:formula="of:=A1" table:number-rows-spanned="2" table:number-columns-spanned="2"><text:p>t</text:p><office:annotation><text:p>c</text:p></office:annotation></table:table-cell><table:covered-table-cell/></table:table-row>` +
			`</table:table><table:named-expressions><table:named-range table:name="n" table:cell-range-address="$S.$A$1"/></table:named-expressions></office:spreadsheet></office:body>`,
	}
	slideSeeds = []string{
		`<office:automatic-styles><style:style style:name="d" style:family="drawing-page"><style:drawing-page-properties presentation:visibility="hidden"/></style:style></office:automatic-styles>` +
			`<office:body><office:presentation><draw:page draw:name="p" draw:style-name="d"><draw:frame presentation:class="title" svg:x="1cm" svg:y="1cm"><draw:text-box><text:p>T</text:p></draw:text-box></draw:frame>` +
			`<draw:g><draw:custom-shape svg:y="3cm"><text:p>s</text:p></draw:custom-shape></draw:g><presentation:notes><draw:frame><draw:text-box><text:p>n</text:p></draw:text-box></draw:frame></presentation:notes></draw:page></office:presentation></office:body>`,
	}
	flatSeeds = []string{
		`<office:meta><dc:title>t</dc:title></office:meta><office:master-styles><style:master-page style:name="S"><style:footer><text:p>f</text:p></style:footer></style:master-page></office:master-styles>` +
			`<office:body><office:text><text:p><draw:frame><draw:image><office:binary-data>iVBORw0KGgo=</office:binary-data></draw:image></draw:frame>x</text:p></office:text></office:body>`,
	}
)

// fuzzFlat wraps a fragment in the root of a flat file.
func fuzzFlat(data []byte) []byte {
	return append(append([]byte(`<office:document`+namespaces+`>`), data...), "</office:document>"...)
}

// FuzzSheet fuzzes the content of a spreadsheet.
func FuzzSheet(f *testing.F) { fuzzStream(f, sheetMIME, contentStream, sheetSeeds, richOpts) }

// FuzzSlides fuzzes the content of a presentation.
func FuzzSlides(f *testing.F) { fuzzStream(f, slideMIME, contentStream, slideSeeds, richOpts) }

// FuzzFlat fuzzes a flat file.
func FuzzFlat(f *testing.F) {
	for _, s := range flatSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzExtract(t, fuzzFlat(data), richOpts())
	})
}

// fuzzCase is one stream and its seeds, for TestMutations.
type fuzzCase struct {
	mime  string
	st    stream
	seeds []string
}

var fuzzCases = []fuzzCase{
	{textMIME, contentStream, textSeeds},
	{textMIME, contentStream, objectSeeds},
	{textMIME, stylesStream, stylesSeeds},
	{textMIME, metaStream, metaSeeds},
	{textMIME, manifestStream, manifestSeeds},
	{sheetMIME, contentStream, sheetSeeds},
	{slideMIME, contentStream, slideSeeds},
}

// TestMutations runs a fixed number of seeded random mutations of every
// stream through the extractor: the fuzz targets' work in a short,
// repeatable form that runs with go test.
func TestMutations(t *testing.T) {
	n := 400
	if testing.Short() {
		n = 150
	}
	rng := rand.New(rand.NewSource(1))
	for _, c := range fuzzCases {
		for i := 0; i < n; i++ {
			b := mutate(rng, []byte(c.seeds[rng.Intn(len(c.seeds))]))
			fuzzExtract(t, fuzzPackage(t, c.mime, c.st, b), &gofastcore.Options{Spans: i%2 == 0, Cells: i%3 == 0, Images: gofastcore.ImagesData})
		}
	}
	whole := smallPackage(t)
	for i := 0; i < n; i++ {
		fuzzExtract(t, mutate(rng, append([]byte(nil), whole...)), nil)
		fuzzExtract(t, fuzzFlat(mutate(rng, []byte(flatSeeds[0]))), richOpts())
	}
}

// mutate changes bytes, duplicates a stretch, or cuts one out.
func mutate(rng *rand.Rand, b []byte) []byte {
	for k := 0; k < 1+rng.Intn(4); k++ {
		if len(b) < 2 {
			return b
		}
		i, j := rng.Intn(len(b)), rng.Intn(len(b))
		if i > j {
			i, j = j, i
		}
		switch rng.Intn(4) {
		case 0:
			b[i] = byte(rng.Intn(256))
		case 1:
			b = append(b[:j:j], append(append([]byte(nil), b[i:j]...), b[j:]...)...)
		case 2:
			b = append(b[:i:i], b[j:]...)
		default:
			b[i] = "0123456789\"<>/="[rng.Intn(15)]
		}
	}
	return b
}
