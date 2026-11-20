package gofastodf

import (
	"testing"

	"github.com/AOShei/go-fast-core"
)

// A form control placed by a draw:control the file declares not displayed
// (draw:display="none", ODF 1.3 part 3 19.124) is hidden; a placed control
// with no such declaration has no reason. A form:hidden control and a
// control no draw:control places stay in the stored part with no reason,
// and a password field keeps its value with no reason.
func TestControlDisplay(t *testing.T) {
	forms := `<office:forms><form:form form:name="F">` +
		`<form:text form:id="c1" form:name="Shown" form:current-value="Ann"/>` +
		`<form:text form:id="c2" form:name="Off" form:current-value="Bob"/>` +
		`<form:password form:id="c3" form:name="Pin" form:value="1234"/>` +
		`<form:hidden form:id="c4" form:name="token" form:value="abc"/>` +
		`<form:text form:id="c5" form:name="Loose" form:current-value="Cy"/>` +
		`</form:form></office:forms>`
	doc := extract(t, textDoc(t, "", forms+
		`<text:p><draw:control draw:control="c1"/></text:p>`+
		`<text:p><draw:control draw:control="c2" draw:display="none"/></text:p>`+
		`<text:p><draw:control draw:control="c3"/></text:p>`), nil)
	check(t, body(t, doc), `
paragraph Ann
paragraph{hidden} Bob
paragraph 1234`)
	stored := doc.Parts[len(doc.Parts)-1]
	if stored.Kind != gofastcore.PartStored {
		t.Fatal("no stored part")
	}
	found := map[string]bool{}
	for _, b := range stored.Blocks {
		if b.Attrs[gofastcore.AttrStored] == gofastcore.StoredFormField {
			found[b.Attrs[gofastcore.AttrField]] = true
			if b.Display != 0 {
				t.Errorf("stored %q has %v", b.Text, b.Display)
			}
		}
	}
	if !found["token"] || !found["Loose"] {
		t.Errorf("stored form fields %v", found)
	}
}
