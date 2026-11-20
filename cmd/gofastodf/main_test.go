package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

func writeODT(t *testing.T, path, body string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	w.Write([]byte("application/vnd.oasis.opendocument.text"))
	w, _ = zw.Create("content.xml")
	w.Write([]byte(`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">` +
		`<office:body><office:text>` + body + `</office:text></office:body></office:document-content>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommand(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.odt")
	writeODT(t, a, `<text:h text:outline-level="1">Title</text:h><text:p>Body text.</text:p>`)
	writeODT(t, filepath.Join(dir, "b.odt"), `<text:p>Second.</text:p>`)
	os.WriteFile(filepath.Join(dir, "bad.odt"), []byte("not a document"), 0o644)
	os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("ignored"), 0o644)

	var out, errs bytes.Buffer
	if code := run(context.Background(), []string{a}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var doc gofastcore.Document
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil || doc.Source.Name != "a.odt" || doc.Parts[0].Blocks[0].Text != "Title" {
		t.Errorf("doc %+v %v", doc.Source, err)
	}

	out.Reset()
	errs.Reset()
	code := run(context.Background(), []string{"-format", "markdown", dir}, &out, &errs)
	if code != 1 || !strings.Contains(errs.String(), "bad.odt") {
		t.Errorf("exit %d, stderr %q", code, errs.String())
	}
	if md := out.String(); !strings.Contains(md, "# Title") || !strings.Contains(md, "Second.") || strings.Contains(md, "ignored") {
		t.Errorf("markdown %q", md)
	}

	for _, bad := range [][]string{{}, {"-format", "pdf", a}, {"-images", "all", a}, {"-parts", "3-1", a}, {"-exclude-display=nonsense", a}} {
		if code := run(context.Background(), bad, &out, &errs); code != 2 {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
}
