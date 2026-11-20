package odt

import (
	"slices"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/style"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// presentation reads office:presentation, which is current, into one
// part per slide (draw:page).
func (r *reader) presentation(serr error) []gofastcore.Part {
	var parts []gofastcore.Part
	var uses []slideUse
	if serr == nil && r.s.Depth() == 3 {
		s := &r.s
		depth := s.Depth()
		for {
			k := s.Next()
			if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
				break
			}
			if k != xmlscan.Start || !r.alive() {
				continue
			}
			if s.Space() != ns.Draw || string(s.Local()) != "page" {
				s.Skip()
				continue
			}
			n := len(parts) + 1
			if !r.opts.Parts.Contains(n) {
				s.Skip()
				p := gofastcore.Part{Kind: gofastcore.PartSlide, Number: n}
				p.Finish()
				parts = append(parts, p)
				uses = append(uses, slideUse{})
				continue
			}
			p, u := r.slide(n)
			parts = append(parts, p)
			uses = append(uses, u)
		}
		serr = s.Err()
		if serr == nil && !r.stopped && r.ctx.Err() == nil {
			r.addMasters(parts, uses)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, gofastcore.Part{Kind: gofastcore.PartSlide, Number: 1})
		r.finish(&parts[0], "presentation")
	}
	last := &parts[len(parts)-1]
	r.noteFailure(last, "content.xml", serr)
	if r.limitMsg != "" {
		last.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, r.limitMsg)
		r.limitMsg = ""
	}
	return parts
}

// item is one thing on a slide: a frame, a shape, a group.
type item struct {
	x, y   float64
	title  bool
	order  int
	blocks []gofastcore.Block
}

// slideUse is what the master pages need to know of a slide read.
type slideUse struct {
	master  string // draw:master-page-name
	hidden  bool   // the slide is declared hidden
	notesAt int    // where the slide's notes begin in its blocks
}

// slide reads draw:page.
func (r *reader) slide(n int) (gofastcore.Part, slideUse) {
	s := &r.s
	part := gofastcore.Part{Kind: gofastcore.PartSlide, Number: n}
	name := clean.Name(r.attr(ns.Draw, "name"))
	use := slideUse{master: r.attr(ns.Draw, "master-page-name")}
	page := r.set.Box(r.sc, "drawing-page", r.attr(ns.Draw, "style-name"))
	if page.BoxHidden {
		part.Display = gofastcore.DisplayHidden
	}
	c := ctxt{}
	var items []item
	var notes []gofastcore.Block
	lastX, lastY := 0.0, 0.0
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch {
		case space == ns.Presentation && local == "notes":
			var nb []gofastcore.Block
			r.blocks(ctxt{}, &nb)
			text, spans, hid, rest := r.flatten(nb)
			if text != "" {
				r.emit(&notes, gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Spans: spans, Display: hid,
					Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteSpeaker}})
			}
			notes = append(notes, rest...)
		case space == ns.Draw:
			it := item{order: len(items), x: lastX, y: lastY}
			if x, ok := r.length(ns.SVG, "x"); ok {
				it.x = x
			}
			if y, ok := r.length(ns.SVG, "y"); ok {
				it.y = y
			}
			lastX, lastY = it.x, it.y
			class := r.attr(ns.Presentation, "class")
			it.title = class == "title"
			r.drawing(c, &it.blocks)
			for i := range it.blocks {
				b := &it.blocks[i]
				if b.Table != nil || b.Image != nil || b.Kind == gofastcore.KindNote || b.Kind == gofastcore.KindFootnote {
					continue
				}
				switch class {
				case "title":
					b.Kind, b.Level, b.Marker, b.Ordered = gofastcore.KindHeading, 1, "", false
				case "footer", "date-time", "page-number":
					markerInText(b)
					b.Kind, b.Level = gofastcore.KindFooter, 0
				case "header":
					markerInText(b)
					b.Kind, b.Level = gofastcore.KindHeader, 0
				}
			}
			if len(it.blocks) > 0 {
				items = append(items, it)
			}
		case space == ns.Office && local == "forms":
			r.readForms()
		default:
			s.Skip()
		}
	}
	// Reading order: the title first, then from the top down and, at one
	// height, from the left.
	slices.SortStableFunc(items, func(a, b item) int {
		switch {
		case a.title != b.title:
			if a.title {
				return -1
			}
			return 1
		case a.y != b.y:
			if a.y < b.y {
				return -1
			}
			return 1
		case a.x != b.x:
			if a.x < b.x {
				return -1
			}
			return 1
		}
		return a.order - b.order
	})
	for _, it := range items {
		if it.title && part.Name == "" {
			for _, b := range it.blocks {
				if b.Kind == gofastcore.KindHeading {
					part.Name = clean.Label(b.Text)
					break
				}
			}
		}
		part.Blocks = append(part.Blocks, it.blocks...)
	}
	use.notesAt, use.hidden = len(part.Blocks), part.Display != 0
	part.Blocks = append(part.Blocks, notes...)
	if part.Name == "" {
		part.Name = name
	}
	r.finish(&part, "slide")
	return part, use
}

// defaultSlideHeight is the height, in points, of LibreOffice's default
// 16:9 slide (15.75 cm), for a master whose page layout gives none.
const defaultSlideHeight = 15.75 / 2.54 * 72

// addMasters reads the text the slides' master pages show of their own
// and gives it once per master, on the first slide that uses the master
// and is not hidden (or the first, if all are), before that slide's
// notes. Placeholders on a master are not content.
func (r *reader) addMasters(parts []gofastcore.Part, uses []slideUse) {
	first := map[string]int{}
	var order []string
	for i, u := range uses {
		if u.master == "" {
			continue
		}
		j, ok := first[u.master]
		if !ok {
			order = append(order, u.master)
		}
		if !ok || uses[j].hidden && !u.hidden {
			first[u.master] = i
		}
	}
	if len(order) == 0 {
		return
	}
	// The masters are in another stream (or earlier in a flat file):
	// read with a scanner of their own.
	saved := r.s
	r.s = xmlscan.Scanner{}
	masters := r.slideMasters(&parts[len(parts)-1], first)
	r.s = saved
	for _, name := range order {
		blocks := masters[name]
		if len(blocks) == 0 {
			continue
		}
		i := first[name]
		p := &parts[i]
		p.Blocks = slices.Insert(p.Blocks, min(uses[i].notesAt, len(p.Blocks)), blocks...)
		p.Warnings = slices.DeleteFunc(p.Warnings, func(w gofastcore.Warning) bool { return w.Code == gofastcore.WarnPartNoText })
		p.Finish()
	}
}

// slideMasters reads the master pages named in want from styles.xml,
// with the heights of their page layouts, into the blocks of their own
// text and pictures.
func (r *reader) slideMasters(part *gofastcore.Part, want map[string]int) map[string][]gofastcore.Block {
	if !r.pkg.Has("styles.xml") {
		return nil
	}
	c, err := r.pkg.Scan("styles.xml", &r.s)
	if err != nil {
		return nil
	}
	defer c.Close()
	r.sc = style.Styles
	defer func() { r.sc = style.Content }()
	heights := map[string]float64{}
	layout := ""
	out := map[string][]gofastcore.Block{}
	s := &r.s
	for {
		k := s.Next()
		if k == xmlscan.EOF {
			break
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch {
		case space == ns.Office && local == "body":
			return out // a flat file: the master pages came before
		case space == ns.Office && s.Depth() == 2 && local != "automatic-styles" && local != "master-styles":
			s.Skip()
		case space == ns.Style && local == "page-layout":
			layout = r.attr(ns.Style, "name")
		case space == ns.Style && local == "page-layout-properties":
			if h, ok := r.length(ns.FO, "page-height"); ok && h > 0 && layout != "" && len(heights) < 1<<12 {
				heights[layout] = h
			}
		case space == ns.Style && local == "master-page":
			name := r.attr(ns.Style, "name")
			_, wanted := want[name]
			_, done := out[name]
			if !wanted || done {
				s.Skip()
				continue
			}
			h, ok := heights[r.attr(ns.Style, "page-layout-name")]
			if !ok {
				h = defaultSlideHeight
			}
			out[name] = r.masterItems(h)
		}
	}
	if err := s.Err(); err != nil && !r.stopped {
		r.noteFailure(part, "styles.xml", err)
	}
	return out
}

// masterItems reads the children of a style:master-page: the shapes that
// are not placeholders, as header text if they start in the upper half
// of the slide and footer text if in the lower, in reading order.
func (r *reader) masterItems(height float64) []gofastcore.Block {
	s := &r.s
	var items []item
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		if s.Space() != ns.Draw || r.attr(ns.Presentation, "class") != "" || r.attr(ns.Presentation, "placeholder") == "true" {
			s.Skip() // notes master, placeholders, forms
			continue
		}
		it := item{order: len(items)}
		it.x, _ = r.length(ns.SVG, "x")
		it.y, _ = r.length(ns.SVG, "y")
		kind := gofastcore.KindHeader
		if it.y >= height/2 {
			kind = gofastcore.KindFooter
		}
		r.drawing(ctxt{}, &it.blocks)
		for i := range it.blocks {
			b := &it.blocks[i]
			if b.Table != nil || b.Image != nil || b.Kind == gofastcore.KindNote || b.Kind == gofastcore.KindFootnote {
				continue
			}
			markerInText(b)
			b.Kind, b.Level = kind, 0
		}
		if len(it.blocks) > 0 {
			items = append(items, it)
		}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		switch {
		case a.y != b.y:
			if a.y < b.y {
				return -1
			}
			return 1
		case a.x != b.x:
			if a.x < b.x {
				return -1
			}
			return 1
		}
		return a.order - b.order
	})
	var out []gofastcore.Block
	for _, it := range items {
		out = append(out, it.blocks...)
	}
	return out
}

// markerInText puts a block's label into its text, for a block that
// becomes furniture and so has no label of its own.
func markerInText(b *gofastcore.Block) {
	if b.Marker == "" {
		return
	}
	shift := len(b.Marker) + 1
	b.Text = b.Marker + " " + b.Text
	for i := range b.Spans {
		b.Spans[i].Start += shift
		b.Spans[i].End += shift
	}
	b.Marker, b.Ordered = "", false
}

// length reads a length attribute in points.
func (r *reader) length(space xmlscan.Space, local string) (float64, bool) {
	v, ok := r.s.Attr(space, local)
	if !ok {
		return 0, false
	}
	return style.Length(string(v))
}
