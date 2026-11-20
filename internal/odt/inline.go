package odt

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/display"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/style"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

var (
	decodeRune     = utf8.DecodeRune
	decodeLastRune = utf8.DecodeLastRune
	settle         = display.Settle
)

// maxSpaces bounds what one text:s may ask for.
const maxSpaces = 1 << 16

// spaceRun is sliced for the spaces of text:s, so that a text:s with no
// room left costs no allocation.
var spaceRun = strings.Repeat(" ", maxSpaces)

// reasons gives what the file declares about the display of text with
// the given formatting: text:display="none" (hidden), a definite colour
// with loext:opacity of 0 (no ink), and what a field or container
// declares (ru.hidden).
func reasons(ru *run) gofastcore.Display {
	h := ru.hidden
	if ru.eff.DisplayNone {
		h |= gofastcore.DisplayHidden
	}
	if ru.eff.NoInk {
		h |= gofastcore.DisplayNoInk
	}
	return h
}

func inlineStyle(o style.Resolved) gofastcore.Style {
	var st gofastcore.Style
	if o.Bold {
		st |= gofastcore.StyleBold
	}
	if o.Italic {
		st |= gofastcore.StyleItalic
	}
	if o.Underline {
		st |= gofastcore.StyleUnderline
	}
	if o.Strike {
		st |= gofastcore.StyleStrike
	}
	if o.Super {
		st |= gofastcore.StyleSuper
	}
	if o.Sub {
		st |= gofastcore.StyleSub
	}
	return st
}

// room reports whether n more bytes may go into the paragraph; the budget
// left bounds what one paragraph, together with the paragraphs it stands
// in (r.held), may hold before it is charged.
func (r *reader) room(p *para, n int) bool {
	if p.cut {
		return false
	}
	if r.budget != nil && int64(r.held+len(p.buf)+n) > r.budget.Left() {
		p.cut = true
		return false
	}
	return true
}

// mark records a span over p.buf[start:] for the run, if it says
// anything.
func (r *reader) mark(p *para, ru *run, start int) {
	end := len(p.buf)
	if end <= start {
		return
	}
	sp := gofastcore.Span{Start: start, End: end, Display: reasons(ru)}
	if r.opts.Spans {
		sp.Style, sp.Href = ru.inline, ru.href
	}
	if sp.Display == 0 && sp.Style == 0 && sp.Href == "" {
		return
	}
	if n := len(p.spans); n > 0 {
		last := &p.spans[n-1]
		if last.End == start && last.Display == sp.Display && last.Style == sp.Style && last.Href == sp.Href {
			last.End = end
			return
		}
	}
	p.spans = append(p.spans, sp)
}

// text appends character data, white space collapsed as OpenDocument
// asks: a run of spaces, tabs and line ends is one space, and none at the
// start of the paragraph.
func (r *reader) text(p *para, ru *run, b []byte) {
	if !r.room(p, len(b)) {
		return
	}
	start := len(p.buf)
	i := 0
	for i < len(b) {
		c := b[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if !p.space {
				p.buf = append(p.buf, ' ')
				p.space = true
			}
			i++
			continue
		}
		j := i
		for j < len(b) && b[j] != ' ' && b[j] != '\t' && b[j] != '\n' && b[j] != '\r' {
			j++
		}
		var removed int
		p.buf, removed = clean.Append(p.buf, b[i:j], clean.Ligatures)
		r.removed += removed
		p.space = false
		i = j
	}
	r.mark(p, ru, start)
}

// literal appends text that is not collapsed: a tab, a line break, the
// spaces of text:s.
func (r *reader) literal(p *para, ru *run, s string, space bool) {
	if !r.room(p, len(s)) {
		return
	}
	start := len(p.buf)
	p.buf = append(p.buf, s...)
	p.space = space
	r.mark(p, ru, start)
}

// inline reads the content of the current element as text of p, until
// the element at depth ends.
func (r *reader) inline(p *para, ru run, depth int) {
	s := &r.s
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		switch k {
		case xmlscan.Text:
			r.text(p, &ru, s.Text())
			continue
		case xmlscan.Start:
		default:
			continue
		}
		if !r.alive() {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch space {
		case ns.Text:
			r.inlineText(p, ru, local)
		case ns.Draw:
			r.drawing(ctxt{hidden: reasons(&ru) & gofastcore.DisplayHidden}, &p.after)
		case ns.Office:
			switch local {
			case "annotation":
				r.annotation(ctxt{}, &p.after)
			case "annotation-end":
				s.Skip()
			case "event-listeners":
				if style.HasChildren(s) {
					r.active++
				}
			default:
				r.inline(p, ru, s.Depth())
			}
		case ns.Script:
			r.active++
			s.Skip()
		default:
			r.inline(p, ru, s.Depth())
		}
	}
}

// inlineText handles an element of the text namespace inside a paragraph.
func (r *reader) inlineText(p *para, ru run, local string) {
	s := &r.s
	switch local {
	case "span":
		eff, own := r.set.Text(r.sc, r.attr(ns.Text, "style-name"), ru.eff)
		inner := ru
		inner.eff = eff
		inner.inline |= inlineStyle(own)
		r.inline(p, inner, s.Depth())
	case "a":
		href := r.attr(ns.XLink, "href")
		if isMacro(href) {
			r.active++
		}
		inner := ru
		if r.opts.Spans {
			inner.href = clean.Address(href)
			if inner.href != "" && !r.budget.Take(len(inner.href)) {
				r.stop(textCut)
				return
			}
		}
		r.inline(p, inner, s.Depth())
	case "s":
		n := 1
		if v, err := strconv.Atoi(strings.TrimSpace(r.attr(ns.Text, "c"))); err == nil && v > 0 {
			n = min(v, maxSpaces)
		}
		r.literal(p, &ru, spaceRun[:n], false)
		s.Skip()
	case "tab":
		r.literal(p, &ru, "\t", true)
		s.Skip()
	case "line-break":
		r.literal(p, &ru, "\n", true)
		s.Skip()
	case "number":
		// The label of a numbered heading or list item as the producer
		// computed it.
		v := s.AppendText(nil)
		p.marker, _ = clean.String(v, clean.Prose)
		p.marker = clean.Label(p.marker)
		p.hasMarker = p.marker != ""
	case "note":
		r.note(p, ru)
	case "change":
		r.useDeletion(r.attr(ns.Text, "change-id"), &p.after)
		s.Skip()
	case "hidden-paragraph":
		if recordedHidden(r.attr(ns.Text, "is-hidden")) {
			p.hidden |= gofastcore.DisplayHidden
		}
		s.Skip()
	case "hidden-text":
		inner := ru
		if recordedHidden(r.attr(ns.Text, "is-hidden")) {
			inner.hidden |= gofastcore.DisplayHidden
		}
		start := len(p.buf)
		r.inline(p, inner, s.Depth())
		if len(p.buf) == start {
			// No content written: the text is in the attribute.
			if v := r.attrValue("string-value"); v != "" {
				r.text(p, &inner, []byte(v))
			}
		}
	case "conditional-text":
		r.conditional(p, ru)
	case "variable-set", "variable-get", "variable-input", "user-field-get", "user-field-input",
		"expression", "sequence", "user-defined":
		inner := ru
		if r.attr(ns.Text, "display") == "none" {
			inner.hidden |= gofastcore.DisplayHidden
		}
		r.inline(p, inner, s.Depth())
	case "drop-down":
		r.dropDown(p, ru)
	case "ruby":
		r.ruby(p, ru)
	case "execute-macro":
		r.active++
		r.inline(p, ru, s.Depth())
	case "soft-page-break", "bookmark", "bookmark-start", "bookmark-end", "reference-mark",
		"reference-mark-start", "reference-mark-end", "change-start", "change-end",
		"toc-mark", "toc-mark-start", "toc-mark-end", "alphabetical-index-mark",
		"alphabetical-index-mark-start", "alphabetical-index-mark-end", "user-index-mark",
		"user-index-mark-start", "user-index-mark-end", "bibliography-mark-end":
		s.Skip()
	case "p", "h":
		// A paragraph inside a paragraph is not allowed; its text is kept
		// as part of this one, on a line of its own.
		if len(p.buf) > 0 {
			r.literal(p, &ru, "\n", true)
		}
		r.inline(p, ru, s.Depth())
	default:
		// Fields (dates, page numbers, titles, references, ...) show their
		// value as their content.
		r.inline(p, ru, s.Depth())
	}
}

func (r *reader) attrValue(local string) string {
	v, _ := r.s.Attr(ns.Text, local)
	out, removed := clean.String(v, clean.Prose)
	r.removed += removed
	return out
}

func isMacro(href string) bool {
	h := strings.ToLower(strings.TrimSpace(href))
	return strings.HasPrefix(h, "vnd.sun.star.script:") || strings.HasPrefix(h, "macro:") || strings.HasPrefix(h, "javascript:")
}

// recordedHidden reads text:is-hidden of a conditionally hidden paragraph
// or text: what the file records as hidden. The condition itself is never
// evaluated.
func recordedHidden(isHidden string) bool { return isHidden == "true" }

// conditional reads text:conditional-text: its content is what is shown;
// the value for the other outcome of its condition is kept in a note.
func (r *reader) conditional(p *para, ru run) {
	cond := r.attrValue("condition")
	ifTrue := r.attrValue("string-value-if-true")
	ifFalse := r.attrValue("string-value-if-false")
	r.inline(p, ru, r.s.Depth())
	if cond == "" && ifTrue == "" && ifFalse == "" {
		return
	}
	text := "IF " + cond + " THEN \"" + ifTrue + "\" ELSE \"" + ifFalse + "\""
	// The field's instruction: stored data (a field code), with no reason.
	b := gofastcore.Block{Kind: gofastcore.KindNote, Text: strings.TrimSpace(text),
		Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteField, gofastcore.AttrField: "conditional-text",
			gofastcore.AttrStored: gofastcore.StoredFieldCode}}
	r.emit(&p.after, b)
}

// dropDown reads text:drop-down: the value chosen is in the text, and the
// values offered become a choices note.
func (r *reader) dropDown(p *para, ru run) {
	s := &r.s
	name := clean.Name(r.attr(ns.Text, "name"))
	var values []string
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		switch k {
		case xmlscan.Text:
			r.text(p, &ru, s.Text())
		case xmlscan.Start:
			if s.Space() == ns.Text && string(s.Local()) == "label" && len(values) < 1<<12 {
				if v := r.attrValue("value"); v != "" {
					values = append(values, v)
				}
			}
		}
	}
	if len(values) == 0 {
		return
	}
	b := gofastcore.Block{Kind: gofastcore.KindNote, Text: strings.Join(values, "\n"),
		Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteChoices}}
	if name != "" {
		b.Attrs[gofastcore.AttrField] = name
	}
	r.emit(&p.after, b)
}

// ruby reads text:ruby: the base text, then its annotation in brackets.
func (r *reader) ruby(p *para, ru run) {
	s := &r.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start {
			continue
		}
		switch {
		case s.Space() == ns.Text && string(s.Local()) == "ruby-text":
			v, _ := clean.String(s.AppendText(nil), clean.Prose)
			if v != "" {
				r.literal(p, &ru, "("+v+")", false)
			}
		default:
			r.inline(p, ru, s.Depth())
		}
	}
}
