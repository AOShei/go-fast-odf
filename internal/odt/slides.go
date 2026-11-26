package odt

import (
	"fmt"
	"maps"
	"slices"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/style"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// presentation reads office:presentation, which is current, into one
// part per slide (draw:page), or office:drawing into one part per page:
// the two hold the same draw:page elements.
func (r *reader) presentation(serr error) []gofastcore.Part {
	kind, whole := gofastcore.PartSlide, "presentation"
	if r.pages {
		kind, whole = gofastcore.PartPage, "drawing"
	}
	var parts []gofastcore.Part
	var uses []slideUse
	r.heldHits = map[int][]capHit{}
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
				p := gofastcore.Part{Kind: kind, Number: n}
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
		parts = append(parts, gofastcore.Part{Kind: kind, Number: 1})
		r.finish(&parts[0], whole)
	}
	for i := range parts {
		for _, h := range r.heldHits[parts[i].Number] {
			w := h.warning()
			parts[i].Warn(w.Severity, w.Code, w.Message)
		}
	}
	r.heldHits = nil
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
	master  string // draw:master-page-name; "" if its objects are not shown
	notesAt int    // where the slide's notes begin in its blocks
}

// slide reads draw:page: a slide, or a drawing's page.
func (r *reader) slide(n int) (gofastcore.Part, slideUse) {
	s := &r.s
	part := gofastcore.Part{Kind: gofastcore.PartSlide, Number: n}
	what := "slide"
	if r.pages {
		part.Kind, what = gofastcore.PartPage, "page"
	}
	name := r.attr(ns.Draw, "name") // as the file gives it: used if no title names the slide
	use := slideUse{master: r.attr(ns.Draw, "master-page-name")}
	page := r.set.Box(r.sc, "drawing-page", r.attr(ns.Draw, "style-name"))
	if page.BoxHidden {
		part.Display = gofastcore.DisplayHidden
	}
	if page.NoMasterObjects {
		use.master = "" // the slide does not show its master's objects
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
			text, spans, hid, rest := r.flatten(nb, false)
			if text != "" {
				r.emit(&notes, gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Spans: spans, Display: hid,
					Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteSpeaker}})
			}
			notes = append(notes, rest...)
		case space == ns.Draw:
			it := item{order: len(items)}
			class := r.attr(ns.Presentation, "class")
			it.title = class == "title"
			r.place(c, &it, lastX, lastY)
			lastX, lastY = it.x, it.y
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
					part.Name = r.label(b.Text)
					break
				}
			}
		}
		part.Blocks = append(part.Blocks, it.blocks...)
	}
	use.notesAt = len(part.Blocks)
	part.Blocks = append(part.Blocks, notes...)
	if part.Name == "" {
		part.Name = r.name(name)
	}
	r.finish(&part, what)
	return part, use
}

// defaultSlideHeight is the height, in points, of LibreOffice's default
// 16:9 slide (15.75 cm), for a master whose page layout gives none;
// defaultPageHeight that of its default drawing page (A4, 29.7 cm).
const (
	defaultSlideHeight = 15.75 / 2.54 * 72
	defaultPageHeight  = 29.7 / 2.54 * 72
)

// maxRepeated bounds the blocks that slides take from their master
// pages over a presentation, as go-fast-ooxml bounds what slides take
// from their layouts and masters. A variable so that tests can lower it.
var maxRepeated int64 = 1 << 20

// SetMaxRepeatedForTest sets maxRepeated and returns a function that
// restores it. For tests only; not safe while documents are read.
func SetMaxRepeatedForTest(n int64) (restore func()) {
	old := maxRepeated
	maxRepeated = n
	return func() { maxRepeated = old }
}

// addMasters reads the text the slides' master pages show of their own
// and gives it on every slide that uses the master and shows its objects
// (presentation:background-objects-visible), before that slide's notes.
// A hidden slide's copy is hidden with the slide. Placeholders on a
// master are not content.
//
// The master's text is read, and charged to the text budget, once; each
// further copy is charged again, and the copies over the presentation
// are bounded by maxRepeated: each slide past that says so, the first
// with the number of slides left without their copy. What bounds left
// out of a master's own content is said on each slide given it.
func (r *reader) addMasters(parts []gofastcore.Part, uses []slideUse) {
	want := map[string]int{}
	for i, u := range uses {
		if _, ok := want[u.master]; u.master != "" && !ok {
			want[u.master] = i
		}
	}
	if len(want) == 0 {
		return
	}
	// The masters are in another stream (or earlier in a flat file):
	// read with a scanner of their own.
	saved := r.s
	r.s = xmlscan.Scanner{}
	masters, lostIn := r.slideMasters(&parts[len(parts)-1], want)
	r.s.Release()
	r.s = saved
	costs := map[string]int{}
	for name, blocks := range masters {
		for i := range blocks {
			costs[name] += int(cost(&blocks[i]))
		}
	}
	left := maxRepeated
	full := false
	var skipped []int // slides whose copy is left out
	said := map[string]bool{}
	for i, u := range uses {
		blocks := masters[u.master]
		if len(blocks) == 0 || i >= len(parts) {
			continue
		}
		p := &parts[i]
		switch n := int64(len(blocks)); {
		case full:
			continue
		case want[u.master] == i:
			// The first copy was charged when the master was read.
		case left < n:
			skipped = append(skipped, i)
			continue
		case !r.budget.Take(costs[u.master]):
			p.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, textCut)
			r.stopped, full = true, true
			continue
		default:
			left -= n
		}
		p.Blocks = slices.Insert(p.Blocks, min(u.notesAt, len(p.Blocks)), repeated(blocks)...)
		p.Warnings = slices.DeleteFunc(p.Warnings, func(w gofastcore.Warning) bool { return w.Code == gofastcore.WarnPartNoText })
		// What bounds left out of the master is left out of every slide
		// that shows it.
		r.heldHits[p.Number] = mergeHits(r.heldHits[p.Number], lostIn[u.master])
		said[u.master] = true
		p.Finish()
	}
	// Said on each slide it concerns, so that a slide read on its own
	// says it too; the first gives the count.
	for k, i := range skipped {
		msg := fmt.Sprintf("the text of the slide's master page is left out: the slides of the presentation repeat more of it than is kept (%d blocks)", maxRepeated)
		if r.pages {
			msg = fmt.Sprintf("the text of the page's master page is left out: the pages of the drawing repeat more of it than is kept (%d blocks)", maxRepeated)
		}
		if k == 0 {
			if r.pages {
				msg += fmt.Sprintf("; %d pages are left without it", len(skipped))
			} else {
				msg += fmt.Sprintf("; %d slides are left without it", len(skipped))
			}
		}
		parts[i].Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, msg)
	}
	// A master no slide was given: what was lost of it goes on the
	// document. The names are sorted so that the order does not vary.
	names := slices.Sorted(maps.Keys(lostIn))
	for _, name := range names {
		if !said[name] {
			r.hits = mergeHits(r.hits, lostIn[name])
		}
	}
}

// repeated copies blocks a master puts on a slide. A picture gets an
// Image of its own on each slide, so that its bytes can be left with the
// first block that shows it and taken from the others.
func repeated(from []gofastcore.Block) []gofastcore.Block {
	out := make([]gofastcore.Block, len(from))
	for i, b := range from {
		if b.Image != nil {
			img := *b.Image
			b.Image = &img
		}
		out[i] = b
	}
	return out
}

// slideMasters reads the master pages named in want from styles.xml,
// with the heights of their page layouts, into the blocks of their own
// text and pictures.
func (r *reader) slideMasters(part *gofastcore.Part, want map[string]int) (map[string][]gofastcore.Block, map[string][]capHit) {
	if !r.pkg.Has("styles.xml") {
		return nil, nil
	}
	c, err := r.pkg.Scan("styles.xml", &r.s)
	if err != nil {
		return nil, nil
	}
	lost := map[string][]capHit{}
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
			return out, lost // a flat file: the master pages came before
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
				if r.pages {
					h = defaultPageHeight
				}
			}
			// What bounds leave out of a master is said on the slides
			// that show it.
			// What is lost once for the document (an embedded document,
			// a picture) stays with the document.
			outer := r.hits
			r.hits = nil
			out[name] = r.masterItems(h)
			for _, hit := range r.hits {
				if caps[hit.id].once {
					outer = mergeHits(outer, []capHit{hit})
				} else {
					lost[name] = append(lost[name], hit)
				}
			}
			r.hits = outer
		}
	}
	if err := s.Err(); err != nil && !r.stopped {
		r.noteFailure(part, "styles.xml", err)
	}
	return out, lost
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
		r.place(ctxt{}, &it, 0, 0)
		kind := gofastcore.KindHeader
		if it.y >= height/2 {
			kind = gofastcore.KindFooter
		}
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

// position reads where the current shape stands: svg:x and svg:y, or
// for a line, connector or measure, which have none, its start point
// (svg:x1, svg:y1). A coordinate not given keeps the one passed in; ok
// reports whether either was given.
func (r *reader) position(x, y float64) (float64, float64, bool) {
	ok := false
	if v, found := r.length(ns.SVG, "x"); found {
		x, ok = v, true
	} else if v, found := r.length(ns.SVG, "x1"); found {
		x, ok = v, true
	}
	if v, found := r.length(ns.SVG, "y"); found {
		y, ok = v, true
	} else if v, found := r.length(ns.SVG, "y1"); found {
		y, ok = v, true
	}
	return x, y, ok
}

// place reads the drawing element whose Start is current, as an item at
// x, y: where it stands, or for a group with no position of its own (as
// LibreOffice writes them) where the first shape in it with one stands.
func (r *reader) place(c ctxt, it *item, x, y float64) {
	var ok bool
	it.x, it.y, ok = r.position(x, y)
	group := !ok && string(r.s.Local()) == "g"
	if group {
		r.groupAt = &groupPos{x: x, y: y}
	}
	r.drawing(c, &it.blocks)
	if group {
		if r.groupAt.found {
			it.x, it.y = r.groupAt.x, r.groupAt.y
		}
		r.groupAt = nil
	}
}

// groupPos is the position of the first shape with one met in a group
// being read.
type groupPos struct {
	x, y  float64
	found bool
}

// length reads a length attribute in points.
func (r *reader) length(space xmlscan.Space, local string) (float64, bool) {
	v, ok := r.s.Attr(space, local)
	if !ok {
		return 0, false
	}
	return style.Length(string(v))
}
