package style

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-odf/internal/ns"
)

const nsDecl = ` xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"` +
	` xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"` +
	` xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"` +
	` xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"` +
	` xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"` +
	` xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"` +
	` xmlns:presentation="urn:oasis:names:tc:opendocument:xmlns:presentation:1.0"` +
	` xmlns:loext="urn:org:documentfoundation:names:experimental:office:xmlns:loext:1.0"`

// stylesA declares, among others, every name stylesB uses without
// declaring it, with properties that would show if they were carried over.
const stylesA = `<office:document-styles` + nsDecl + `>` +
	`<office:scripts><office:script/></office:scripts>` +
	`<office:styles>` +
	`<style:default-style style:family="paragraph"><style:text-properties fo:font-weight="bold"/></style:default-style>` +
	`<style:style style:name="Hidden" style:family="text"><style:text-properties text:display="none"/></style:style>` +
	`<style:style style:name="Ink" style:family="text"><style:text-properties fo:color="#000000" loext:opacity="0%"/></style:style>` +
	`<style:style style:name="Head" style:family="paragraph" style:default-outline-level="3" style:list-style-name="L1" style:master-page-name="M"/>` +
	`<style:style style:name="Horizontal_20_Line" style:family="paragraph"/>` +
	`<style:style style:name="Loop" style:family="paragraph" style:parent-style-name="Loop2"/>` +
	`<style:style style:name="Loop2" style:family="paragraph" style:parent-style-name="Loop"><style:text-properties fo:font-style="italic"/></style:style>` +
	`<style:style style:name="Sheet" style:family="table"><style:table-properties table:display="false"/></style:style>` +
	`<style:style style:name="Slide" style:family="drawing-page"><style:drawing-page-properties presentation:visibility="hidden" presentation:background-objects-visible="false"/></style:style>` +
	`<text:list-style style:name="L1"><text:list-level-style-number text:level="1" style:num-format="i" style:num-prefix="(" style:num-suffix=")" text:start-value="4" text:display-levels="2"/>` +
	`<text:list-level-style-bullet text:level="2" text:bullet-char="o"/><text:list-level-style-number text:level="3" style:num-format="a"/></text:list-style>` +
	`<text:outline-style><text:outline-level-style text:level="1" style:num-format="A"/></text:outline-style>` +
	`<draw:layer-set><draw:layer draw:name="Back" draw:display="none"/></draw:layer-set>` +
	`</office:styles>` +
	`<office:automatic-styles>` +
	`<style:style style:name="P1" style:family="paragraph" style:parent-style-name="Head"><style:text-properties style:text-underline-style="solid" style:text-position="super 58%"/></style:style>` +
	`<style:style style:name="T1" style:family="text" style:parent-style-name="Hidden"/>` +
	`<text:list-style style:name="L2"><text:list-level-style-bullet text:level="1" text:bullet-char="*"/></text:list-style>` +
	`</office:automatic-styles>` +
	`</office:document-styles>`

// stylesB declares a few styles and names others it does not declare.
const stylesB = `<office:document-styles` + nsDecl + `>` +
	`<office:styles>` +
	`<style:style style:name="Own" style:family="paragraph" style:parent-style-name="Head"><style:text-properties style:text-line-through-style="solid"/></style:style>` +
	`<style:style style:name="OwnText" style:family="text" style:parent-style-name="Hidden"/>` +
	`<text:list-style style:name="LB"><text:list-level-style-bullet text:level="2" text:bullet-char="-"/></text:list-style>` +
	`</office:styles>` +
	`</office:document-styles>`

func read(t *testing.T, set *Set, stream string, sc Scope) {
	t.Helper()
	var s xmlscan.Scanner
	s.ResetBytes([]byte(stream), ns.Options)
	defer s.Release()
	if err := set.Read(&s, sc); err != nil {
		t.Fatal(err)
	}
}

// answers is what a set says of every name either stream uses.
func answers(set *Set) string {
	var b strings.Builder
	for _, sc := range []Scope{Content, Styles} {
		for _, name := range []string{"", "Hidden", "Ink", "Head", "Horizontal_20_Line", "Loop", "Loop2", "P1", "Own", "Unknown"} {
			fmt.Fprintf(&b, "para %d %s %+v\n", sc, name, set.Para(sc, name))
		}
		for _, name := range []string{"", "Hidden", "Ink", "T1", "OwnText", "Unknown"} {
			eff, own := set.Text(sc, name, Resolved{Outline: -1})
			fmt.Fprintf(&b, "text %d %s %+v %+v\n", sc, name, eff, own)
		}
		for _, fam := range []string{"table", "drawing-page"} {
			for _, name := range []string{"Sheet", "Slide"} {
				fmt.Fprintf(&b, "box %d %s %s %+v\n", sc, fam, name, set.Box(sc, fam, name))
			}
		}
		for _, name := range []string{"L1", "L2", "LB", "Unknown"} {
			if l := set.List(sc, name); l != nil {
				fmt.Fprintf(&b, "list %d %s %+v\n", sc, name, *l)
			} else {
				fmt.Fprintf(&b, "list %d %s none\n", sc, name)
			}
		}
	}
	if o := set.Outline(); o != nil {
		fmt.Fprintf(&b, "outline %+v\n", *o)
	}
	fmt.Fprintf(&b, "layer %v truncated %v damaged %v scripts %d\n", set.LayerHidden("Back"), set.Truncated, set.Damaged, set.Scripts)
	return b.String()
}

// TestReleasedSetIsEmpty: a set given back after reading one document's
// styles answers for the next document as a new set does.
func TestReleasedSetIsEmpty(t *testing.T) {
	fresh := New()
	read(t, fresh, stylesB, Styles)
	want := answers(fresh)
	if !strings.Contains(want, "Strike:true") {
		t.Fatalf("stylesB was not read: %s", want)
	}
	withA := New()
	read(t, withA, stylesA, Styles)
	read(t, withA, stylesA, Content)
	if a := answers(withA); a == want {
		t.Fatal("stylesA makes no difference to the answers; the test shows nothing")
	}
	for range 200 {
		set := Get()
		read(t, set, stylesA, Styles)
		read(t, set, stylesA, Content)
		answers(set) // fills the cache
		set.Release()
		set = Get()
		if n := len(set.common.styles) + len(set.common.lists) + len(set.auto[0].styles) + len(set.auto[0].lists) +
			len(set.auto[1].styles) + len(set.auto[1].lists) + len(set.defaults) + len(set.hiddenLayers) + len(set.cache); n != 0 ||
			set.count != 0 || set.outline != nil || set.Truncated || set.Damaged || set.Scripts != 0 || set.styles.n != 0 || set.lists.n != 0 {
			t.Fatalf("a set from Get is not empty: %d entries, count %d, outline %v, flags %v %v %d, slabs %d %d",
				n, set.count, set.outline, set.Truncated, set.Damaged, set.Scripts, set.styles.n, set.lists.n)
		}
		read(t, set, stylesB, Styles)
		if got := answers(set); got != want {
			t.Fatalf("a reused set answers\n%s\nwhere a new one answers\n%s", got, want)
		}
		set.Release()
	}
}

// TestLargeSetNotKept: a set grown past maxPooled is not reused.
func TestLargeSetNotKept(t *testing.T) {
	set := New()
	set.count = maxPooled + 1
	set.Release()
	if set.count != maxPooled+1 {
		t.Fatal("a large set was emptied for reuse")
	}
}
