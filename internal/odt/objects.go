package odt

import (
	"archive/zip"
	"bytes"
	"io"
	"slices"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/odpkg"
)

// Media types of the sub-documents read here rather than handed on.
const (
	mimeFormula = "application/vnd.oasis.opendocument.formula"
	mimeChart   = "application/vnd.oasis.opendocument.chart"
)

// maxFormula bounds the text of one formula.
const maxFormula = 64 << 10

// object reads draw:object or draw:object-ole, whose Start is current. It
// reports whether the object became blocks of its own (a formula, a
// chart), in which case its frame's replacement picture is not given: it
// is a drawing of the same thing.
func (r *reader) object(c ctxt, name string, dst *[]gofastcore.Block) bool {
	s := &r.s
	ole := string(s.Local()) == "object-ole"
	href := r.attr(ns.XLink, "href")
	if href != "" && odpkg.IsExternal(href) {
		r.external++
		s.Skip()
		return false
	}
	if href == "" {
		// The object held in the element (a flat file): MathML, or a
		// whole document.
		return r.inlineObject(c, name, dst)
	}
	s.Skip()
	member := odpkg.Resolve(href)
	if member == "" || r.seenObjects[member] {
		return false
	}
	r.seenObjects[member] = true
	if ole || r.pkg.Has(member) {
		// An OLE object is one member, as its program stored it.
		return r.child(member, r.pkg.MediaType(member), func() ([]byte, error) { return r.readMember(member) })
	}
	mime := r.pkg.MediaType(member + "/")
	contentName := member + "/content.xml"
	if !r.pkg.Has(contentName) {
		r.addUnread("embedded objects")
		return false
	}
	if mime == mimeFormula || mime == mimeChart || mime == "" {
		var sub xmlscan.Scanner
		closer, err := r.pkg.Scan(contentName, &sub)
		if err == nil {
			done, body := r.subDocument(&sub, c, name, dst)
			closer.Close()
			sub.Release()
			if done {
				return true
			}
			if mime == "" {
				// The manifest does not say; the body does.
				mime = bodyMIME[body]
			}
		}
		if mime == mimeFormula || mime == mimeChart {
			r.addUnread("embedded objects")
			return false
		}
	}
	// Another kind of document: packed as a package of its own and handed
	// to the caller's extractor.
	return r.child(member, mime, func() ([]byte, error) { return r.repack(member, mime) })
}

// bodyMIME gives the media type of a document by its body element.
var bodyMIME = map[string]string{
	"text":         "application/vnd.oasis.opendocument.text",
	"spreadsheet":  "application/vnd.oasis.opendocument.spreadsheet",
	"presentation": "application/vnd.oasis.opendocument.presentation",
	"drawing":      "application/vnd.oasis.opendocument.graphics",
}

// subDocument reads the content of an embedded formula or chart from sub,
// which is at its start. It reports whether it found one, and otherwise
// the body element it met, if any.
func (r *reader) subDocument(sub *xmlscan.Scanner, c ctxt, name string, dst *[]gofastcore.Block) (bool, string) {
	for {
		switch sub.Next() {
		case xmlscan.EOF:
			return false, ""
		case xmlscan.Start:
			switch {
			case sub.Space() == ns.Math && string(sub.Local()) == "math":
				r.formula(sub, c, dst)
				return true, ""
			case sub.Space() == ns.Chart && string(sub.Local()) == "chart":
				r.chart(sub, c, name, dst)
				return true, ""
			case sub.Space() == ns.Office && bodyMIME[string(sub.Local())] != "":
				return false, string(sub.Local())
			}
		}
	}
}

// inlineObject reads an object held in its element.
func (r *reader) inlineObject(c ctxt, name string, dst *[]gofastcore.Block) bool {
	s := &r.s
	depth := s.Depth()
	found := false
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start || found {
			continue
		}
		switch {
		case s.Space() == ns.Math && string(s.Local()) == "math":
			r.formula(s, c, dst)
			found = true
		case s.Space() == ns.Chart && string(s.Local()) == "chart":
			r.chart(s, c, name, dst)
			found = true
		case s.Space() == ns.Office && bodyMIME[string(s.Local())] != "":
			r.addUnread("embedded documents held in a flat file")
			s.Skip()
		}
	}
	return found
}

// child hands an embedded file to Options.Child, with the decompression
// budget settled before and after, and keeps the document it gives.
func (r *reader) child(name, mime string, read func() ([]byte, error)) bool {
	if r.stopped {
		return false
	}
	if len(r.children) >= maxChildren {
		r.lost(capChildren, 1)
		return false
	}
	name = r.label(name)
	data, err := read()
	if err != nil {
		d := gofastcore.New("")
		d.Source = gofastcore.Source{Name: name, MIME: mime}
		d.Warn(gofastcore.SeverityWarning, gofastcore.WarnChildFailed, clean.Label("the embedded file could not be read: "+err.Error()))
		r.children = append(r.children, d)
		return false
	}
	budget := gofastcore.DecodeBudgetFrom(r.ctx)
	r.pkg.Sync(budget)
	doc := r.opts.ExtractChild(r.ctx, name, mime, bytes.NewReader(data), int64(len(data)))
	r.pkg.Sync(budget)
	r.children = append(r.children, doc)
	return false
}

// maxChildren bounds the embedded documents listed.
const maxChildren = 1 << 12

// repack writes the members of an embedded sub-document (the directory
// member/) as a package of its own, members stored, its mimetype first.
func (r *reader) repack(member, mime string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	prefix := member + "/"
	if mime != "" {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
		if err != nil {
			return nil, err
		}
		w.Write([]byte(mime))
	}
	for _, name := range r.pkg.Names() {
		if !strings.HasPrefix(name, prefix) || name == prefix+"mimetype" {
			continue
		}
		rc, err := r.pkg.Open(name)
		if err != nil {
			return nil, err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name[len(prefix):], Method: zip.Store})
		if err == nil {
			_, err = io.Copy(w, rc)
		}
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- formulas ----

// formula reads math:math, whose Start is current in sub, into a formula
// block. The text is the MathML written out linearly (a/b as (a)/(b),
// powers with ^, indices with _); the StarMath source LibreOffice keeps
// in an annotation is given as Attrs["starmath"], and is the text when
// the MathML gives none.
func (r *reader) formula(sub *xmlscan.Scanner, c ctxt, dst *[]gofastcore.Block) {
	m := &mathReader{s: sub}
	text := strings.TrimSpace(m.elem("math"))
	if len(text) > maxFormula {
		text = text[:maxFormula]
		m.cut = true
	}
	text, _ = clean.String([]byte(text), clean.Prose)
	star, _ := clean.String(m.starmath, clean.Prose)
	attrs := map[string]string{gofastcore.AttrNotation: gofastcore.NotationPlain}
	if star != "" {
		attrs["starmath"] = star
		if text == "" {
			text = star
		}
	}
	if text == "" {
		return
	}
	if m.cut {
		r.lost(capFormula, 1)
	}
	if star != "" && !r.budget.Take(len(star)) {
		r.stop(textCut)
		return
	}
	b := gofastcore.Block{Kind: gofastcore.KindFormula, Text: text, Attrs: attrs}
	whole(&b, c.hidden)
	r.emit(dst, b)
}

type mathReader struct {
	s        *xmlscan.Scanner
	starmath []byte
	size     int
	cut      bool // text past maxFormula was left out
}

// elem renders the element whose Start is current and reads to its end.
func (m *mathReader) elem(local string) string {
	s := m.s
	var kids []string
	var text []byte
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		switch k {
		case xmlscan.Text:
			if m.size < maxFormula {
				text = append(text, bytes.TrimSpace(s.Text())...)
				m.size += len(s.Text())
			} else if len(bytes.TrimSpace(s.Text())) > 0 {
				m.cut = true
			}
		case xmlscan.Start:
			child := string(s.Local())
			if s.Space() != ns.Math {
				s.Skip()
				continue
			}
			switch child {
			case "annotation":
				// The attribute is read before the text: reading on reuses
				// the scanner's buffer.
				enc, _ := s.Attr(xmlscan.NoSpace, "encoding")
				star := strings.HasPrefix(string(enc), "StarMath")
				v := s.AppendText(nil)
				if star && len(m.starmath) == 0 {
					if len(v) > maxFormula {
						v, m.cut = v[:maxFormula], true
					}
					m.starmath = v
				}
			case "annotation-xml":
				s.Skip()
			default:
				kids = append(kids, m.elem(child))
			}
		}
	}
	part := func(i int) string {
		if i >= len(kids) {
			return ""
		}
		return kids[i]
	}
	group := func(v string) string {
		if len([]rune(v)) <= 1 {
			return v
		}
		return "(" + v + ")"
	}
	switch local {
	case "mi", "mn", "mtext", "ms":
		return string(text)
	case "mo":
		switch op := string(text); op {
		case "=", "+", "-", "−", "<", ">", "≤", "≥", "≠", "±", "→", "×", "⋅":
			return " " + op + " "
		default:
			return op
		}
	case "mspace":
		return " "
	case "mfrac":
		return group(part(0)) + "/" + group(part(1))
	case "msup":
		return part(0) + "^" + group(part(1))
	case "msub":
		return part(0) + "_" + group(part(1))
	case "msubsup":
		return part(0) + "_" + group(part(1)) + "^" + group(part(2))
	case "msqrt":
		return "sqrt(" + strings.Join(kids, "") + ")"
	case "mroot":
		return "root(" + part(1) + ", " + part(0) + ")"
	case "munder":
		return part(0) + "_" + group(part(1))
	case "mover":
		return part(0) + "^" + group(part(1))
	case "munderover":
		return part(0) + "_" + group(part(1)) + "^" + group(part(2))
	case "mfenced":
		return "(" + strings.Join(kids, ", ") + ")"
	case "mtable":
		return strings.Join(kids, "; ")
	case "mtr", "mlabeledtr":
		return strings.Join(kids, ", ")
	case "semantics":
		return part(0)
	}
	return strings.Join(kids, "")
}

// ---- charts ----

// Bounds on a chart's own table.
const (
	maxChartRows = 1 << 14
	maxChartCols = 1 << 10
)

// chart reads chart:chart, whose Start is current in sub: its title as a
// caption, its data (the table the chart keeps of its own) as a table with
// Attrs role "chart", its type (chart:class) and the titles of its axes.
func (r *reader) chart(sub *xmlscan.Scanner, c ctxt, name string, dst *[]gofastcore.Block) {
	class := strings.TrimPrefix(attrOf(sub, ns.Chart, "class"), "chart:")
	var title, axisX, axisY, dim string
	var cuts int64 // of the axis titles, said if the chart's table is given
	var rows [][]string
	header := 0
	inHeader := false
	depth := sub.Depth()
	for {
		k := sub.Next()
		if k == xmlscan.EOF || k == xmlscan.End && sub.Depth() < depth {
			break
		}
		if k == xmlscan.End && sub.Space() == ns.Table && string(sub.Local()) == "table-header-rows" {
			inHeader = false
		}
		if k != xmlscan.Start {
			continue
		}
		switch {
		case sub.Space() == ns.Chart && string(sub.Local()) == "axis":
			dim = attrOf(sub, ns.Chart, "dimension")
		case sub.Space() == ns.Chart && string(sub.Local()) == "title":
			t, _ := clean.String(sub.AppendText(nil), clean.Prose)
			switch {
			case sub.Depth() > depth+1 && dim == "x" && axisX == "":
				axisX = cutLabel(t, &cuts)
			case sub.Depth() > depth+1 && dim == "y" && axisY == "":
				axisY = cutLabel(t, &cuts)
			case title == "":
				title = t
			}
		case sub.Space() == ns.Chart && (string(sub.Local()) == "subtitle" || string(sub.Local()) == "footer"):
			sub.Skip()
		case sub.Space() == ns.Table && string(sub.Local()) == "table-header-rows":
			inHeader = true
		case sub.Space() == ns.Table && string(sub.Local()) == "table-row":
			n := asked(attrOf(sub, ns.Table, "number-rows-repeated"))
			row, lostCols, cut := chartRow(sub)
			kept := min(n, int64(maxChartRows-len(rows)))
			for range kept {
				rows = append(rows, row)
				if inHeader {
					header++
				}
			}
			if slices.ContainsFunc(row, func(v string) bool { return v != "" }) {
				r.lost(capChartRows, n-kept)
			}
			r.lost(capChartCols, satMul(lostCols, kept))
			r.lost(capChartCell, satMul(cut, kept))
		}
	}
	if title != "" {
		if !r.emit(dst, gofastcore.Block{Kind: gofastcore.KindCaption, Text: title}) {
			return
		}
	}
	width := 0
	for _, row := range rows {
		width = max(width, len(row))
	}
	if width == 0 {
		return
	}
	attrs := map[string]string{gofastcore.AttrRole: gofastcore.RoleChart}
	if class != "" {
		attrs[gofastcore.AttrChart] = cutLabel(class, &cuts)
	}
	if axisX != "" {
		attrs[gofastcore.AttrAxisX] = axisX
	}
	if axisY != "" {
		attrs[gofastcore.AttrAxisY] = axisY
	}
	if name := cutName(name, &cuts); name != "" {
		attrs[gofastcore.AttrName] = name
	}
	r.lost(capLabel, cuts)
	need := gofastcore.BlockCost + gofastcore.CellCost*width*len(rows)
	for _, v := range attrs {
		need += len(v)
	}
	for i, row := range rows {
		for _, cell := range row {
			need += len(cell)
		}
		for len(row) < width {
			row = append(row, "")
		}
		rows[i] = row
	}
	if !r.budget.Take(need) {
		r.stop(textCut)
		return
	}
	t := &gofastcore.Table{Rows: rows, HeaderRows: min(header, len(rows)-1)}
	b := gofastcore.Block{Kind: gofastcore.KindTable, Table: t, Attrs: attrs}
	whole(&b, c.hidden)
	*dst = append(*dst, b)
}

// chartRow reads the cells of a row of a chart's table, and counts the
// cells with text past the last column kept and the cells kept that were
// cut.
func chartRow(sub *xmlscan.Scanner) (row []string, lost, cut int64) {
	depth := sub.Depth()
	for {
		k := sub.Next()
		if k == xmlscan.EOF || k == xmlscan.End && sub.Depth() < depth {
			return row, lost, cut
		}
		if k != xmlscan.Start || sub.Space() != ns.Table || (string(sub.Local()) != "table-cell" && string(sub.Local()) != "covered-table-cell") {
			continue
		}
		n := asked(attrOf(sub, ns.Table, "number-columns-repeated"))
		v, _ := clean.String(sub.AppendText(nil), clean.Prose)
		kept := min(n, int64(maxChartCols-len(row)))
		if len(v) > maxFormula {
			// Cut where the bound is; a character split there becomes
			// U+FFFD.
			v, _ = clean.String([]byte(v[:maxFormula]), clean.Prose)
			cut = satAdd(cut, kept)
		}
		for range kept {
			row = append(row, v)
		}
		if v != "" {
			lost = satAdd(lost, n-kept)
		}
	}
}

func attrOf(s *xmlscan.Scanner, space xmlscan.Space, local string) string {
	v, _ := s.Attr(space, local)
	return string(v)
}

// ---- form controls ----

// control is a form control as office:forms describes it.
type control struct {
	kind, name, label, value string
	options                  []string
	hidden                   bool // a hidden control, or a password, whose value is never shown
	used                     bool
	cuts                     int64 // of kind and name, said if the control is given
}

// maxControls bounds the controls remembered.
const maxControls = 1 << 14

// maxOptions bounds the values a list control or a drop-down field
// offers.
const maxOptions = 1 << 12

// readForms reads office:forms: the controls, by their id, for
// draw:control elements to place.
func (r *reader) readForms() {
	s := &r.s
	depth := s.Depth()
	var cur *control
	curDepth := 0
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k == xmlscan.End && cur != nil && s.Depth() < curDepth {
			cur = nil
		}
		if k != xmlscan.Start || s.Space() != ns.Form {
			continue
		}
		local := string(s.Local())
		switch local {
		case "form", "properties", "property", "list-property", "list-value", "connection-resource":
			continue
		case "option", "item":
			if cur != nil && len(cur.options) >= maxOptions {
				label, _ := s.Attr(ns.Form, "label")
				value, _ := s.Attr(ns.Form, "value")
				if len(label) > 0 || len(value) > 0 {
					r.lost(capOptions, 1)
				}
			} else if cur != nil {
				label := r.plain([]byte(r.attr(ns.Form, "label")))
				if label == "" {
					label = r.plain([]byte(r.attr(ns.Form, "value")))
				}
				if label != "" {
					cur.options = append(cur.options, label)
				}
				if local == "option" && (r.attr(ns.Form, "current-selected") == "true" || r.attr(ns.Form, "selected") == "true") && cur.value == "" {
					cur.value = label
				}
			}
			continue
		}
		id := r.attr(ns.Form, "id")
		if id == "" {
			id = r.attr(xmlscan.SpaceXML, "id")
		}
		c := &control{label: r.plain([]byte(r.attr(ns.Form, "label")))}
		c.kind, c.name = cutLabel(local, &c.cuts), cutName(r.attr(ns.Form, "name"), &c.cuts)
		c.value = r.plain([]byte(r.attr(ns.Form, "current-value")))
		if c.value == "" {
			c.value = r.plain([]byte(r.attr(ns.Form, "value")))
		}
		switch local {
		case "checkbox", "radio":
			state := r.attr(ns.Form, "current-state")
			if state == "" {
				state = r.attr(ns.Form, "state")
			}
			if state == "" && r.attr(ns.Form, "current-selected") == "true" {
				state = "checked"
			}
			c.value = ""
			if state == "checked" {
				c.value = "checked"
			}
		case "password", "hidden":
			c.hidden = true
		}
		if id == "" {
			continue
		}
		if _, dup := r.controls[id]; !dup && len(r.controls) >= maxControls {
			r.lost(capControls, 1)
			continue
		}
		if _, dup := r.controls[id]; !dup {
			r.controls[id] = c
			r.controlOrder = append(r.controlOrder, id)
		}
		cur, curDepth = c, s.Depth()
	}
}

// controlText is what a control holds: its label and its value.
func (c *control) text() string {
	switch {
	case c.label != "" && c.value != "":
		return c.label + ": " + c.value
	case c.value != "":
		return c.value
	}
	return c.label
}

// placeControl handles draw:control: the control's text where it stands,
// and the values a list offers as a choices note.
func (r *reader) placeControl(c ctxt, dst *[]gofastcore.Block) {
	id := r.attr(ns.Draw, "control")
	r.s.Skip()
	ctl, ok := r.controls[id]
	if !ok || ctl.used {
		return
	}
	ctl.used = true
	r.controlBlocks(ctl, c.hidden, dst, false)
}

// controlBlocks writes a control's blocks. The value of a hidden control
// or a password field is a form value, given with no reason; the reasons
// are those declared for where the control stands.
func (r *reader) controlBlocks(ctl *control, hidden gofastcore.Display, dst *[]gofastcore.Block, stored bool) {
	if ctl.text() != "" || len(ctl.options) > 0 {
		r.lost(capLabel, ctl.cuts)
	}
	if text := ctl.text(); text != "" {
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text, Attrs: map[string]string{"control": ctl.kind}}
		if ctl.name != "" {
			b.Attrs[gofastcore.AttrField] = ctl.name
		}
		whole(&b, hidden)
		if !r.emit(dst, b) {
			return
		}
	}
	if len(ctl.options) > 0 {
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: strings.Join(ctl.options, "\n"),
			Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteChoices}}
		if ctl.name != "" {
			b.Attrs[gofastcore.AttrField] = ctl.name
		}
		whole(&b, hidden)
		r.emit(dst, b)
	}
}

// unplacedControls puts the controls no draw:control shows into the
// stored part: fields of a form shown on no page.
func (r *reader) unplacedControls() {
	for _, id := range r.controlOrder {
		ctl := r.controls[id]
		if ctl.used || r.stopped {
			continue
		}
		var blocks []gofastcore.Block
		r.controlBlocks(ctl, 0, &blocks, true)
		for _, b := range blocks {
			b.Attrs[gofastcore.AttrStored] = gofastcore.StoredFormField
			r.userVars = append(r.userVars, b)
		}
	}
}
