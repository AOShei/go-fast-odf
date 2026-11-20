// Package style reads the styles of an OpenDocument file (styles.xml and
// the automatic styles of each stream) as far as the extractor needs them:
// outline levels, list styles, and the text formatting that decides
// whether text can be seen (display, colour, background, size) or is
// emphasised (weight, slant, lines, position).
//
// A style inherits from its parent (style:parent-style-name) and, at the
// root, from the default style of its family. Lookups walk that chain,
// bounded and safe against cycles, and cache the result.
package style

import (
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/odpkg"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// Color is a colour as a style gives it.
type Color struct {
	Set  bool // the style says something
	None bool // transparent, or no fill: whatever is behind shows
	Auto bool // the program chooses (window font colour)
	// Varied: an opaque fill that is not one colour (a picture, a
	// gradient, a hatch). Text on it is not compared with it.
	Varied  bool
	R, G, B uint8
}

// Known reports whether the colour is a definite one.
func (c Color) Known() bool { return c.Set && !c.None && !c.Auto }

// White is the colour of paper.
var White = Color{Set: true, R: 255, G: 255, B: 255}

// Tri is a property that may be unset, on or off.
type Tri int8

const (
	Unset Tri = iota
	On
	Off
)

func (t Tri) on() bool { return t == On }

// textProps are the properties of style:text-properties.
type textProps struct {
	display   Tri // Off: text:display="none"
	color     Color
	highlight Color
	size      float64 // points, if > 0
	sizePct   float64 // percent of the inherited size, if > 0
	scale     float64 // percent width, if > 0
	opacity   float64 // percent, if >= 0 and opacitySet
	opSet     bool
	bold      Tri
	italic    Tri
	underline Tri
	strike    Tri
	position  int8 // 1 super, -1 sub, 2 explicitly normal
}

// Style is one style as written.
type Style struct {
	family     string
	parent     string
	text       textProps
	background Color // fo:background-color or a solid fill of the style's own box
	outline    int   // -1: not set
	listStyle  string
	hasList    bool
	master     string
	boxHidden  Tri // On: the sheet or slide is hidden
	name       string
	border     [4]string // top, bottom, left, right as written; "" unset
}

// Sides of a paragraph's border, as indexes of Resolved.Border.
const (
	Top = iota
	Bottom
	Left
	Right
)

// readBorders reads fo:border and fo:border-top/-bottom/-left/-right.
func readBorders(s *xmlscan.Scanner, st *Style) {
	if v, ok := s.Attr(ns.FO, "border"); ok {
		for i := range st.border {
			st.border[i] = borderValue(v)
		}
	}
	for i, side := range [4]string{"border-top", "border-bottom", "border-left", "border-right"} {
		if v, ok := s.Attr(ns.FO, side); ok {
			st.border[i] = borderValue(v)
		}
	}
}

// borderValue gives a border as written, "none" for none.
func borderValue(v []byte) string {
	b := strings.TrimSpace(string(v))
	if b == "" || b == "none" || strings.HasPrefix(b, "0pt") || strings.HasPrefix(b, "0cm") || strings.HasPrefix(b, "0in") {
		return "none"
	}
	if len(b) > 128 {
		b = b[:128]
	}
	return b
}

// Resolved is a style with everything it inherits applied.
type Resolved struct {
	// Hidden: the text is not displayed (text:display="none").
	Hidden bool
	// Color is the text colour; Highlight is the background of the
	// characters themselves.
	Color, Highlight Color
	// Background is the background of the style's own box: a paragraph's
	// shading, a cell's or a section's fill.
	Background Color
	// Size is the font size in points; 0 if no style says.
	Size float64
	// Scale is the width of the characters in percent; 0 if no style says.
	Scale float64
	// Transparent: the characters are drawn with full transparency.
	Transparent bool

	Bold, Italic, Underline, Strike, Super, Sub bool

	// Outline is the default outline level; -1 if no style says.
	Outline int
	// ListStyle is the list style the style names.
	ListStyle string
	// Master is the master page the paragraph style asks for.
	Master string
	// BoxHidden says a table style hides its sheet (table:display="false")
	// or a drawing-page style its slide (presentation:visibility="hidden").
	BoxHidden bool
	// Border is the paragraph's border on each side (Top, Bottom, Left,
	// Right) as written, "" or "none" for none.
	Border [4]string
	// HorizontalLine says the style is, or inherits from, LibreOffice's
	// "Horizontal Line" paragraph style.
	HorizontalLine bool
}

// Scope says which automatic styles apply: those of content.xml or those
// of styles.xml (for headers and footers).
type Scope uint8

const (
	Content Scope = iota
	Styles
)

// ListLevel is one level of a list style.
type ListLevel struct {
	Kind          byte // 'n' number, 'b' bullet, 'i' image, 0 none given
	Format        string
	Prefix        string
	Suffix        string
	Bullet        string
	Start         int
	DisplayLevels int
	LetterSync    bool
}

// ListStyle is a list style: up to ten levels.
type ListStyle struct {
	Levels [10]ListLevel
}

// Level returns the definition for a nesting depth from 1; deeper levels
// use the deepest defined.
func (l *ListStyle) Level(depth int) ListLevel {
	if l == nil {
		return ListLevel{}
	}
	return l.Levels[min(max(depth, 1), 10)-1]
}

type key struct {
	family, name string
}

type scope struct {
	styles map[key]*Style
	lists  map[string]*ListStyle
}

// Set is the styles of one document.
type Set struct {
	common   scope
	auto     [2]scope
	defaults map[string]*Style
	outline  *ListStyle
	masters  map[string]string // master page -> page layout
	masterBG map[string]string // master page -> its drawing-page style
	// hiddenLayers are the drawing layers that are not displayed.
	hiddenLayers map[string]bool
	first        string           // the first master page
	layouts      map[string]Color // page layout -> background
	cache        map[cacheKey]Resolved
	count        int
	layoutName   string // the page layout being read
	Truncated    bool   // more styles than maxStyles; the rest were left out
	Damaged      bool   // a stream's styles could not be read to their end
	Scripts      int    // non-empty office:scripts or office:event-listeners met
}

type cacheKey struct {
	scope  Scope
	family string
	name   string
}

// maxStyles bounds the styles kept from one document. A real document has
// hundreds; each costs memory.
const maxStyles = 1 << 16

// maxChain bounds the length of an inheritance chain.
const maxChain = 32

// New returns an empty set.
func New() *Set {
	return &Set{
		common:   scope{styles: map[key]*Style{}, lists: map[string]*ListStyle{}},
		auto:     [2]scope{{styles: map[key]*Style{}, lists: map[string]*ListStyle{}}, {styles: map[key]*Style{}, lists: map[string]*ListStyle{}}},
		defaults: map[string]*Style{},
		masters:  map[string]string{},
		masterBG: map[string]string{},

		hiddenLayers: map[string]bool{},
		layouts:      map[string]Color{},
		cache:        map[cacheKey]Resolved{},
	}
}

// ReadStyles reads styles.xml. A package without one has only the styles
// of its content.
func (set *Set) ReadStyles(pkg *odpkg.Package) error {
	if !pkg.Has("styles.xml") {
		return nil
	}
	var s xmlscan.Scanner
	c, err := pkg.Scan("styles.xml", &s)
	if err != nil {
		set.Damaged = true
		return err
	}
	defer c.Close()
	return set.Read(&s, Styles)
}

// Read reads the style elements a stream holds, until the stream ends or,
// in content.xml, the body begins. Automatic styles go to the given scope.
func (set *Set) Read(s *xmlscan.Scanner, sc Scope) error {
	var cur *Style
	var list *ListStyle
	where := &set.common
	for {
		k := s.Next()
		if k == xmlscan.EOF {
			if err := s.Err(); err != nil {
				set.Damaged = true
				return err
			}
			return nil
		}
		if k == xmlscan.End {
			switch {
			case s.Space() == ns.Style && (string(s.Local()) == "style" || string(s.Local()) == "default-style" || string(s.Local()) == "page-layout"):
				cur = nil
			case s.Space() == ns.Text && (string(s.Local()) == "list-style" || string(s.Local()) == "outline-style"):
				list = nil
			case s.Space() == ns.Office && (string(s.Local()) == "automatic-styles" || string(s.Local()) == "styles"):
				where = &set.common
			}
			continue
		}
		if k != xmlscan.Start {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch space {
		case ns.Office:
			switch local {
			case "body":
				return nil // the styles come before the body
			case "scripts", "event-listeners":
				if HasChildren(s) {
					set.Scripts++
				}
			case "automatic-styles":
				where = &set.auto[sc]
			case "styles":
				where = &set.common
			}
		case ns.Style:
			switch local {
			case "style", "default-style":
				family := string(attr(s, ns.Style, "family"))
				st := &Style{family: family, outline: -1, name: string(attr(s, ns.Style, "name"))}
				st.parent = string(attr(s, ns.Style, "parent-style-name"))
				st.master = string(attr(s, ns.Style, "master-page-name"))
				if v, ok := s.Attr(ns.Style, "default-outline-level"); ok {
					st.outline = 0
					if n, err := strconv.Atoi(strings.TrimSpace(string(v))); err == nil && n > 0 {
						st.outline = min(n, 10)
					}
				}
				if v, ok := s.Attr(ns.Style, "list-style-name"); ok {
					st.listStyle, st.hasList = string(v), true
				}
				if set.count >= maxStyles {
					set.Truncated = true
					s.Skip()
					continue
				}
				set.count++
				if local == "default-style" {
					set.defaults[family] = st
				} else {
					where.styles[key{family, string(attr(s, ns.Style, "name"))}] = st
				}
				cur = st
			case "page-layout":
				st := &Style{family: "page-layout", outline: -1}
				set.layoutName = string(attr(s, ns.Style, "name"))
				cur = st
			case "text-properties":
				if cur != nil {
					readText(s, &cur.text)
				}
			case "paragraph-properties", "table-cell-properties", "section-properties", "graphic-properties", "page-layout-properties", "table-properties", "drawing-page-properties":
				if cur != nil {
					if local == "paragraph-properties" {
						readBorders(s, cur)
					}
					if bg := background(s); bg.Set {
						cur.background = bg
						if cur.family == "page-layout" {
							set.layouts[set.layoutName] = bg
						}
					}
					// A hidden sheet, a hidden slide.
					if v, ok := s.Attr(ns.Table, "display"); ok && local == "table-properties" {
						cur.boxHidden = Off
						if string(v) == "false" {
							cur.boxHidden = On
						}
					}
					if v, ok := s.Attr(ns.Presentation, "visibility"); ok && local == "drawing-page-properties" {
						cur.boxHidden = Off
						if string(v) == "hidden" {
							cur.boxHidden = On
						}
					}
				}
			case "master-page":
				name := string(attr(s, ns.Style, "name"))
				if len(set.masters) < maxStyles {
					set.masters[name] = string(attr(s, ns.Style, "page-layout-name"))
					if dp := attr(s, ns.Draw, "style-name"); len(dp) > 0 {
						set.masterBG[name] = string(dp)
					}
				}
				if set.first == "" {
					set.first = name
				}
				s.Skip() // the content of a master page is read by the body reader
			}
		case ns.Draw:
			if local == "layer" && len(set.hiddenLayers) < maxStyles {
				if v, _ := s.Attr(ns.Draw, "display"); string(v) == "none" {
					set.hiddenLayers[string(attr(s, ns.Draw, "name"))] = true
				}
			}
		case ns.Text:
			switch local {
			case "list-style", "outline-style":
				if set.count >= maxStyles {
					set.Truncated = true
					s.Skip()
					continue
				}
				set.count++
				list = &ListStyle{}
				if local == "outline-style" {
					set.outline = list
				} else {
					where.lists[string(attr(s, ns.Style, "name"))] = list
				}
			case "list-level-style-number", "list-level-style-bullet", "list-level-style-image", "outline-level-style":
				if list == nil {
					continue
				}
				lvl, _ := strconv.Atoi(strings.TrimSpace(string(attr(s, ns.Text, "level"))))
				if lvl < 1 || lvl > 10 {
					continue
				}
				l := &list.Levels[lvl-1]
				switch local {
				case "list-level-style-bullet":
					l.Kind = 'b'
					l.Bullet = string(attr(s, ns.Text, "bullet-char"))
				case "list-level-style-image":
					l.Kind = 'i'
				default:
					l.Kind = 'n'
				}
				l.Format = string(attr(s, ns.Style, "num-format"))
				l.Prefix = string(attr(s, ns.Style, "num-prefix"))
				l.Suffix = string(attr(s, ns.Style, "num-suffix"))
				l.LetterSync = string(attr(s, ns.Style, "num-letter-sync")) == "true"
				l.Start = 1
				if n, err := strconv.Atoi(strings.TrimSpace(string(attr(s, ns.Text, "start-value")))); err == nil && n >= 0 {
					l.Start = n
				}
				l.DisplayLevels = 1
				if n, err := strconv.Atoi(strings.TrimSpace(string(attr(s, ns.Text, "display-levels")))); err == nil && n > 1 {
					l.DisplayLevels = min(n, 10)
				}
			}
		}
	}
}

// HasChildren reads past the current element and reports whether it held
// any element.
func HasChildren(s *xmlscan.Scanner) bool {
	depth := s.Depth()
	found := false
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return found
		}
		if k == xmlscan.Start {
			found = true
		}
	}
}

func attr(s *xmlscan.Scanner, space xmlscan.Space, local string) []byte {
	v, _ := s.Attr(space, local)
	return v
}

func readText(s *xmlscan.Scanner, t *textProps) {
	if v, ok := s.Attr(ns.Text, "display"); ok {
		switch string(v) {
		case "none":
			t.display = Off
		case "true":
			t.display = On
		}
	}
	if v, ok := s.Attr(ns.FO, "color"); ok {
		t.color = ParseColor(string(v))
	}
	if v, ok := s.Attr(ns.Style, "use-window-font-color"); ok && string(v) == "true" {
		t.color = Color{Set: true, Auto: true}
	}
	if v, ok := s.Attr(ns.FO, "background-color"); ok {
		t.highlight = ParseColor(string(v))
	}
	if v, ok := s.Attr(ns.FO, "font-size"); ok {
		t.size, t.sizePct = 0, 0
		if p, ok := percent(string(v)); ok {
			t.sizePct = p
		} else if pt, ok := Length(string(v)); ok {
			t.size = pt
		}
	}
	if v, ok := s.Attr(ns.Style, "text-scale"); ok {
		if p, ok := percent(string(v)); ok {
			t.scale = p
		}
	}
	if v, ok := s.Attr(ns.LOExt, "opacity"); ok {
		if p, ok := percent(string(v)); ok {
			t.opacity, t.opSet = p, true
		}
	}
	if v, ok := s.Attr(ns.FO, "font-weight"); ok {
		t.bold = Off
		switch w := string(v); w {
		case "bold", "bolder", "600", "700", "800", "900":
			t.bold = On
		}
	}
	if v, ok := s.Attr(ns.FO, "font-style"); ok {
		t.italic = Off
		if w := string(v); w == "italic" || w == "oblique" {
			t.italic = On
		}
	}
	if v, ok := s.Attr(ns.Style, "text-underline-style"); ok {
		t.underline = Off
		if string(v) != "none" {
			t.underline = On
		}
	}
	if v, ok := s.Attr(ns.Style, "text-line-through-style"); ok {
		t.strike = Off
		if string(v) != "none" {
			t.strike = On
		}
	}
	if v, ok := s.Attr(ns.Style, "text-position"); ok {
		f := strings.Fields(string(v))
		t.position = 2
		if len(f) > 0 {
			switch {
			case f[0] == "super":
				t.position = 1
			case f[0] == "sub":
				t.position = -1
			default:
				if p, ok := percent(f[0]); ok && p > 0 {
					t.position = 1
				} else if strings.HasPrefix(f[0], "-") {
					t.position = -1
				}
			}
		}
	}
}

// background reads the fill of a properties element: fo:background-color,
// or a solid draw:fill.
func background(s *xmlscan.Scanner) Color {
	if v, ok := s.Attr(ns.FO, "background-color"); ok {
		return ParseColor(string(v))
	}
	fill, ok := s.Attr(ns.Draw, "fill")
	if !ok {
		return Color{}
	}
	switch string(fill) {
	case "none":
		return Color{Set: true, None: true}
	case "solid":
		if v, ok := s.Attr(ns.Draw, "fill-color"); ok {
			return ParseColor(string(v))
		}
	case "bitmap", "gradient", "hatch":
		return Color{Set: true, Varied: true}
	}
	return Color{}
}

// ParseColor reads "#rrggbb" or "transparent".
func ParseColor(v string) Color {
	v = strings.TrimSpace(v)
	if v == "transparent" {
		return Color{Set: true, None: true}
	}
	if len(v) == 7 && v[0] == '#' {
		n, err := strconv.ParseUint(v[1:], 16, 32)
		if err == nil {
			return Color{Set: true, R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n)}
		}
	}
	return Color{}
}

func percent(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasSuffix(v, "%") {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v[:len(v)-1]), 64)
	if err != nil || f != f {
		return 0, false
	}
	return f, true
}

// Length converts a length ("12pt", "0.5cm", "3mm", "1in", "16px", "1pc")
// to points.
func Length(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	units := []struct {
		suffix string
		pt     float64
	}{{"pt", 1}, {"cm", 72 / 2.54}, {"mm", 72 / 25.4}, {"in", 72}, {"px", 0.75}, {"pc", 12}}
	for _, u := range units {
		if strings.HasSuffix(v, u.suffix) {
			f, err := strconv.ParseFloat(strings.TrimSpace(v[:len(v)-len(u.suffix)]), 64)
			if err != nil || f != f || f < 0 {
				return 0, false
			}
			return f * u.pt, true
		}
	}
	return 0, false
}

func (set *Set) lookup(sc Scope, family, name string) *Style {
	k := key{family, name}
	if st, ok := set.auto[sc].styles[k]; ok {
		return st
	}
	return set.common.styles[k]
}

// chain returns the style named and its ancestors, nearest first, then the
// family's default style.
func (set *Set) chain(sc Scope, family, name string) []*Style {
	var out []*Style
	seen := map[string]bool{}
	for name != "" && len(out) < maxChain && !seen[name] {
		seen[name] = true
		st := set.lookup(sc, family, name)
		if st == nil {
			break
		}
		out = append(out, st)
		name = st.parent
	}
	if d := set.defaults[family]; d != nil {
		out = append(out, d)
	}
	return out
}

// Para returns the resolved paragraph style of the given name (family
// "paragraph"). An unknown name resolves to the default.
func (set *Set) Para(sc Scope, name string) Resolved {
	return set.resolve(sc, "paragraph", name, Resolved{Outline: -1})
}

// Box returns the resolved style of a box of the given family
// ("table-cell", "section", "graphic", "table"): its background and the
// text properties it gives its content.
func (set *Set) Box(sc Scope, family, name string) Resolved {
	return set.resolve(sc, family, name, Resolved{Outline: -1})
}

// Text returns the properties of text in a span of the given text style,
// on top of base (the paragraph's), and the inline formatting that the
// span's style itself sets.
func (set *Set) Text(sc Scope, name string, base Resolved) (eff, own Resolved) {
	own = set.resolve(sc, "text", name, Resolved{Outline: -1})
	chain := set.chain(sc, "text", name)
	eff = base
	for i := len(chain) - 1; i >= 0; i-- {
		apply(&eff, chain[i])
	}
	return eff, own
}

func (set *Set) resolve(sc Scope, family, name string, r Resolved) Resolved {
	k := cacheKey{sc, family, name}
	if v, ok := set.cache[k]; ok {
		return v
	}
	chain := set.chain(sc, family, name)
	for i := len(chain) - 1; i >= 0; i-- {
		apply(&r, chain[i])
	}
	if len(set.cache) < maxStyles {
		set.cache[k] = r
	}
	return r
}

// apply lays a style's own properties over r.
func apply(r *Resolved, st *Style) {
	t := &st.text
	switch t.display {
	case Off:
		r.Hidden = true
	case On:
		r.Hidden = false
	}
	if t.color.Set {
		r.Color = t.color
	}
	if t.highlight.Set {
		r.Highlight = t.highlight
	}
	if st.background.Set {
		r.Background = st.background
	}
	switch {
	case t.size > 0:
		r.Size = t.size
	case t.sizePct > 0:
		base := r.Size
		if base == 0 {
			base = DefaultSize
		}
		r.Size = base * t.sizePct / 100
	}
	if t.scale > 0 {
		r.Scale = t.scale
	}
	if t.opSet {
		r.Transparent = t.opacity <= 0
	}
	set3 := func(dst *bool, v Tri) {
		if v != Unset {
			*dst = v.on()
		}
	}
	set3(&r.Bold, t.bold)
	set3(&r.Italic, t.italic)
	set3(&r.Underline, t.underline)
	set3(&r.Strike, t.strike)
	switch t.position {
	case 1:
		r.Super, r.Sub = true, false
	case -1:
		r.Super, r.Sub = false, true
	case 2:
		r.Super, r.Sub = false, false
	}
	if st.outline >= 0 {
		r.Outline = st.outline
	}
	if st.hasList {
		r.ListStyle = st.listStyle
	}
	if st.master != "" {
		r.Master = st.master
	}
	for i, b := range st.border {
		if b != "" {
			r.Border[i] = b
		}
	}
	if st.family == "paragraph" && (st.name == "Horizontal_20_Line" || st.name == "Horizontal Line") {
		r.HorizontalLine = true
	}
	if st.boxHidden != Unset {
		r.BoxHidden = st.boxHidden == On
	}
}

// DefaultSize is the font size, in points, of text no style gives a size.
const DefaultSize = 12.0

// List returns the list style of the given name.
func (set *Set) List(sc Scope, name string) *ListStyle {
	if l, ok := set.auto[sc].lists[name]; ok {
		return l
	}
	return set.common.lists[name]
}

// Outline returns the outline numbering style, or nil.
func (set *Set) Outline() *ListStyle { return set.outline }

// LayerHidden reports whether the named drawing layer is not displayed
// (draw:display="none" in the layer set).
func (set *Set) LayerHidden(name string) bool { return name != "" && set.hiddenLayers[name] }

// SlideBackground returns the background a master page of a presentation
// gives its slides (the fill of its drawing-page style, in styles.xml),
// of the first master page if the name is "" or unknown. It is not Known
// if the master page gives none.
func (set *Set) SlideBackground(master string) Color {
	dp, ok := set.masterBG[master]
	if !ok {
		if _, named := set.masters[master]; named {
			return Color{}
		}
		dp = set.masterBG[set.first]
	}
	if dp == "" {
		return Color{}
	}
	return set.Box(Styles, "drawing-page", dp).Background
}

// PageBackground returns the background of the pages of the named master
// page, or of the first master page if the name is "" or unknown. A page
// with no background is white.
func (set *Set) PageBackground(master string) Color {
	layout, ok := set.masters[master]
	if !ok {
		layout = set.masters[set.first]
	}
	if bg, ok := set.layouts[layout]; ok && bg.Known() {
		return bg
	}
	return White
}
