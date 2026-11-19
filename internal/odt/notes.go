package odt

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// deletion is text removed by a tracked change, as text:tracked-changes
// keeps it.
type deletion struct {
	order        int
	author, date string
	blocks       []gofastcore.Block
}

// flatten joins the text blocks read from a note, comment, deletion or
// table cell into one text, one line per block, with their spans and
// hidden reasons. Blocks that are not text (pictures, notes inside them)
// are returned as rest. What emit charged for the joined blocks is given
// back: the text is charged again with the block it becomes.
func (r *reader) flatten(blocks []gofastcore.Block) (text string, spans []gofastcore.Span, all gofastcore.Hidden, rest []gofastcore.Block) {
	var buf []byte
	var collected []gofastcore.Span
	for i := range blocks {
		b := &blocks[i]
		var line string
		var lineSpans []gofastcore.Span
		var lineWhole gofastcore.Hidden
		switch {
		case b.Table != nil:
			line = tableText(b.Table)
			lineWhole = b.Hidden
		case b.Image != nil, b.Kind == gofastcore.KindNote, b.Kind == gofastcore.KindFootnote:
			rest = append(rest, *b)
			continue
		default:
			r.budget.Return(cost(b))
			line = b.Text
			lineSpans = b.Spans
			lineWhole = b.HiddenWhole()
			if b.Marker != "" {
				prefix := b.Marker + " "
				line = prefix + line
				shifted := make([]gofastcore.Span, len(lineSpans))
				for k, sp := range lineSpans {
					sp.Start += len(prefix)
					sp.End += len(prefix)
					shifted[k] = sp
				}
				lineSpans = shifted
			}
		}
		if line == "" {
			continue
		}
		if len(buf) > 0 {
			buf = append(buf, '\n')
		}
		off := len(buf)
		buf = append(buf, line...)
		if lineWhole != 0 {
			collected = append(collected, gofastcore.Span{Start: off, End: len(buf), Hidden: lineWhole})
		}
		for _, sp := range lineSpans {
			sp.Start += off
			sp.End += off
			collected = append(collected, sp)
		}
	}
	if len(buf) == 0 {
		return "", nil, 0, rest
	}
	sortSpans(collected)
	spans, all = settle(splitSpans(collected), 0, len(buf), buf)
	return string(buf), spans, all, rest
}

// splitSpans makes spans that overlap (a line's whole-block reasons over
// the spans inside it) into spans that do not, each with the reasons of
// all that cover it.
func splitSpans(in []gofastcore.Span) []gofastcore.Span {
	if len(in) < 2 {
		return in
	}
	var cuts []int
	for _, sp := range in {
		cuts = append(cuts, sp.Start, sp.End)
	}
	sortInts(cuts)
	var out []gofastcore.Span
	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		if a == b {
			continue
		}
		var piece gofastcore.Span
		found := false
		for _, sp := range in {
			if sp.Start <= a && sp.End >= b {
				piece.Hidden |= sp.Hidden
				piece.Style |= sp.Style
				if sp.Href != "" {
					piece.Href = sp.Href
				}
				found = true
			}
		}
		if !found {
			continue
		}
		piece.Start, piece.End = a, b
		if n := len(out); n > 0 && out[n-1].End == a && out[n-1].Hidden == piece.Hidden && out[n-1].Style == piece.Style && out[n-1].Href == piece.Href {
			out[n-1].End = b
			continue
		}
		out = append(out, piece)
	}
	return out
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func sortSpans(a []gofastcore.Span) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].Start < a[j-1].Start; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// tableText writes a table as lines of tab-separated cells.
func tableText(t *gofastcore.Table) string {
	var sb strings.Builder
	for i, row := range t.Rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.Join(row, "\t"))
	}
	return strings.TrimSpace(sb.String())
}

// note reads text:note: a footnote after the paragraph, an endnote at the
// end of the document. The citation is the label.
func (r *reader) note(p *para, ru run) {
	s := &r.s
	class := r.attr(ns.Text, "note-class")
	var marker string
	var body []gofastcore.Block
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start || s.Space() != ns.Text {
			continue
		}
		switch string(s.Local()) {
		case "note-citation":
			label := r.attr(ns.Text, "label")
			v := s.AppendText(nil)
			if label == "" {
				label = string(v)
			}
			marker, _ = clean.String([]byte(label), clean.Prose)
			marker = clean.Label(marker)
		case "note-body":
			r.blocks(ctxt{bg: ru.bg, hidden: reasons(&ru) & gofastcore.HiddenInvisible}, &body)
		}
	}
	text, spans, all, rest := r.flatten(body)
	dst := &p.after
	if class == "endnote" {
		dst = &r.endnotes
	}
	if text != "" {
		b := gofastcore.Block{Kind: gofastcore.KindFootnote, Marker: marker, Text: text, Spans: spans, Hidden: all}
		if !r.emit(dst, b) {
			return
		}
	}
	*dst = append(*dst, rest...) // charged when they were read
}

// annotation reads office:annotation: a comment, kept as a note.
func (r *reader) annotation(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	var author, date string
	var body []gofastcore.Block
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start {
			continue
		}
		switch {
		case s.Space() == ns.DC && string(s.Local()) == "creator":
			author = clean.Name(string(s.AppendText(nil)))
		case s.Space() == ns.DC && string(s.Local()) == "date":
			date = clean.Label(string(s.AppendText(nil)))
		case s.Space() == ns.Meta && string(s.Local()) == "date-string":
			if date == "" {
				date = clean.Label(string(s.AppendText(nil)))
			} else {
				s.Skip()
			}
		default:
			r.blockAt(ctxt{hidden: c.hidden}, &body)
		}
	}
	text, spans, all, rest := r.flatten(body)
	if text != "" {
		attrs := map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}
		if author != "" {
			attrs[gofastcore.AttrAuthor] = author
		}
		if date != "" {
			attrs[gofastcore.AttrDate] = date
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Spans: spans, Hidden: all, Attrs: attrs}
		whole(&b, c.hidden)
		if !r.emit(dst, b) {
			return
		}
	}
	*dst = append(*dst, rest...) // charged when they were read
}

// trackedChanges reads text:tracked-changes, which keeps what each change
// removed. The body holds the text as if the changes were accepted.
func (r *reader) trackedChanges() {
	s := &r.s
	depth := s.Depth()
	var id string
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		switch {
		case s.Space() == ns.Text && string(s.Local()) == "changed-region":
			r.tracked = true
			id = r.attr(ns.Text, "id")
			if id == "" {
				id = r.attr(xmlscan.SpaceXML, "id")
			}
		case s.Space() == ns.Text && string(s.Local()) == "deletion":
			d := &deletion{order: len(r.deletions)}
			r.readDeletion(d)
			if _, dup := r.deletions[id]; !dup {
				r.deletions[id] = d
			}
		case s.Space() == ns.Text && (string(s.Local()) == "insertion" || string(s.Local()) == "format-change"):
			s.Skip()
		}
	}
}

func (r *reader) readDeletion(d *deletion) {
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
		if s.Space() == ns.Office && string(s.Local()) == "change-info" {
			cd := s.Depth()
			for {
				k := s.Next()
				if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < cd {
					break
				}
				if k != xmlscan.Start {
					continue
				}
				switch {
				case s.Space() == ns.DC && string(s.Local()) == "creator":
					d.author = clean.Name(string(s.AppendText(nil)))
				case s.Space() == ns.DC && string(s.Local()) == "date":
					d.date = clean.Label(string(s.AppendText(nil)))
				default:
					s.Skip()
				}
			}
			continue
		}
		r.blockAt(ctxt{}, &d.blocks)
	}
}

// useDeletion places the note for a deletion where the body marks it.
func (r *reader) useDeletion(id string, dst *[]gofastcore.Block) {
	d, ok := r.deletions[id]
	if !ok || r.usedDel[id] {
		return
	}
	r.usedDel[id] = true
	text, spans, all, rest := r.flatten(d.blocks)
	d.blocks = nil
	if text != "" {
		attrs := map[string]string{gofastcore.AttrNoteType: gofastcore.NoteDeletion}
		if d.author != "" {
			attrs[gofastcore.AttrAuthor] = d.author
		}
		if d.date != "" {
			attrs[gofastcore.AttrDate] = d.date
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Spans: spans, Hidden: all, Attrs: attrs}
		if !r.emit(dst, b) {
			return
		}
	}
	*dst = append(*dst, rest...) // charged when they were read
}

// deletionOrder lists the deletions not yet placed, in document order.
func (r *reader) deletionOrder() []string {
	var ids []string
	for id := range r.deletions {
		if !r.usedDel[id] {
			ids = append(ids, id)
		}
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && r.deletions[ids[j]].order < r.deletions[ids[j-1]].order; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}
