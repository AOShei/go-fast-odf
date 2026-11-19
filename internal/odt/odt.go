// Package odt reads the body of an OpenDocument text document
// (office:text) into blocks: headings, paragraphs, lists, tables, frames
// and text boxes in reading order, images, notes, comments, tracked
// deletions, and the headers and footers of its master pages.
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

// Warning codes this package raises beyond the shared ones. The public
// package documents them.
const (
	WarnUnreadContent = "odf.unread_content"
	WarnPartDamaged   = "odf.part_damaged"
)

// HiddenChecked is what the reader checks completely.
const HiddenChecked = gofastcore.HiddenInvisible | gofastcore.HiddenTiny | gofastcore.HiddenLowContrast | gofastcore.HiddenChars

// Config holds the settings of the public Extractor that concern text
// documents.
type Config struct {
	// MaxCells bounds the table cells kept from the document; negative
	// means no limit.
	MaxCells int64
}

// Result is what reading the document gave.
type Result struct {
	// Kind is the body element's local name: "text", "spreadsheet",
	// "presentation", or another this package does not read (Parts is
	// then empty), or "" if the stream has no body.
	Kind string
	// Parts are the body of a text document, the sheets of a spreadsheet,
	// the slides of a presentation. Stored is the stored part, or nil.
	Parts  []gofastcore.Part
	Stored *gofastcore.Part
	// Warnings concern the document as a whole.
	Warnings []gofastcore.Warning
	// ActiveContent counts scripts, macro links and event handlers.
	ActiveContent int
}

var errStop = errors.New("odt: reading stopped")

// ctxt is what a block inherits from where it stands.
type ctxt struct {
	hidden gofastcore.Hidden // reasons that apply to all of it
	bg     style.Color       // what is behind it
	list   *listCtx          // the list it is in, if any
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
	delOrder    []string // deletion ids in document order
	tick        int
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
		res.Warnings = append(res.Warnings, gofastcore.Warning{Code: "odf.control_characters_removed", Severity: gofastcore.SeverityInfo,
			Message: fmt.Sprintf("%d control characters removed from the text", r.removed)})
	}
	if len(r.userVars) > 0 {
		st := &gofastcore.Part{Kind: gofastcore.PartStored, Number: len(res.Parts) + 1, Hidden: gofastcore.HiddenInvisible, Blocks: r.userVars}
		st.MarkHidden()
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
		r.blocks(ctxt{bg: r.set.PageBackground("")}, &body)
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

// finish adds the warnings a part's reading gave rise to and marks its
// hidden text.
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
	if len(part.Blocks) == 0 && len(part.Warnings) == 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartNoText, "the "+what+" has no text and no images")
	}
	part.MarkHidden()
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
				if r.hasForms() {
					r.addUnread("sets of form controls")
				}
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

// hasForms reads past office:forms and reports whether it held a form.
func (r *reader) hasForms() bool {
	s := &r.s
	depth := s.Depth()
	found := false
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return found
		}
		if k == xmlscan.Start && s.Space() == ns.Form && string(s.Local()) == "form" {
			found = true
		}
	}
}

// section reads text:section.
func (r *reader) section(c ctxt, dst *[]gofastcore.Block) {
	switch r.attr(ns.Text, "display") {
	case "none":
		c.hidden |= gofastcore.HiddenInvisible
	case "condition":
		// Shown or not as a condition decides; conditions are not
		// evaluated, so the section is taken as one a reader may not see.
		c.hidden |= gofastcore.HiddenInvisible
	}
	if bg := r.set.Box(r.sc, "section", r.attr(ns.Text, "style-name")).Background; bg.Known() {
		c.bg = bg
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
		name := clean.Name(r.attr(ns.Text, "name"))
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
	eff    style.Resolved // what decides visibility
	inline gofastcore.Style
	href   string
	hidden gofastcore.Hidden // reasons from fields (hidden text)
	bg     style.Color
}

type para struct {
	buf       []byte
	spans     []gofastcore.Span
	after     []gofastcore.Block
	space     bool // the last thing written was white space, or nothing
	marker    string
	hasMarker bool
	hidden    gofastcore.Hidden // the paragraph is hidden (text:hidden-paragraph)
	cut       bool
}

// paragraph reads text:p or text:h. item, if set, is the list item it
// opens.
func (r *reader) paragraph(c ctxt, heading bool, item *itemCtx, dst *[]gofastcore.Block) {
	s := &r.s
	styleName := r.attr(ns.Text, "style-name")
	ps := r.set.Para(r.sc, styleName)
	anchor := clean.Label(r.attr(xmlscan.SpaceXML, "id"))
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
			anchor = clean.Label(r.attr(ns.Text, "id"))
		}
	}
	bg := c.bg
	if ps.Background.Known() {
		bg = ps.Background
	}
	p := &para{space: true}
	r.inline(p, run{eff: ps, bg: bg}, s.Depth())

	from, to := trimBounds(p.buf)
	b := gofastcore.Block{Kind: gofastcore.KindParagraph, Loc: gofastcore.Location{Anchor: anchor}}
	if from < to {
		b.Text = string(p.buf[from:to])
		spans, all := settle(p.spans, from, to, p.buf)
		b.Spans, b.Hidden = spans, all
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
		case !isHeader:
			b.Marker = r.outlineMarker(level)
		}
		if p.hasMarker {
			b.Marker = p.marker // as the producer numbered it
		}
	case item != nil:
		b.Kind = gofastcore.KindListItem
		b.Level = uint8(min(item.depth, 255))
		if p.hasMarker {
			b.Marker = p.marker
			_, b.Ordered = item.marker()
		} else {
			b.Marker, b.Ordered = item.marker()
		}
	case c.list != nil:
		// A further paragraph of a list item: part of the item, with no
		// label of its own.
		b.Kind = gofastcore.KindListItem
		b.Level = uint8(min(c.list.depth, 255))
		b.Ordered = c.list.ordered()
	}
	if b.Text != "" {
		if !r.emit(dst, b) {
			return
		}
	}
	// What stands after the paragraph was charged when it was read.
	*dst = append(*dst, p.after...)
	if p.cut {
		r.stop(textCut)
	}
}

// whole adds reasons that apply to all of a block, taking them off its
// spans, where they would say they apply only there.
func whole(b *gofastcore.Block, h gofastcore.Hidden) {
	if h == 0 || b.Text == "" && b.Table == nil {
		return
	}
	b.Hidden |= h
	kept := b.Spans[:0]
	for _, sp := range b.Spans {
		sp.Hidden &^= h
		if sp.Hidden != 0 || sp.Style != 0 || sp.Href != "" {
			kept = append(kept, sp)
		}
	}
	if len(kept) == 0 {
		kept = nil
	}
	b.Spans = kept
	if b.Table != nil {
		regions := b.Table.Hidden[:0]
		for _, reg := range b.Table.Hidden {
			reg.Hidden &^= h
			if reg.Hidden != 0 {
				regions = append(regions, reg)
			}
		}
		if len(regions) == 0 {
			regions = nil
		}
		b.Table.Hidden = regions
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
