package gofastodf

import (
	"testing"

	"github.com/AOShei/go-fast-core"
)

// A picture is hidden by whatever hides what it is anchored in: a hidden
// section, a hidden paragraph style, a hidden span, a hidden paragraph in
// a text box. With alt text or without: a picture with none was left
// unmarked, and pictures in a hidden section were left unmarked with or
// without it.
func TestHiddenImages(t *testing.T) {
	img := func(alt string) string {
		s := `<draw:frame><draw:image xlink:href="http://example.com/x.png"/>`
		if alt != "" {
			s += `<svg:desc>` + alt + `</svg:desc>`
		}
		return s + `</draw:frame>`
	}
	both := img("") + img("alt")
	cases := []struct{ name, body string }{
		{"recorded hidden section", `<text:section text:name="A" text:display="condition" text:condition="ooow:x" text:is-hidden="true"><text:p>T` + both + `</text:p></text:section>`},
		{"section display none", `<text:section text:name="B" text:display="none"><text:p>T` + both + `</text:p></text:section>`},
		{"hidden paragraph style", `<text:p text:style-name="HidePara">T` + both + `</text:p>`},
		{"hidden span", `<text:p>T<text:span text:style-name="Hide">` + both + `</text:span></text:p>`},
		{"hidden paragraph in a text box", `<text:p>T<draw:frame><draw:text-box><text:p text:style-name="HidePara">boxed` + both + `</text:p></draw:text-box></draw:frame></text:p>`},
		{"hidden-paragraph field", `<text:p>T` + both + `<text:hidden-paragraph text:condition="ooow:x" text:is-hidden="true"/></text:p>`},
	}
	for _, c := range cases {
		doc := extract(t, textDoc(t, hidingStyles, c.body), nil)
		n := 0
		for _, b := range body(t, doc) {
			if b.Image == nil {
				continue
			}
			n++
			if b.Display != gofastcore.DisplayHidden {
				t.Errorf("%s: picture %q has display %v", c.name, b.Text, b.Display)
			}
		}
		if n != 2 {
			t.Errorf("%s: %d pictures", c.name, n)
		}
	}
	// A picture in a shown paragraph is not marked.
	for _, b := range body(t, extract(t, textDoc(t, hidingStyles, `<text:p>T`+both+`</text:p>`), nil)) {
		if b.Display != 0 {
			t.Errorf("shown: %s %q has display %v", b.Kind, b.Text, b.Display)
		}
	}
}
