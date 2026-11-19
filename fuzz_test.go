package gofastodf

import (
	"archive/zip"
	"bytes"
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// The fuzz targets bound what one input may cost (decompressed bytes,
// text, cells) so that a run holds little memory, and check every
// document they get with Validate.

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

// FuzzExtract feeds whole files.
func FuzzExtract(f *testing.F) {
	f.Add(textDoc(f, listStyles, `<text:h>T</text:h><text:list text:style-name="L1"><text:list-item><text:p>a</text:p></text:list-item></text:list>`))
	f.Add(textDoc(f, "", `<table:table><table:table-row><table:table-cell><text:p>x</text:p></table:table-cell></table:table-row></table:table>`))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzExtract(t, data, nil)
	})
}

// TestMutations runs a fixed number of seeded random mutations of every
// stream through the extractor: the fuzz targets' work in a short,
// repeatable form that runs with go test.
func TestMutations(t *testing.T) {
	n := 400
	if testing.Short() {
		n = 150
	}
	streams := []struct {
		name  string
		seeds []string
	}{
		{"content.xml", contentSeeds},
		{"styles.xml", []string{stylesSeed}},
		{"meta.xml", []string{metaSeed}},
		{"META-INF/manifest.xml", []string{manifestSeed}},
	}
	rng := rand.New(rand.NewSource(1))
	for _, st := range streams {
		for i := 0; i < n; i++ {
			b := mutate(rng, []byte(st.seeds[rng.Intn(len(st.seeds))]))
			fuzzExtract(t, fuzzPackage(t, st.name, b), &gofastcore.Options{Spans: i%2 == 0, Images: gofastcore.ImagesData})
		}
	}
	whole := textDoc(t, listStyles, `<text:p>whole file</text:p>`)
	for i := 0; i < n; i++ {
		fuzzExtract(t, mutate(rng, append([]byte(nil), whole...)), nil)
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

// fuzzPackage puts a stream in a sound package, so that the fuzzer works
// on the XML.
func fuzzPackage(t *testing.T, name string, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("mimetype")
	w.Write([]byte(textMIME))
	if name != "content.xml" {
		w, _ = zw.Create("content.xml")
		w.Write([]byte(content("", `<text:p>body</text:p>`)))
	}
	w, _ = zw.Create(name)
	w.Write(data)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var contentSeeds = []string{
	content(hidingStyles, `<text:p>Visible <text:span text:style-name="Hide">secret</text:span></text:p><text:section text:display="none"><text:p>x</text:p></text:section>`),
	content(listStyles, `<text:list text:style-name="L1"><text:list-item><text:p>One</text:p><text:list><text:list-item><text:h>deep</text:h></text:list-item></text:list></text:list-item></text:list>`),
	content("", `<table:table><table:table-row table:number-rows-repeated="5"><table:table-cell table:number-columns-spanned="2" table:number-rows-spanned="3"><text:p>x</text:p></table:table-cell></table:table-row></table:table>`),
	content("", `<text:tracked-changes><text:changed-region text:id="c"><text:deletion><text:p>gone</text:p></text:deletion></text:changed-region></text:tracked-changes><text:p>a<text:change text:change-id="c"/><text:note><text:note-citation>1</text:note-citation><text:note-body><text:p>n</text:p></text:note-body></text:note><office:annotation><text:p>c</text:p></office:annotation></text:p>`),
	content("", `<text:p><draw:frame><draw:text-box><text:p>box</text:p></draw:text-box><draw:image xlink:href="Pictures/x.png"/><svg:desc>d</svg:desc></draw:frame></text:p><text:user-field-decls><text:user-field-decl text:name="a" office:string-value="b"/></text:user-field-decls>`),
	content("", `<text:p><text:drop-down><text:label text:value="a"/>a</text:drop-down><text:conditional-text text:condition="x" text:string-value-if-true="t">f</text:conditional-text><text:hidden-text text:is-hidden="true">h</text:hidden-text><text:s text:c="9"/></text:p>`),
}

var stylesSeed = stylesXML(outlineStyles+listStyles, `<style:page-layout style:name="p"><style:page-layout-properties fo:background-color="#000000"/></style:page-layout>`,
	`<style:master-page style:name="S" style:page-layout-name="p"><style:header><text:p>h</text:p></style:header><style:footer style:display="false"><text:p>f</text:p></style:footer></style:master-page>`)

var metaSeed = metaXML(`<dc:title>t</dc:title><meta:keyword>k</meta:keyword><meta:user-defined meta:name="n">v</meta:user-defined><meta:creation-date>2026-01-01T00:00:00</meta:creation-date>`)

var manifestSeed = `<manifest:manifest` + namespaces + `><manifest:file-entry manifest:full-path="/" manifest:media-type="` + textMIME + `"/>` +
	`<manifest:file-entry manifest:full-path="content.xml"><manifest:encryption-data><manifest:algorithm manifest:algorithm-name="x"/></manifest:encryption-data></manifest:file-entry></manifest:manifest>`

func fuzzStream(f *testing.F, name string, seeds []string, opts func() *gofastcore.Options) {
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzExtract(t, fuzzPackage(t, name, data), opts())
	})
}

// FuzzContent fuzzes content.xml: the body reader and automatic styles.
func FuzzContent(f *testing.F) {
	fuzzStream(f, "content.xml", contentSeeds, func() *gofastcore.Options {
		return &gofastcore.Options{Spans: true, Images: gofastcore.ImagesData}
	})
}

// FuzzStyles fuzzes styles.xml: styles, list styles, master pages.
func FuzzStyles(f *testing.F) {
	fuzzStream(f, "styles.xml", []string{stylesSeed}, func() *gofastcore.Options { return nil })
}

// FuzzMeta fuzzes meta.xml.
func FuzzMeta(f *testing.F) {
	fuzzStream(f, "meta.xml", []string{metaSeed}, func() *gofastcore.Options { return nil })
}

// FuzzManifest fuzzes the manifest.
func FuzzManifest(f *testing.F) {
	fuzzStream(f, "META-INF/manifest.xml", []string{manifestSeed}, func() *gofastcore.Options { return nil })
}
