package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

const content = `<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"` +
	` xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:xlink="http://www.w3.org/1999/xlink">`

// writeODT writes a text document with body, and extra members given as
// name and content pairs.
func writeODT(t *testing.T, path, body string, extra ...string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	w.Write([]byte("application/vnd.oasis.opendocument.text"))
	w, _ = zw.Create("content.xml")
	w.Write([]byte(content + `<office:body><office:text>` + body + `</office:text></office:body></office:document-content>`))
	for i := 0; i+1 < len(extra); i += 2 {
		w, _ = zw.Create(extra[i])
		w.Write([]byte(extra[i+1]))
	}
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

	for _, bad := range [][]string{{}, {"-format", "pdf", a}, {"-images", "all", a}, {"-parts", "3-1", a}, {"-exclude-display=nonsense", a}, {"-exclude-display=unknown", a}} {
		if code := run(context.Background(), bad, &out, &errs); code != 2 {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
}

// The render flags of COMMAND_FLAGS.md: -exclude-display alone, with
// =hidden and =all, and =unknown an error; -stored adds the stored part.
func TestRenderFlags(t *testing.T) {
	var f displayFlag
	for v, want := range map[string]gofastcore.Display{
		"true": gofastcore.DisplayAny, "all": gofastcore.DisplayAny, "": 0, "false": 0,
		"hidden": gofastcore.DisplayHidden, "hidden,no_ink": gofastcore.DisplayHidden | gofastcore.DisplayNoInk,
		"no_ink+hidden": gofastcore.DisplayHidden | gofastcore.DisplayNoInk,
	} {
		if err := f.Set(v); err != nil || f.reasons != want {
			t.Errorf("%q: %v %v", v, f.reasons, err)
		}
	}
	for _, v := range []string{"unknown", "hidden,unknown", "nonsense"} {
		if err := f.Set(v); err == nil || !strings.Contains(err.Error(), "hidden") {
			t.Errorf("%q: %v", v, err)
		}
	}

	dir := t.TempDir()
	a := filepath.Join(dir, "a.odt")
	writeODT(t, a, `<text:user-field-decls><text:user-field-decl office:value-type="string" office:string-value="Kept value" text:name="Client"/></text:user-field-decls>`+
		`<text:p>Shown.</text:p><text:section text:name="S" text:display="none"><text:p>Secret.</text:p></text:section>`)
	render := func(args ...string) string {
		t.Helper()
		var out, errs bytes.Buffer
		if code := run(context.Background(), append(append([]string{"-format", "text"}, args...), a), &out, &errs); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errs.String())
		}
		return out.String()
	}
	if got := render(); !strings.Contains(got, "Shown.") || !strings.Contains(got, "Secret.") || strings.Contains(got, "Kept value") {
		t.Errorf("no flags: %q", got)
	}
	for _, args := range [][]string{{"-exclude-display"}, {"-exclude-display=hidden"}, {"-exclude-display=all"}} {
		if got := render(args...); !strings.Contains(got, "Shown.") || strings.Contains(got, "Secret.") {
			t.Errorf("%v: %q", args, got)
		}
	}
	if got := render("-exclude-display=no_ink"); !strings.Contains(got, "Secret.") {
		t.Errorf("no_ink: %q", got)
	}
	if got := render("-stored"); !strings.Contains(got, "Kept value") {
		t.Errorf("-stored: %q", got)
	}
	var out, errs bytes.Buffer
	if code := run(context.Background(), []string{"-exclude-display=unknown", a}, &out, &errs); code != 2 || !strings.Contains(errs.String(), "the reasons are hidden, no_ink\n") {
		t.Errorf("unknown: exit %d, %q", code, errs.String())
	}
}

// nested writes a text document that embeds a text document as Object 1,
// which embeds another, levels deep, and an OLE object beside the first.
func nested(t *testing.T, path string, levels int) {
	t.Helper()
	object := `<text:p><draw:frame><draw:object xlink:href="./Object 1"/></draw:frame></text:p>`
	var extra []string
	prefix := ""
	for level := 1; level <= levels; level++ {
		prefix += "Object 1/"
		extra = append(extra, prefix+"content.xml", content+`<office:body><office:text><text:p>Level `+strconv.Itoa(level)+`.</text:p>`+object+`</office:text></office:body></office:document-content>`)
	}
	extra = append(extra, "Object 2", "\xd0\xcf\x11\xe0 not really OLE")
	writeODT(t, path, `<text:p>Outer.</text:p>`+object+`<text:p><draw:frame><draw:object-ole xlink:href="./Object 2"/></draw:frame></text:p>`, extra...)
}

// The child dispatcher reads the OpenDocument objects a document embeds,
// as deep as core's depth limit, and lists anything else unread.
func TestChildren(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.odt")
	nested(t, a, gofastcore.DefaultMaxDepth+1)
	var out, errs bytes.Buffer
	if code := run(context.Background(), []string{a}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var doc gofastcore.Document
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(doc.Children) != 2 || doc.Children[0].Source.Format != "odt" || doc.Children[1].Warnings[0].Code != gofastcore.WarnChildUnsupported {
		t.Fatalf("children %+v", doc.Children)
	}
	depth := 0
	for d := doc.Children[0]; ; d = d.Children[0] {
		depth++
		if len(d.Parts) == 0 {
			if depth != gofastcore.DefaultMaxDepth+1 || d.Warnings[0].Code != gofastcore.WarnChildNotExtracted {
				t.Errorf("depth %d: %+v", depth, d.Warnings)
			}
			break
		}
		if got := d.Parts[0].Blocks[0].Text; got != "Level "+strconv.Itoa(depth)+"." {
			t.Errorf("depth %d: %q", depth, got)
		}
		if len(d.Children) == 0 {
			t.Fatalf("depth %d: no children", depth)
		}
	}
}

// -children: on by default, a child's text and the line naming it are in
// the text output; -children=false leaves both out.
func TestChildrenFlag(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.odt")
	nested(t, a, 1)
	render := func(args ...string) string {
		t.Helper()
		var out, errs bytes.Buffer
		if code := run(context.Background(), append(append([]string{"-format", "text"}, args...), a), &out, &errs); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errs.String())
		}
		return out.String()
	}
	if got := render(); !strings.Contains(got, "Outer.") || !strings.Contains(got, "Level 1.") || !strings.Contains(got, "[child 1: Object 1]") {
		t.Errorf("default: %q", got)
	}
	if got := render("-children=false"); !strings.Contains(got, "Outer.") || strings.Contains(got, "Level 1.") || strings.Contains(got, "[child") {
		t.Errorf("-children=false: %q", got)
	}
}
