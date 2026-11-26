package gofastodf

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func extractErr(data []byte, opts *gofastcore.Options) error {
	_, err := Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
	return err
}

func TestErrors(t *testing.T) {
	var ooxml bytes.Buffer
	zw := zip.NewWriter(&ooxml)
	w, _ := zw.Create("[Content_Types].xml")
	w.Write([]byte(`<Types/>`))
	zw.Close()

	enc := pkg(t, textMIME, member{name: "content.xml", data: "xx", encrypted: true})
	cases := []struct {
		name string
		data []byte
		opts *gofastcore.Options
		want error
	}{
		{"not zip", []byte("hello, this is not an archive"), nil, gofastcore.ErrMalformed},
		{"tiny", []byte("PK"), nil, gofastcore.ErrMalformed},
		{"ooxml", ooxml.Bytes(), nil, gofastcore.ErrUnsupported},
		{"chart", pkg(t, "application/vnd.oasis.opendocument.chart", member{name: "content.xml", data: "<x/>"}), nil, gofastcore.ErrUnsupported},
		{"not xml", []byte("<<<<>>>> not xml at all"), nil, gofastcore.ErrUnsupported},
		{"xml not odf", []byte(`<?xml version="1.0"?><html><body/></html>`), nil, gofastcore.ErrUnsupported},
		{"epub", pkg(t, "application/epub+zip", member{name: "x.xhtml", data: "<x/>"}), nil, gofastcore.ErrUnsupported},
		{"encrypted", enc, nil, gofastcore.ErrPasswordRequired},
		// The fixture's initialisation vector is 3 bytes, not 16.
		{"encrypted with password, damaged", enc, &gofastcore.Options{Password: "pw"}, gofastcore.ErrMalformed},
		{"no content", pkg(t, textMIME), nil, gofastcore.ErrMalformed},
		{"input limit", textDoc(t, "", "<text:p>x</text:p>"), &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: 10}}, gofastcore.ErrLimitExceeded},
		{"no parts", textDoc(t, "", "<text:p>x</text:p>"), &gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 3}}, gofastcore.ErrNoPartsSelected},
		{"body of another kind", pkg(t, "", member{name: "content.xml", data: `<office:document-content` + namespaces + `><office:body><office:chart/></office:body></office:document-content>`}), nil, gofastcore.ErrUnsupported},
	}
	for _, c := range cases {
		if err := extractErr(c.data, c.opts); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestDecompressionLimit(t *testing.T) {
	big := "<text:p>" + strings.Repeat("a", 1<<20) + "</text:p>"
	data := textDoc(t, "", big)
	doc := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 64 << 10}})
	if !hasWarning(doc, gofastcore.WarnLimitExceeded) {
		t.Error("no limit warning")
	}
}

func TestTextLimit(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("<text:p>paragraph of some length to fill the budget</text:p>")
	}
	data := textDoc(t, "", sb.String())
	doc := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 10000}})
	n := len(body(t, doc))
	if n == 0 || n >= 1000 || !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("%d blocks, warnings %v", n, doc.Parts[0].Warnings)
	}
	// One text:s may not blow a paragraph up beyond the budget.
	spaces := textDoc(t, "", strings.Repeat(`<text:p>x<text:s text:c="65536"/>y</text:p>`, 100))
	doc = extract(t, spaces, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 100000}})
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Error("spaces: no limit warning")
	}
}

func TestCutArchive(t *testing.T) {
	data := textDoc(t, "", "<text:p>first</text:p>"+strings.Repeat("<text:p>more text</text:p>", 50))
	// Cut off the directory at the end.
	cut := data[:len(data)-60]
	doc := extract(t, cut, nil)
	if !hasWarning(doc, gofastcore.WarnInputRepaired) {
		t.Error("no repaired warning")
	}
	if b := body(t, doc); len(b) == 0 || b[0].Text != "first" {
		t.Errorf("blocks %v", summary(b))
	}
}

func TestDeterministicAndJSON(t *testing.T) {
	data := textDoc(t, listStyles, `<text:h>T</text:h><text:list text:style-name="L1"><text:list-item><text:p>a</text:p></text:list-item></text:list>`+
		`<table:table><table:table-row><table:table-cell/></table:table-row></table:table>`)
	a, _ := json.Marshal(extract(t, data, &gofastcore.Options{Workers: 1}))
	b, _ := json.Marshal(extract(t, data, &gofastcore.Options{Workers: 8}))
	if !bytes.Equal(a, b) {
		t.Error("output depends on workers")
	}
	if !bytes.Contains(a, []byte(`"blocks":[`)) {
		t.Errorf("json %s", a)
	}
}

func TestCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := textDoc(t, "", "<text:p>x</text:p>")
	if _, err := Extract(ctx, bytes.NewReader(data), int64(len(data)), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
}

func TestActiveAndEmpty(t *testing.T) {
	data := textDoc(t, "", `<text:p><text:a xlink:href="vnd.sun.star.script:Standard.Module1.Main?language=Basic">run</text:a></text:p>`,
		member{name: "Basic/Standard/Module1.xml", data: "<x/>"})
	doc := extract(t, data, nil)
	if !hasWarning(doc, gofastcore.WarnInputActiveContent) {
		t.Error("no active content warning")
	}
	empty := extract(t, textDoc(t, "", ""), nil)
	if !empty.Parts[0].Has(gofastcore.WarnPartNoText) {
		t.Error("empty body not reported")
	}
}

func TestLigaturesAndSpans(t *testing.T) {
	data := textDoc(t, `<style:style style:name="B" style:family="text"><style:text-properties fo:font-weight="bold"/></style:style>`,
		"<text:p>o"+ligFfi+"ce <text:span text:style-name=\"B\">bold</text:span> <text:a xlink:href=\"https://example.com/\">link</text:a></text:p>")
	b := body(t, extract(t, data, &gofastcore.Options{Spans: true}))
	if b[0].Text != "office bold link" || len(b[0].Spans) != 2 || b[0].Spans[0].Style != gofastcore.StyleBold || b[0].Spans[1].Href != "https://example.com/" {
		t.Errorf("%+v", b[0])
	}
}
