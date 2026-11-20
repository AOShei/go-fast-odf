package odt

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/odpkg"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
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
		case s.Space() == ns.Office && (string(s.Local()) == "text" || string(s.Local()) == "spreadsheet" || string(s.Local()) == "presentation"):
			r.addUnread("embedded documents held in a flat file")
			s.Skip()
		}
	}
	return found
}

// child hands an embedded file to Options.Child, with the decompression
// budget settled before and after, and keeps the document it gives.
func (r *reader) child(name, mime string, read func() ([]byte, error)) bool {
	if r.stopped || len(r.children) >= maxChildren {
		return false
	}
	data, err := read()
	if err != nil {
		d := gofastcore.New("")
		d.Source = gofastcore.Source{Name: clean.Label(name), MIME: mime}
		d.Warn(gofastcore.SeverityWarning, gofastcore.WarnChildFailed, clean.Label("the embedded file could not be read: "+err.Error()))
		r.children = append(r.children, d)
		return false
	}
	budget := gofastcore.DecodeBudgetFrom(r.ctx)
	r.pkg.Sync(budget)
	doc := r.opts.ExtractChild(r.ctx, clean.Label(name), mime, bytes.NewReader(data), int64(len(data)))
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
			}
		case xmlscan.Start:
			child := string(s.Local())
			if s.Space() != ns.Math {
				s.Skip()
				continue
			}
			switch child {
			case "annotation":
				enc, _ := s.Attr(xmlscan.NoSpace, "encoding")
				v := s.AppendText(nil)
				if strings.HasPrefix(string(enc), "StarMath") && len(m.starmath) == 0 && len(v) <= maxFormula {
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
				axisX = clean.Label(t)
			case sub.Depth() > depth+1 && dim == "y" && axisY == "":
				axisY = clean.Label(t)
			case title == "":
				title = t
			}
		case sub.Space() == ns.Chart && (string(sub.Local()) == "subtitle" || string(sub.Local()) == "footer"):
			sub.Skip()
		case sub.Space() == ns.Table && string(sub.Local()) == "table-header-rows":
			inHeader = true
		case sub.Space() == ns.Table && string(sub.Local()) == "table-row" && len(rows) < maxChartRows:
			n := repeat(attrOf(sub, ns.Table, "number-rows-repeated"))
			row := chartRow(sub)
			for i := 0; i < n && len(rows) < maxChartRows; i++ {
				rows = append(rows, row)
				if inHeader {
					header++
				}
			}
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
		attrs[gofastcore.AttrChart] = clean.Label(class)
	}
	if axisX != "" {
		attrs[gofastcore.AttrAxisX] = axisX
	}
	if axisY != "" {
		attrs[gofastcore.AttrAxisY] = axisY
	}
	if name != "" {
		attrs[gofastcore.AttrName] = name
	}
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

// chartRow reads the cells of a row of a chart's table.
func chartRow(sub *xmlscan.Scanner) []string {
	var row []string
	depth := sub.Depth()
	for {
		k := sub.Next()
		if k == xmlscan.EOF || k == xmlscan.End && sub.Depth() < depth {
			return row
		}
		if k != xmlscan.Start || sub.Space() != ns.Table || (string(sub.Local()) != "table-cell" && string(sub.Local()) != "covered-table-cell") {
			continue
		}
		n := repeat(attrOf(sub, ns.Table, "number-columns-repeated"))
		v, _ := clean.String(sub.AppendText(nil), clean.Prose)
		if len(v) > maxFormula {
			v = clean.Label(v)
		}
		for i := 0; i < n && len(row) < maxChartCols; i++ {
			row = append(row, v)
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
}

// maxControls bounds the controls remembered.
const maxControls = 1 << 14

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
			if cur != nil && len(cur.options) < 1<<12 {
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
		c := &control{kind: clean.Label(local), name: clean.Name(r.attr(ns.Form, "name")),
			label: r.plain([]byte(r.attr(ns.Form, "label")))}
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
		if id == "" || len(r.controls) >= maxControls {
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
// or a password field is never shown, but form values are routine data:
// it is flagged only as an outlier (gofastcore.UnshownOutlier).
func (r *reader) controlBlocks(ctl *control, hidden gofastcore.Hidden, dst *[]gofastcore.Block, stored bool) {
	if text := ctl.text(); text != "" {
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text, Attrs: map[string]string{"control": ctl.kind}}
		if ctl.name != "" {
			b.Attrs[gofastcore.AttrField] = ctl.name
		}
		whole(&b, hidden)
		if ctl.hidden || stored {
			r.unshown(&b, stored)
		}
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
