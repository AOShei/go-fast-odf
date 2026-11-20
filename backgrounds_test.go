package gofastodf

import (
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Ordinary presentations: light text on a dark background that the
// master page gives, and on a slide whose background is a picture, is in
// plain view. White text on a white master still is not.
func TestSlideBackgroundsNotFlagged(t *testing.T) {
	styles := stylesXML(``,
		`<style:style style:name="Mdp1" style:family="drawing-page"><style:drawing-page-properties draw:fill="solid" draw:fill-color="#1c1c1c"/></style:style>`+
			`<style:style style:name="Mdp2" style:family="drawing-page"><style:drawing-page-properties draw:fill="bitmap" draw:fill-image-name="Sky"/></style:style>`+
			`<style:style style:name="Mdp3" style:family="drawing-page"><style:drawing-page-properties draw:fill="solid" draw:fill-color="#ffffff"/></style:style>`,
		`<style:master-page style:name="Dark" draw:style-name="Mdp1"/><style:master-page style:name="Photo" draw:style-name="Mdp2"/>`+
			`<style:master-page style:name="Light" draw:style-name="Mdp3"/>`)
	auto := `<style:style style:name="T1" style:family="text"><style:text-properties fo:color="#ffffff"/></style:style>` +
		`<style:style style:name="dp1" style:family="drawing-page"><style:drawing-page-properties presentation:background-visible="true"/></style:style>` +
		`<style:style style:name="dp2" style:family="drawing-page"><style:drawing-page-properties draw:fill="gradient"/></style:style>`
	frame := `<draw:frame svg:x="2cm" svg:y="5cm"><draw:text-box><text:p><text:span text:style-name="T1">Light words</text:span></text:p></draw:text-box></draw:frame>`
	slides := `<draw:page draw:name="p1" draw:master-page-name="Dark" draw:style-name="dp1">` + frame + `</draw:page>` +
		`<draw:page draw:name="p2" draw:master-page-name="Photo">` + frame + `</draw:page>` +
		`<draw:page draw:name="p3" draw:master-page-name="Light" draw:style-name="dp2">` + frame + `</draw:page>`
	doc := extract(t, bodyDoc(t, slideMIME, auto, `<office:presentation>`+slides+`</office:presentation>`, member{name: "styles.xml", data: styles}), nil)
	noHidden(t, doc)

	light := `<draw:page draw:name="p4" draw:master-page-name="Light">` + frame + `</draw:page>`
	doc = extract(t, bodyDoc(t, slideMIME, auto, `<office:presentation>`+light+`</office:presentation>`, member{name: "styles.xml", data: styles}), nil)
	if b := doc.Parts[0].Blocks; len(b) != 1 || b[0].Hidden != gofastcore.HiddenLowContrast {
		t.Errorf("white on white: %s", summary(b))
	}
}
