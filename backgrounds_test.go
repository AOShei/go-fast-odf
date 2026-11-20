package gofastodf

import "testing"

// A shape that is not displayed (draw:display="none", or on a layer that
// is not) holds text the file declares hidden.
func TestHiddenShapes(t *testing.T) {
	styles := stylesXML(``, ``, `<draw:layer-set><draw:layer draw:name="layout"/><draw:layer draw:name="secret" draw:display="none"/></draw:layer-set>`+
		`<style:master-page style:name="Default"/>`)
	slides := `<draw:page draw:name="p1">` +
		`<draw:frame svg:x="1cm" svg:y="1cm" draw:layer="layout"><draw:text-box><text:p>Shown</text:p></draw:text-box></draw:frame>` +
		`<draw:custom-shape svg:x="1cm" svg:y="3cm" draw:display="none"><text:p>Not displayed</text:p></draw:custom-shape>` +
		`<draw:frame svg:x="1cm" svg:y="5cm" draw:layer="secret"><draw:text-box><text:p>On a hidden layer</text:p></draw:text-box></draw:frame>` +
		`</draw:page>`
	doc := extract(t, bodyDoc(t, slideMIME, "", `<office:presentation>`+slides+`</office:presentation>`, member{name: "styles.xml", data: styles}), nil)
	check(t, doc.Parts[0].Blocks, `
paragraph Shown
paragraph{hidden} Not displayed
paragraph{hidden} On a hidden layer`)
	text := textDoc(t, "", `<text:p>Visible<draw:frame draw:display="none"><draw:text-box><text:p>Boxed away</text:p></draw:text-box></draw:frame></text:p>`)
	check(t, body(t, extract(t, text, nil)), `
paragraph Visible
paragraph{hidden} Boxed away`)
}

// Backgrounds are not compared with text colour: light text on a dark
// master, on a picture, and white text on a white master all carry no
// reason (a former low-contrast case).
func TestSlideBackgroundsNotJudged(t *testing.T) {
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
	noDisplay(t, doc)

	light := `<draw:page draw:name="p4" draw:master-page-name="Light">` + frame + `</draw:page>`
	doc = extract(t, bodyDoc(t, slideMIME, auto, `<office:presentation>`+light+`</office:presentation>`, member{name: "styles.xml", data: styles}), nil)
	noDisplay(t, doc)
	if b := doc.Parts[0].Blocks; len(b) != 1 || b[0].Text != "Light words" {
		t.Errorf("white on white: %s", summary(b))
	}
}
