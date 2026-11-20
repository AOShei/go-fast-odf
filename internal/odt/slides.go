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
				p.MarkHidden()
				parts = append(parts, p)
				continue
			}
			parts = append(parts, r.slide(n))
		}
		serr = s.Err()
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

// slide reads draw:page.
func (r *reader) slide(n int) gofastcore.Part {
	s := &r.s
	part := gofastcore.Part{Kind: gofastcore.PartSlide, Number: n}
	name := clean.Name(r.attr(ns.Draw, "name"))
	page := r.set.Box(r.sc, "drawing-page", r.attr(ns.Draw, "style-name"))
	if page.BoxHidden {
		part.Hidden = gofastcore.HiddenInvisible
	}
	bg := page.Background
	if !bg.Known() {
		// Most slides show the background of their master page.
		bg = r.set.SlideBackground(r.attr(ns.Draw, "master-page-name"))
	}
	if !bg.Known() {
		bg = style.White
	}
	c := ctxt{bg: bg}
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
			r.blocks(ctxt{bg: style.White}, &nb)
			text, spans, hid, rest := r.flatten(nb)
			if text != "" {
				r.emit(&notes, gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Spans: spans, Hidden: hid,
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
			ic := c
			if ps := r.attr(ns.Presentation, "style-name"); ps != "" {
				if b := r.set.Box(r.sc, "presentation", ps).Background; b.Known() {
					ic.bg = b
				}
			}
			r.drawing(ic, &it.blocks)
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
	part.Blocks = append(part.Blocks, notes...)
	if part.Name == "" {
		part.Name = name
	}
	r.finish(&part, "slide")
	return part
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
