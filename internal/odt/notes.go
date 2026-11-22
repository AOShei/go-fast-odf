package odt

import (
	"slices"
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
	cuts         int64 // of author and date, at the length of a label
	blocks       []gofastcore.Block
}

// flatten joins the text blocks read from a note, comment, deletion or
// table cell into one text, one line per block, with their spans and
// hidden reasons. Blocks that are not text (pictures, notes inside them)
// are returned as rest. What emit charged for the joined blocks is given
// back: the text is charged again with the block it becomes. With
// keepBreaks (footnotes and endnotes, which are part of the document's
// text), thematic breaks are returned in rest, to stand after the block.
// Without it they are dropped, as the empty paragraphs they were written
// as: a table cell's text is all it has, and a break inside a comment,
// a speaker's note or a tracked deletion is not a break in the document.
func (r *reader) flatten(blocks []gofastcore.Block, keepBreaks bool) (text string, spans []gofastcore.Span, all gofastcore.Display, rest []gofastcore.Block) {
	var buf []byte
	var collected []gofastcore.Span
	for i := range blocks {
		b := &blocks[i]
		var line string
		var lineSpans []gofastcore.Span
		var lineWhole gofastcore.Display
		switch {
		case b.Table != nil:
			line = tableText(b.Table)
			lineWhole = b.Display
		case b.Image != nil, b.Kind == gofastcore.KindNote, b.Kind == gofastcore.KindFootnote,
			b.Kind == gofastcore.KindThematicBreak && keepBreaks:
			rest = append(rest, *b)
			continue
		default:
			r.budget.Return(cost(b))
			line = b.Text
			lineSpans = b.Spans
			lineWhole = b.DisplayWhole()
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
		// The line's own spans do not overlap; reasons that cover the
		// whole line go on them and on the gaps between them.
		at := off
		gap := func(end int) {
			if lineWhole != 0 && end > at {
				collected = append(collected, gofastcore.Span{Start: at, End: end, Display: lineWhole})
			}
		}
		sortSpans(lineSpans)
		for _, sp := range lineSpans {
			sp.Start += off
			sp.End += off
			if sp.Start < at || sp.End <= sp.Start {
				continue
			}
			gap(sp.Start)
			sp.Display |= lineWhole
			collected = append(collected, sp)
			at = sp.End
		}
		gap(len(buf))
	}
	if len(buf) == 0 {
		return "", nil, 0, rest
	}
	spans, all = settle(collected, 0, len(buf), buf)
	return string(buf), spans, all, rest
}

func sortSpans(a []gofastcore.Span) {
	slices.SortStableFunc(a, func(x, y gofastcore.Span) int { return x.Start - y.Start })
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
	markerCut := false
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
			marker, markerCut = clean.LabelCut(marker)
		case "note-body":
			r.blocks(ctxt{hidden: reasons(&ru) & gofastcore.DisplayHidden}, &body)
		}
	}
	text, spans, all, rest := r.flatten(body, true)
	if markerCut && text != "" {
		r.lost(capLabel, 1)
	}
	dst := &p.after
	if class == "endnote" {
		dst = &r.endnotes
	}
	if text != "" {
		b := gofastcore.Block{Kind: gofastcore.KindFootnote, Marker: marker, Text: text, Spans: spans, Display: all}
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
	var cuts int64 // of author and date, said if the comment is given
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
			author = cutName(string(s.AppendText(nil)), &cuts)
		case s.Space() == ns.DC && string(s.Local()) == "date":
			date = cutLabel(string(s.AppendText(nil)), &cuts)
		case s.Space() == ns.Meta && string(s.Local()) == "date-string":
			if date == "" {
				date = cutLabel(string(s.AppendText(nil)), &cuts)
			} else {
				s.Skip()
			}
		default:
			r.blockAt(ctxt{hidden: c.hidden}, &body)
		}
	}
	text, spans, all, rest := r.flatten(body, false)
	if text != "" {
		attrs := map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}
		r.lost(capLabel, cuts)
		if author != "" {
			attrs[gofastcore.AttrAuthor] = author
		}
		if date != "" {
			attrs[gofastcore.AttrDate] = date
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Spans: spans, Display: all, Attrs: attrs}
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
				r.delOrder = append(r.delOrder, id)
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
					d.author = cutName(string(s.AppendText(nil)), &d.cuts)
				case s.Space() == ns.DC && string(s.Local()) == "date":
					d.date = cutLabel(string(s.AppendText(nil)), &d.cuts)
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
	text, spans, all, rest := r.flatten(d.blocks, false)
	d.blocks = nil
	if text != "" {
		attrs := map[string]string{gofastcore.AttrNoteType: gofastcore.NoteDeletion}
		r.lost(capLabel, d.cuts)
		if d.author != "" {
			attrs[gofastcore.AttrAuthor] = d.author
		}
		if d.date != "" {
			attrs[gofastcore.AttrDate] = d.date
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Spans: spans, Display: all, Attrs: attrs}
		if !r.emit(dst, b) {
			return
		}
	}
	*dst = append(*dst, rest...) // charged when they were read
}

// deletionOrder lists the deletions not yet placed, in document order.
func (r *reader) deletionOrder() []string {
	var ids []string
	for _, id := range r.delOrder {
		if !r.usedDel[id] {
			ids = append(ids, id)
		}
	}
	return ids
}
