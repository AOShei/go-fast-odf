// Package odt reads the body of an OpenDocument document into parts and
// blocks: a text document (office:text) as one body part of headings,
// paragraphs, lists, tables, frames and text boxes in reading order,
// images, notes, comments, tracked deletions, and the headers and footers
// of its master pages; a spreadsheet (office:spreadsheet) as one part per
// sheet, its used regions as tables; a presentation (office:presentation)
// as one part per slide, with the text of its master page and its speaker
// notes. Embedded formulas and charts become blocks, form controls
// paragraphs, and other embedded documents children. Values the document
// keeps and never shows go to the stored part.
//
// The streams are read once, front to back, with the streaming scanner.
// Every block is charged to the text budget as it is made, and a block
// that does not fit stops the reading there.
package odt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/odpkg"
	"github.com/AOShei/go-fast-odf/internal/style"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// Shared warning codes this package raises, by short names.
const (
	WarnUnreadContent = gofastcore.WarnInputUnreadContent
	WarnPartDamaged   = gofastcore.WarnInputStreamDamaged
)

// Config holds the settings of the public Extractor that concern text
// documents.
type Config struct {
	// MaxCells bounds the table cells kept from the document; negative
	// means no limit.
	MaxCells int64
	// Lost is what bounds left out of the metadata and manifest, said
	// on the document.
	Lost Lost
}

// Result is what reading the document gave.
type Result struct {
	// Kind is the body element's local name: "text", "spreadsheet",
	// "presentation", or another this package does not read (Parts is
	// then empty), or "" if the stream has no body.
	Kind string
	// Failed: content.xml could not be read as far as its body (it is
	// damaged, cut short or not XML), so what kind of document it holds
	// is not known.
	Failed bool
	// Parts are the body of a text document, the sheets of a spreadsheet,
	// the slides of a presentation. Stored is the stored part, or nil.
	Parts  []gofastcore.Part
	Stored *gofastcore.Part
	// Children are the embedded documents handed to Options.Child.
	Children []*gofastcore.Document
	// Warnings concern the document as a whole.
	Warnings []gofastcore.Warning
	// ActiveContent counts scripts, macro links and event handlers.
	ActiveContent int
}

var errStop = errors.New("odt: reading stopped")

// ctxt is what a block inherits from where it stands.
type ctxt struct {
	hidden gofastcore.Display // reasons the file declares for all of it
	list   *listCtx           // the list it is in, if any
}

type reader struct {
	ctx    context.Context
	pkg    *odpkg.Package
	set    *style.Set
	opts   *gofastcore.Options
	budget *gofastcore.TextBudget
	s      xmlscan.Scanner
	sc     style.Scope

	stopped  bool
	limitMsg string
	removed  int
	clamped  int
	tracked  bool
	failed   error

	maxCells int64
	cells    int64

	hits []capHit // what fixed bounds left out of the part being read
	// heldHits, while a presentation is read, keeps what bounds left out of
	// each slide (by number) until its master's content is added, so
	// that both are said in one warning per bound.
	heldHits map[int][]capHit

	deletions map[string]*deletion
	usedDel   map[string]bool
	endnotes  []gofastcore.Block
	userVars  []gofastcore.Block

	lists    listState
	outline  counters
	images   map[string]imageInfo
	dataSent map[string]bool

	external    int
	active      int
	unread      map[string]int
	unreadOrder []string
	validations map[string]validation
	dbRanges    []dbRange // database ranges with a header row
	delOrder    []string  // deletion ids in document order

	seq int // counts block-level elements

	cur  *para // the paragraph being read, if any
	held int   // bytes held by the paragraphs cur stands in

	seenObjects  map[string]bool
	children     []*gofastcore.Document
	controls     map[string]*control
	controlOrder []string
	tick         int
}

// Read reads content.xml (and styles.xml for headers and footers).
func Read(ctx context.Context, pkg *odpkg.Package, set *style.Set, opts *gofastcore.Options, cfg Config) (*Result, error) {
	r := &reader{
		ctx: ctx, pkg: pkg, set: set, opts: opts,
		budget:    gofastcore.TextBudgetFrom(ctx),
		maxCells:  cfg.MaxCells,
		deletions: map[string]*deletion{},
		usedDel:   map[string]bool{},
		images:    map[string]imageInfo{},
		dataSent:  map[string]bool{},
		unread:    map[string]int{},

		seenObjects: map[string]bool{},
		controls:    map[string]*control{},
	}
	r.lists.init()
	res := &Result{}

	c, err := pkg.Scan("content.xml", &r.s)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	r.sc = style.Content
	serr := set.Read(&r.s, style.Content)
	if serr == nil {
		for res.Kind == "" {
			k := r.s.Next()
			if k == xmlscan.EOF {
				break
			}
			if k == xmlscan.Start && r.s.Depth() == 3 && r.s.Space() == ns.Office {
				res.Kind = clean.Label(string(r.s.Local()))
			}
		}
	}
	res.Failed = res.Kind == "" && (serr != nil || r.s.Err() != nil)
	switch res.Kind {
	case "spreadsheet":
		res.Parts = r.spreadsheet(serr)
	case "presentation":
		res.Parts = r.presentation(serr)
	case "text", "":
		res.Parts = []gofastcore.Part{r.textBody(serr)}
	default:
		return res, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.removed > 0 {
		msg := fmt.Sprintf("%d control characters with no meaning as text were removed from the text", r.removed)
		if r.removed == 1 {
			msg = "1 control character with no meaning as text was removed from the text"
		}
		res.Warnings = append(res.Warnings, gofastcore.Warning{Code: gofastcore.WarnTextControlCharactersRemoved, Severity: gofastcore.SeverityInfo,
			Message: msg})
	}
	r.unplacedControls()
	// What the metadata and manifest lost, with what the body lost that
	// belongs to no part: one warning per bound.
	r.lost(capMetaValue, cfg.Lost.MetaValues)
	r.lost(capKeywords, cfg.Lost.Keywords)
	r.lost(capUserDefined, cfg.Lost.UserDefined)
	r.lost(capLabel, cfg.Lost.Labels)
	r.lost(capAddress, cfg.Lost.Addresses)
	r.sayLost(nil, &res.Warnings)
	res.Children = r.children
	if len(r.userVars) > 0 {
		// What the file keeps and shows nowhere, with no reason: it is
		// where it came from that this part records.
		st := &gofastcore.Part{Kind: gofastcore.PartStored, Number: len(res.Parts) + 1, Blocks: r.userVars}
		st.Finish()
		res.Stored = st
	}
	res.ActiveContent = r.active
	if r.external > 0 {
		res.Warnings = append(res.Warnings, gofastcore.Warning{Code: gofastcore.WarnInputExternalReference, Severity: gofastcore.SeverityInfo,
			Message: fmt.Sprintf("%d references to files or addresses outside the document (linked pictures, linked sections); nothing was fetched", r.external)})
	}
	for _, what := range r.unreadOrder {
		res.Warnings = append(res.Warnings, gofastcore.Warning{Code: WarnUnreadContent, Severity: gofastcore.SeverityWarning,
			Message: fmt.Sprintf("%d %s not read", r.unread[what], what)})
	}
	return res, nil
}

// addUnread counts content of a kind that is not read. The kinds are
// kept in the order met, not in a map's order, so that the output and
// the code paths taken do not vary from run to run.
func (r *reader) addUnread(what string) {
	if r.unread[what] == 0 {
		r.unreadOrder = append(r.unreadOrder, what)
	}
	r.unread[what]++
}

// textBody reads the body of a text document, whose office:text element
// is current, into its one part, with the headers and footers of its
// master pages.
func (r *reader) textBody(serr error) gofastcore.Part {
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: 1, Blocks: []gofastcore.Block{}}
	var body []gofastcore.Block
	if serr == nil && r.s.Depth() == 3 {
		r.blocks(ctxt{}, &body)
		serr = r.s.Err()
	}
	r.noteFailure(&part, "content.xml", serr)
	// Deletions nothing in the body points at are kept, at the end.
	if !r.stopped {
		for _, id := range r.deletionOrder() {
			r.useDeletion(id, &body)
		}
	}
	// The headers and footers are in another stream (or further on in a
	// flat file): read with a scanner of their own.
	var headers, footers []gofastcore.Block
	if !r.stopped && r.ctx.Err() == nil {
		saved := r.s
		r.s = xmlscan.Scanner{}
		headers, footers = r.masterPages(&part)
		r.s = saved
	}
	part.Blocks = append(part.Blocks, headers...)
	part.Blocks = append(part.Blocks, body...)
	part.Blocks = append(part.Blocks, r.endnotes...)
	part.Blocks = append(part.Blocks, footers...)
	r.finish(&part, "document body")
	return part
}

// finish adds the warnings a part's reading gave rise to and finishes it
// (gofastcore.Part.Finish).
func (r *reader) finish(part *gofastcore.Part, what string) {
	if r.stopped && r.limitMsg != "" {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, r.limitMsg)
		r.limitMsg = "" // said once, on the part where it happened
	}
	if r.tracked {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextTrackedChanges, "the document has tracked changes; its text is given as if all were accepted, and deleted text is kept in notes")
		r.tracked = false
	}
	if r.clamped > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnHeadingLevelClamped, fmt.Sprintf("%d headings deeper than level 6", r.clamped))
		r.clamped = 0
	}
	held := false
	if r.heldHits != nil {
		if own := r.takeHits(); len(own) > 0 {
			r.heldHits[part.Number] = own
			held = true
		}
	} else {
		r.sayLost(part, nil)
	}
	if len(part.Blocks) == 0 && len(part.Warnings) == 0 && !held {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "the "+what+" has no text and no images")
	}
	part.Finish()
}

// noteFailure turns the error that ended a stream into a warning.
func (r *reader) noteFailure(part *gofastcore.Part, name string, err error) {
	switch {
	case err == nil, errors.Is(err, errStop):
	case errors.Is(err, odpkg.ErrBudget):
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading "+name+"; the document is cut off there")
		r.stopped = true
	default:
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, clean.Label(name+" could not be read to its end: "+err.Error()))
	}
}

// stop ends the reading because a limit was reached.
func (r *reader) stop(msg string) {
	if !r.stopped {
		r.stopped = true
		r.limitMsg = msg
	}
	r.s.Abort(errStop)
}

const textCut = "the limit on the amount of text was reached; the document is cut off there"

// emit charges a block to the budget and appends it. It reports false,
// and stops the reading, if the block does not fit.
func (r *reader) emit(dst *[]gofastcore.Block, b gofastcore.Block) bool {
	if r.stopped {
		return false
	}
	if !r.budget.Take(gofastcore.BlockCost + len(b.Text) + len(b.Marker)) {
		r.stop(textCut)
		return false
	}
	*dst = append(*dst, b)
	return true
}

// cost is what emit charged for a block.
func cost(b *gofastcore.Block) int64 {
	return int64(gofastcore.BlockCost + len(b.Text) + len(b.Marker))
}

// alive checks for cancellation now and then.
func (r *reader) alive() bool {
	if r.stopped {
		return false
	}
	r.tick++
	if r.tick&255 == 0 && r.ctx.Err() != nil {
		r.stopped = true
		r.s.Abort(errStop)
		return false
	}
	return true
}

func (r *reader) attr(space xmlscan.Space, local string) string {
	v, _ := r.s.Attr(space, local)
	return string(v)
}

// skipped are elements at block level that hold no content to read here.
var skipped = map[string]bool{
	"sequence-decls": true, "variable-decls": true, "dde-connection-decls": true,
	"soft-page-break": true, "bookmark": true, "bookmark-start": true, "bookmark-end": true,
	"alphabetical-index-auto-mark-file": true, "change-start": true, "change-end": true,
}

// blocks reads the children of the current element as block-level
// content, until the element ends.
func (r *reader) blocks(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		r.seq++
		space, local := s.Space(), string(s.Local())
		switch space {
		case ns.Text:
			switch {
			case local == "p":
				r.paragraph(c, false, nil, dst)
			case local == "h":
				r.paragraph(c, true, nil, dst)
			case local == "list":
				r.list(c, dst)
			case local == "numbered-paragraph":
				r.numberedParagraph(c, dst)
			case local == "section":
				r.section(c, dst)
			case local == "tracked-changes":
				r.trackedChanges()
			case local == "user-field-decls":
				r.userFields()
			case local == "change":
				r.useDeletion(r.attr(ns.Text, "change-id"), dst)
			case skipped[local], strings.HasSuffix(local, "-source"):
				s.Skip()
			default:
				// Indexes, their bodies and titles, and wrappers this
				// reader does not know: their content is read.
				r.blocks(c, dst)
			}
		case ns.Table:
			if local == "table" {
				r.table(c, dst)
			} else {
				r.blocks(c, dst)
			}
		case ns.Draw:
			r.drawing(c, dst)
		case ns.Office:
			switch local {
			case "forms":
				r.readForms()
			case "event-listeners", "scripts":
				if style.HasChildren(s) {
					r.active++
				}
			case "annotation":
				r.annotation(c, dst)
			default:
				r.blocks(c, dst)
			}
		case ns.Script:
			r.active++
			s.Skip()
		default:
			r.blocks(c, dst)
		}
	}
}

// section reads text:section.
func (r *reader) section(c ctxt, dst *[]gofastcore.Block) {
	switch r.attr(ns.Text, "display") {
	case "none":
		c.hidden |= gofastcore.DisplayHidden
	case "condition":
		// Hidden when its condition holds. Conditions are never
		// evaluated: the section is reported hidden only where the file
		// records it so (text:is-hidden="true").
		if r.attr(ns.Text, "is-hidden") == "true" {
			c.hidden |= gofastcore.DisplayHidden
		}
	}
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
		if s.Space() == ns.Text && (string(s.Local()) == "section-source" || string(s.Local()) == "dde-source") {
			if href := r.attr(ns.XLink, "href"); href != "" || string(s.Local()) == "dde-source" {
				r.external++
			}
			s.Skip()
			continue
		}
		// Hand the rest to the block reader: it starts at this element.
		r.blockAt(c, dst)
		r.blocks(c, dst)
		return
	}
}

// blockAt reads the element whose Start is current as block content.
func (r *reader) blockAt(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	r.seq++
	space, local := s.Space(), string(s.Local())
	switch {
	case space == ns.Text && local == "p":
		r.paragraph(c, false, nil, dst)
	case space == ns.Text && local == "h":
		r.paragraph(c, true, nil, dst)
	case space == ns.Text && local == "list":
		r.list(c, dst)
	case space == ns.Text && local == "section":
		r.section(c, dst)
	case space == ns.Table && local == "table":
		r.table(c, dst)
	case space == ns.Draw:
		r.drawing(c, dst)
	case space == ns.Office && local == "annotation":
		r.annotation(c, dst)
	case space == ns.Text && (skipped[local] || strings.HasSuffix(local, "-source")):
		s.Skip()
	default:
		r.blocks(c, dst)
	}
}

// userFields reads text:user-field-decls: values the document stores for
// its fields, kept in the stored part.
func (r *reader) userFields() {
	s := &r.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start || s.Space() != ns.Text || string(s.Local()) != "user-field-decl" {
			continue
		}
		name := r.name(r.attr(ns.Text, "name"))
		value := ""
		for _, a := range []string{"string-value", "value", "date-value", "time-value", "boolean-value"} {
			if v, ok := s.Attr(ns.Office, a); ok {
				value, _ = clean.String(v, clean.Prose)
				break
			}
		}
		text := name + ": " + value
		switch {
		case value == "" && name == "":
			continue
		case value == "":
			text = name
		case name == "":
			text = value
		}
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text,
			Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredDocumentVariable}}
		if name != "" {
			b.Attrs[gofastcore.AttrField] = name
		}
		if !r.emit(&r.userVars, b) {
			return
		}
	}
}

// ---- paragraphs ----

// run is the formatting in force for a stretch of text.
type run struct {
	eff    style.Resolved // the style, with what it declares about display
	inline gofastcore.Style
	href   string
	hidden gofastcore.Display // reasons declared by fields (hidden text) and containers
}

type para struct {
	buf       []byte
	spans     []gofastcore.Span
	after     []gofastcore.Block
	space     bool // the last thing written was white space, or nothing
	marker    string
	hasMarker bool
	markerCut bool               // the producer's label was cut at the length of a label
	hidden    gofastcore.Display // the paragraph is hidden (text:hidden-paragraph)
	cut       bool
}

// paragraph reads text:p or text:h. item, if set, is the list item it
// opens.
func (r *reader) paragraph(c ctxt, heading bool, item *itemCtx, dst *[]gofastcore.Block) {
	s := &r.s
	styleName := r.attr(ns.Text, "style-name")
	ps := r.set.Para(r.sc, styleName)
	var cuts int64     // of the anchor, said if the paragraph is given
	markerCut := false // of the label given, said likewise
	anchor := cutLabel(r.attr(xmlscan.SpaceXML, "id"), &cuts)
	level := 0
	isHeader := false
	if heading {
		if n, err := strconv.Atoi(strings.TrimSpace(r.attr(ns.Text, "outline-level"))); err == nil && n > 0 {
			level = n
		} else if ps.Outline > 0 {
			level = ps.Outline
		} else {
			level = 1
		}
		level = min(level, 1<<10)
		isHeader = r.attr(ns.Text, "is-list-header") == "true"
		if r.attr(ns.Text, "restart-numbering") == "true" {
			start := 1
			if n, err := strconv.Atoi(strings.TrimSpace(r.attr(ns.Text, "start-value"))); err == nil && n >= 0 {
				start = n
			}
			r.outline.restart(level, start)
		}
		if anchor == "" {
			anchor = cutLabel(r.attr(ns.Text, "id"), &cuts)
		}
	}
	p := &para{space: true}
	// A paragraph in a paragraph (a text box anchored in it, a note) has
	// a buffer of its own while the outer one is held: both count.
	outer, held := r.cur, r.held
	if outer != nil {
		r.held += len(outer.buf)
	}
	r.cur = p
	r.inline(p, run{eff: ps}, s.Depth())
	r.cur, r.held = outer, held

	from, to := trimBounds(p.buf)
	// White space trimmed off was written all the same (text:s can ask
	// for much of it in few bytes), so it is charged too.
	if waste := len(p.buf) - max(to-from, 0); waste > 0 && !r.stopped && !r.budget.Take(waste) {
		r.stop(textCut)
	}
	b := gofastcore.Block{Kind: gofastcore.KindParagraph, Loc: gofastcore.Location{Anchor: anchor}}
	if from < to {
		b.Text = string(p.buf[from:to])
		spans, all := settle(p.spans, from, to, p.buf)
		b.Spans, b.Display = spans, all
	}
	whole(&b, c.hidden|p.hidden)

	switch {
	case heading:
		b.Kind = gofastcore.KindHeading
		if level > 6 {
			b.Attrs = map[string]string{gofastcore.AttrLevel: strconv.Itoa(level)}
			r.clamped++
		}
		b.Level = uint8(min(level, 6))
		switch {
		case item != nil:
			b.Marker, b.Ordered = item.marker()
			markerCut = item.cut
		case !isHeader:
			b.Marker, markerCut = r.outlineMarker(level)
		}
		if p.hasMarker {
			b.Marker, markerCut = p.marker, p.markerCut // as the producer numbered it
		}
	case item != nil:
		b.Kind = gofastcore.KindListItem
		b.Level = uint8(min(item.depth, 255))
		if p.hasMarker {
			b.Marker, markerCut = p.marker, p.markerCut
			_, b.Ordered = item.marker()
		} else {
			b.Marker, b.Ordered = item.marker()
			markerCut = item.cut
		}
	case c.list != nil:
		// A further paragraph of a list item: part of the item, with no
		// label of its own.
		b.Kind = gofastcore.KindListItem
		b.Level = uint8(min(c.list.depth, 255))
		b.Ordered = c.list.ordered()
	}
	if !heading {
		r.thematicBreak(ps, b.Text == "" && len(p.after) == 0, dst)
	}
	if b.Text != "" {
		if !r.emit(dst, b) {
			return
		}
		if markerCut {
			cuts++
		}
		r.lost(capLabel, cuts)
	}
	// What stands after the paragraph was charged when it was read.
	*dst = append(*dst, p.after...)
	if p.cut {
		r.stop(textCut)
	}
}

// thematicBreak emits a break where the file declares one. ODF has no
// element for a rule; LibreOffice writes an HTML <hr> as an empty
// paragraph in its built-in style "Horizontal Line", so an empty paragraph
// in that style (or one inheriting from it) is a native break. An empty
// paragraph with only a bottom border is a border, not a declared break.
func (r *reader) thematicBreak(ps style.Resolved, empty bool, dst *[]gofastcore.Block) {
	if empty && ps.HorizontalLine {
		r.emit(dst, gofastcore.Block{Kind: gofastcore.KindThematicBreak, Origin: gofastcore.OriginNative})
	}
}

// whole adds reasons that apply to all of a block, taking them off its
// spans, where they would say they apply only there.
func whole(b *gofastcore.Block, h gofastcore.Display) {
	if h == 0 || b.Text == "" && b.Table == nil {
		return
	}
	b.Display |= h
	kept := b.Spans[:0]
	for _, sp := range b.Spans {
		sp.Display &^= h
		if sp.Display != 0 || sp.Style != 0 || sp.Href != "" {
			kept = append(kept, sp)
		}
	}
	if len(kept) == 0 {
		kept = nil
	}
	b.Spans = kept
	if b.Table != nil {
		regions := b.Table.Display[:0]
		for _, reg := range b.Table.Display {
			reg.Display &^= h
			if reg.Display != 0 {
				regions = append(regions, reg)
			}
		}
		if len(regions) == 0 {
			regions = nil
		}
		b.Table.Display = regions
	}
}

func trimBounds(b []byte) (int, int) {
	from, to := 0, len(b)
	for from < to {
		r, n := decodeRune(b[from:])
		if !unicode.IsSpace(r) {
			break
		}
		from += n
	}
	for to > from {
		r, n := decodeLastRune(b[from:to])
		if !unicode.IsSpace(r) {
			break
		}
		to -= n
	}
	return from, to
}
