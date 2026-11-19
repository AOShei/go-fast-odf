package odt

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/style"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// WarnRegionSplit: an area of a sheet whose cells are spread so thinly
// that the rectangle around them is mostly empty was written as one table
// per run of cells in a row instead of as one table.
const WarnRegionSplit = "odf.region_split"

// Bounds on a sheet's coordinates. A sheet of LibreOffice has at most
// 16,384 columns and 1,048,576 rows; a file can say anything.
const (
	maxSheetCols = 1 << 16
	maxSheetRows = 1 << 24
)

// sheetCell is a cell of a sheet that has something: text, a span, a
// formula, a value.
//
// A sheet can hold millions of cells, so the struct is kept small: what
// few cells have is behind a pointer, shared by the copies of a repeated
// cell.
type sheetCell struct {
	row, col   int32
	rows, cols int32 // spans, at least 1
	text       string
	hidden     gofastcore.Hidden
	more       *cellMore
}

type cellMore struct {
	formula string
	typ     gofastcore.CellType
	value   string
	href    string
}

// run is a stretch of rows or columns, from and to inclusive.
type span struct {
	from, to int
	hidden   gofastcore.Hidden
	style    string // default cell style, for columns
}

type sheetIn struct {
	cells   []sheetCell
	rows    []span // hidden rows
	cols    []span // columns as declared
	notes   []placed
	extra   []gofastcore.Block // shapes on the sheet
	choices map[string]*[4]int // validation name -> bounds of the cells that use it
	order   []string
}

// placed is a note that stands at a cell.
type placed struct {
	row, col int
	block    gofastcore.Block
}

// validation is what a content validation offers.
type validation struct {
	text string
}

// spreadsheet reads office:spreadsheet, which is current, into one part
// per sheet.
func (r *reader) spreadsheet(serr error) []gofastcore.Part {
	var parts []gofastcore.Part
	if serr != nil || r.s.Depth() != 3 {
		p := gofastcore.Part{Kind: gofastcore.PartSheet, Number: 1}
		r.noteFailure(&p, "content.xml", serr)
		r.finish(&p, "workbook")
		return []gofastcore.Part{p}
	}
	r.validations = map[string]validation{}
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
		switch {
		case s.Space() == ns.Table && string(s.Local()) == "table":
			n := len(parts) + 1
			if !r.opts.Parts.Contains(n) {
				s.Skip()
				p := gofastcore.Part{Kind: gofastcore.PartSheet, Number: n}
				p.MarkHidden()
				parts = append(parts, p)
				continue
			}
			parts = append(parts, r.sheet(n))
		case s.Space() == ns.Table && string(s.Local()) == "content-validations":
			r.readValidations()
		case s.Space() == ns.Table && (string(s.Local()) == "named-expressions" || string(s.Local()) == "database-ranges"):
			r.namedRanges("")
		default:
			s.Skip()
		}
	}
	if err := s.Err(); err != nil && len(parts) > 0 {
		r.noteFailure(&parts[len(parts)-1], "content.xml", err)
	} else if err != nil {
		p := gofastcore.Part{Kind: gofastcore.PartSheet, Number: 1}
		r.noteFailure(&p, "content.xml", err)
		r.finish(&p, "workbook")
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		p := gofastcore.Part{Kind: gofastcore.PartSheet, Number: 1}
		r.finish(&p, "workbook")
		parts = append(parts, p)
	}
	// A limit that stopped the reading is said on the last part read.
	if r.limitMsg != "" {
		last := &parts[len(parts)-1]
		last.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, r.limitMsg)
		r.limitMsg = ""
	}
	return parts
}

// readValidations reads table:content-validations: the lists of values
// cells offer.
func (r *reader) readValidations() {
	s := &r.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start || s.Space() != ns.Table || string(s.Local()) != "content-validation" {
			continue
		}
		name := r.attr(ns.Table, "name")
		if text := listOf(r.attr(ns.Table, "condition")); text != "" && len(r.validations) < 1<<14 {
			r.validations[name] = validation{text: text}
		}
		s.Skip()
	}
}

// listOf reads a condition of the form cell-content-is-in-list(...): the
// values one per line, or a reference to cells, written "=" and the
// reference as the file gives it.
func listOf(cond string) string {
	const fn = "cell-content-is-in-list("
	i := strings.Index(cond, fn)
	if i < 0 {
		return ""
	}
	in := strings.TrimSpace(cond[i+len(fn):])
	if j := strings.LastIndexByte(in, ')'); j >= 0 {
		in = strings.TrimSpace(in[:j])
	}
	if in == "" {
		return ""
	}
	if in[0] != '"' {
		ref := strings.TrimSuffix(strings.TrimPrefix(in, "["), "]")
		v, _ := clean.String([]byte("="+ref), clean.Prose)
		return v
	}
	var values []string
	var cur strings.Builder
	quoted := false
	for k := 0; k < len(in); k++ {
		c := in[k]
		switch {
		case c == '"' && quoted && k+1 < len(in) && in[k+1] == '"':
			cur.WriteByte('"')
			k++
		case c == '"':
			quoted = !quoted
		case !quoted && (c == ';' || c == ','):
			values = append(values, cur.String())
			cur.Reset()
		case quoted:
			cur.WriteByte(c)
		}
	}
	values = append(values, cur.String())
	var lines []string
	for _, v := range values {
		if v, _ := clean.String([]byte(v), clean.Prose); v != "" {
			lines = append(lines, v)
		}
	}
	return strings.Join(lines, "\n")
}

// namedRanges reads table:named-expressions or table:database-ranges into
// the stored part. sheet prefixes the names local to a sheet.
func (r *reader) namedRanges(sheet string) {
	s := &r.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start || s.Space() != ns.Table {
			continue
		}
		var addr string
		switch string(s.Local()) {
		case "named-range", "database-range":
			addr = r.attr(ns.Table, "cell-range-address")
			if addr == "" {
				addr = r.attr(ns.Table, "target-range-address")
			}
		case "named-expression":
			addr = r.attr(ns.Table, "expression")
		default:
			continue
		}
		name := clean.Name(r.attr(ns.Table, "name"))
		if sheet != "" && name != "" {
			name = sheet + "!" + name
		}
		value, _ := clean.String([]byte(addr), clean.Prose)
		text := name + ": " + value
		switch {
		case name == "" && value == "":
			continue
		case value == "":
			text = name
		case name == "":
			text = value
		}
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text,
			Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredDefinedName}}
		if name != "" {
			b.Attrs[gofastcore.AttrField] = name
		}
		if string(s.Local()) == "database-range" {
			b.Attrs["kind"] = "database_range"
		}
		if !r.emit(&r.userVars, b) {
			return
		}
	}
}

// sheet reads table:table into a part.
func (r *reader) sheet(n int) gofastcore.Part {
	name := clean.Name(r.attr(ns.Table, "name"))
	part := gofastcore.Part{Kind: gofastcore.PartSheet, Number: n, Name: name}
	if r.set.Box(r.sc, "table", r.attr(ns.Table, "style-name")).BoxHidden {
		part.Hidden = gofastcore.HiddenInvisible
	}
	sh := &sheetIn{choices: map[string]*[4]int{}}
	row := 0
	r.sheetBody(sh, &row, name)
	part.Blocks = r.regions(sh, &part)
	if len(sh.extra) > 0 {
		part.Blocks = append(part.Blocks, sh.extra...)
	}
	r.finish(&part, "sheet")
	return part
}

// sheetBody reads the children of a table or of a group of rows or
// columns.
func (r *reader) sheetBody(sh *sheetIn, row *int, sheetName string) {
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
		switch {
		case space == ns.Table && local == "table-column":
			n := repeat(r.attr(ns.Table, "number-columns-repeated"))
			from := 0
			if len(sh.cols) > 0 {
				from = sh.cols[len(sh.cols)-1].to + 1
			}
			if from < maxSheetCols {
				sh.cols = append(sh.cols, span{from: from, to: min(from+n, maxSheetCols) - 1,
					hidden: visibility(r.attr(ns.Table, "visibility")), style: r.attr(ns.Table, "default-cell-style-name")})
			}
			s.Skip()
		case space == ns.Table && local == "table-row":
			r.sheetRow(sh, row)
		case space == ns.Table && (local == "table-columns" || local == "table-header-columns" || local == "table-column-group" ||
			local == "table-rows" || local == "table-header-rows" || local == "table-row-group"):
			r.sheetBody(sh, row, sheetName)
		case space == ns.Table && local == "named-expressions":
			r.namedRanges(sheetName)
		case space == ns.Table && local == "table-source":
			r.external++
			s.Skip()
		case space == ns.Table && local == "shapes":
			r.blocks(ctxt{bg: style.White}, &sh.extra)
		case space == ns.Office && local == "forms":
			if r.hasForms() {
				r.addUnread("sets of form controls")
			}
		default:
			s.Skip()
		}
	}
}

// colStyle returns the default cell style of a column.
func (sh *sheetIn) colStyle(col int) string {
	i, found := slices.BinarySearchFunc(sh.cols, col, func(sp span, c int) int {
		switch {
		case sp.to < c:
			return -1
		case sp.from > c:
			return 1
		}
		return 0
	})
	if found {
		return sh.cols[i].style
	}
	return ""
}

func (r *reader) sheetRow(sh *sheetIn, row *int) {
	s := &r.s
	n := repeat(r.attr(ns.Table, "number-rows-repeated"))
	hidden := visibility(r.attr(ns.Table, "visibility"))
	rowStyle := r.attr(ns.Table, "default-cell-style-name")
	top := *row
	*row = min(top+n, maxSheetRows)
	if top >= maxSheetRows {
		s.Skip()
		return
	}
	if hidden != 0 {
		sh.rows = append(sh.rows, span{from: top, to: *row - 1, hidden: hidden})
	}
	var cells []sheetCell
	var notes []placed
	col := 0
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		if s.Space() != ns.Table || (string(s.Local()) != "table-cell" && string(s.Local()) != "covered-table-cell") {
			s.Skip()
			continue
		}
		covered := string(s.Local()) == "covered-table-cell"
		reps := repeat(r.attr(ns.Table, "number-columns-repeated"))
		c := sheetCell{rows: 1, cols: 1}
		if !covered {
			c.rows = int32(min(repeat(r.attr(ns.Table, "number-rows-spanned")), maxSheetRows))
			c.cols = int32(min(repeat(r.attr(ns.Table, "number-columns-spanned")), maxSheetCols))
		}
		if vname := r.attr(ns.Table, "content-validation-name"); vname != "" {
			if _, ok := r.validations[vname]; ok {
				sh.choose(vname, top, col, *row-1, min(col+reps, maxSheetCols)-1)
			}
		}
		styleName := r.attr(ns.Table, "style-name")
		if styleName == "" {
			styleName = rowStyle
		}
		if styleName == "" {
			styleName = sh.colStyle(col)
		}
		vt := r.attr(ns.Office, "value-type")
		formula := r.attr(ns.Table, "formula")
		value := cellValue(r, vt)
		cs := r.set.Box(r.sc, "table-cell", styleName)
		bg := cs.Background
		if !bg.Known() {
			bg = style.White
		}
		var content []gofastcore.Block
		r.blocks(ctxt{bg: bg}, &content)
		text, spans, hid, rest := r.flatten(content)
		if vt == "date" || vt == "time" {
			// A date or time stored as a value is written in ISO 8601,
			// whatever the display format.
			if iso := isoValue(vt, value); iso != "" {
				text, spans = iso, nil
			}
		}
		if text == "" && vt != "" && vt != "string" {
			text = value
			if vt == "boolean" {
				text = strings.ToUpper(value)
			}
		}
		if text != "" {
			hid |= reasons(&run{eff: cs, bg: bg})
		}
		c.text, c.hidden = text, hid
		var more cellMore
		if r.opts.Cells {
			more.formula = clean.Label(formula)
			more.typ, more.value = cellType(vt), value
			if more.typ == gofastcore.CellText || more.value == text {
				more.typ, more.value = 0, ""
			}
		}
		if r.opts.Spans {
			for _, sp := range spans {
				if sp.Href != "" {
					more.href = sp.Href
					break
				}
			}
		}
		if more != (cellMore{}) {
			c.more = &more
		}
		for _, b := range rest {
			notes = append(notes, placed{row: top, col: col, block: b})
		}
		has := c.text != "" || c.rows > 1 || c.cols > 1 || c.more != nil
		if covered || !has {
			col = min(col+reps, maxSheetCols)
		} else {
			for k := 0; k < reps && col < maxSheetCols; k++ {
				c.col = int32(col)
				cells = append(cells, c)
				col++
			}
		}
		if col >= maxSheetCols {
			break // the rest of the row is passed over by the caller
		}
	}
	// The row's cells, once for every row it stands for. Each is charged
	// what a cell costs now, so that a few bytes repeated cannot fill
	// memory before the tables are made; the tables charge in full.
	for rr := top; rr < *row && len(cells) > 0; rr++ {
		if r.maxCells >= 0 && r.cells+int64(len(cells)) > r.maxCells {
			r.stop("the limit on table cells was reached; the workbook is cut off there")
			return
		}
		if r.budget != nil && int64(len(sh.cells)+len(cells))*gofastcore.CellCost > r.budget.Left() {
			r.stop(textCut)
			return
		}
		r.cells += int64(len(cells))
		for _, c := range cells {
			c.row = int32(rr)
			sh.cells = append(sh.cells, c)
		}
	}
	sh.notes = append(sh.notes, notes...)
}

// choose records that cells use a validation.
func (sh *sheetIn) choose(name string, r0, c0, r1, c1 int) {
	b, ok := sh.choices[name]
	if !ok {
		sh.choices[name] = &[4]int{r0, c0, r1, c1}
		sh.order = append(sh.order, name)
		return
	}
	b[0], b[1], b[2], b[3] = min(b[0], r0), min(b[1], c0), max(b[2], r1), max(b[3], c1)
}

// cellValue returns the value a cell stores, as the file writes it.
func cellValue(r *reader, vt string) string {
	var v []byte
	switch vt {
	case "date":
		v, _ = r.s.Attr(ns.Office, "date-value")
	case "time":
		v, _ = r.s.Attr(ns.Office, "time-value")
	case "boolean":
		v, _ = r.s.Attr(ns.Office, "boolean-value")
	case "string":
		v, _ = r.s.Attr(ns.Office, "string-value")
	case "":
		return ""
	default:
		v, _ = r.s.Attr(ns.Office, "value")
	}
	out, _ := clean.String(v, clean.Prose)
	return clean.Label(out)
}

func cellType(vt string) gofastcore.CellType {
	switch vt {
	case "float", "percentage", "currency":
		return gofastcore.CellNumber
	case "date", "time":
		return gofastcore.CellDate
	case "boolean":
		return gofastcore.CellBool
	case "string":
		return gofastcore.CellText
	}
	return 0
}

// isoValue writes a date or time value in ISO 8601: a date as it is
// (xsd:date or xsd:dateTime), a time from its duration form
// (PT14H30M00S) as 14:30:00.
func isoValue(vt, v string) string {
	if vt == "date" {
		return v
	}
	d := strings.TrimPrefix(v, "-")
	if !strings.HasPrefix(d, "P") {
		return ""
	}
	d = d[1:]
	days := 0
	if i := strings.IndexByte(d, 'D'); i >= 0 {
		n, err := strconv.Atoi(d[:i])
		if err != nil {
			return ""
		}
		days, d = n, d[i+1:]
	}
	d = strings.TrimPrefix(d, "T")
	var h, m int
	var sec string = "00"
	for d != "" {
		i := strings.IndexAny(d, "HMS")
		if i <= 0 {
			return ""
		}
		num := d[:i]
		switch d[i] {
		case 'H':
			n, err := strconv.Atoi(num)
			if err != nil {
				return ""
			}
			h = n
		case 'M':
			n, err := strconv.Atoi(num)
			if err != nil {
				return ""
			}
			m = n
		case 'S':
			if _, err := strconv.ParseFloat(num, 64); err != nil {
				return ""
			}
			sec = num
			if j := strings.IndexByte(sec, '.'); j == 1 || len(sec) == 1 {
				sec = "0" + sec
			}
		}
		d = d[i+1:]
	}
	return fmt.Sprintf("%02d:%02d:%s", h+24*days, m, sec)
}

// ColName writes a column index from 0 as letters: A, Z, AA.
func ColName(c int) string {
	var b []byte
	for c >= 0 {
		b = append([]byte{byte('A' + c%26)}, b...)
		c = c/26 - 1
	}
	return string(b)
}

func cellRef(row, col int) string { return ColName(col) + strconv.Itoa(row+1) }

func rangeRef(r0, c0, r1, c1 int) string {
	if r0 == r1 && c0 == c1 {
		return cellRef(r0, c0)
	}
	return cellRef(r0, c0) + ":" + cellRef(r1, c1)
}

// region is a rectangle of a sheet written as one table.
type region struct {
	r0, c0, r1, c1 int
	cells          []sheetCell
}

// regions turns a sheet's cells into tables, one per area of cells that
// touch, and the notes that stand at cells.
func (r *reader) regions(sh *sheetIn, part *gofastcore.Part) []gofastcore.Block {
	var out []gofastcore.Block
	split := 0
	for _, reg := range findRegions(sh.cells) {
		area := int64(reg.r1-reg.r0+1) * int64(reg.c1-reg.c0+1)
		if area > 4*int64(len(reg.cells))+1024 {
			split++
			for _, row := range rowRuns(reg.cells) {
				if !r.sheetTable(sh, row, &out) {
					return out
				}
			}
			continue
		}
		if !r.sheetTable(sh, reg, &out) {
			return out
		}
	}
	if split > 0 {
		part.Warn(gofastcore.SeverityInfo, WarnRegionSplit, fmt.Sprintf("%d areas of thinly spread cells were written as one table per run of cells in a row", split))
	}
	for _, n := range sh.notes {
		b := n.block
		b.Loc.Range = cellRef(n.row, n.col)
		out = append(out, b) // charged as it was read
	}
	for _, name := range sh.order {
		v, ok := r.validations[name]
		if !ok {
			continue
		}
		b := sh.choices[name]
		note := gofastcore.Block{Kind: gofastcore.KindNote, Text: v.text, Loc: gofastcore.Location{Range: rangeRef(b[0], b[1], b[2], b[3])},
			Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteChoices, gofastcore.AttrField: clean.Name(name)}}
		if !r.emit(&out, note) {
			break
		}
	}
	return out
}

// findRegions groups cells into rectangles: rows that follow one another
// with cells form a band, and within a band the columns with cells that
// touch form a region. The cells come in row order.
func findRegions(cells []sheetCell) []region {
	var out []region
	for i := 0; i < len(cells); {
		// A band: rows with cells, each next to the one before, or under
		// a cell that spans down to it.
		j, bottom := i, int(cells[i].row)
		for j < len(cells) && int(cells[j].row) <= bottom+1 {
			bottom = max(bottom, int(cells[j].row+cells[j].rows)-1)
			j++
		}
		band := cells[i:j]
		var cols []span
		for _, c := range band {
			cols = append(cols, span{from: int(c.col), to: int(c.col+c.cols) - 1})
		}
		slices.SortFunc(cols, func(a, b span) int { return a.from - b.from })
		var groups []span
		for _, c := range cols {
			if n := len(groups); n > 0 && c.from <= groups[n-1].to+1 {
				groups[n-1].to = max(groups[n-1].to, c.to)
				continue
			}
			groups = append(groups, c)
		}
		for _, g := range groups {
			reg := region{r0: int(band[0].row), r1: bottom, c0: g.from, c1: g.to}
			for _, c := range band {
				if int(c.col) >= g.from && int(c.col) <= g.to {
					reg.cells = append(reg.cells, c)
				}
			}
			out = append(out, reg)
		}
		i = j
	}
	return out
}

// rowRuns splits the cells of a region into runs of cells side by side
// in a row, each a region of its own.
func rowRuns(cells []sheetCell) []region {
	var out []region
	for _, c := range cells {
		c.rows, c.cols = 1, 1
		row, col := int(c.row), int(c.col)
		if n := len(out); n > 0 && out[n-1].r0 == row && out[n-1].c1+1 == col {
			out[n-1].c1 = col
			out[n-1].cells = append(out[n-1].cells, c)
			continue
		}
		out = append(out, region{r0: row, r1: row, c0: col, c1: col, cells: []sheetCell{c}})
	}
	return out
}

// sheetTable writes a region as a table block, charging its cells. It
// reports false if the budget ran out.
func (r *reader) sheetTable(sh *sheetIn, reg region, out *[]gofastcore.Block) bool {
	h, w := reg.r1-reg.r0+1, reg.c1-reg.c0+1
	need := gofastcore.BlockCost + gofastcore.CellCost*h*w
	for _, c := range reg.cells {
		need += len(c.text)
		if m := c.more; m != nil {
			need += len(m.formula) + len(m.value) + len(m.href)
		}
	}
	if !r.budget.Take(need) {
		r.stop(textCut)
		return false
	}
	t := &gofastcore.Table{Rows: make([][]string, h)}
	for i := range t.Rows {
		t.Rows[i] = make([]string, w)
	}
	var all gofastcore.Hidden
	for _, c := range reg.cells {
		i, j := int(c.row)-reg.r0, int(c.col)-reg.c0
		t.Rows[i][j] = c.text
		if c.hidden != 0 {
			t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: i, Col: j, RowSpan: 1, ColSpan: 1, Hidden: c.hidden})
			all |= c.hidden
		}
		if rs, cs := min(int(c.rows), h-i), min(int(c.cols), w-j); rs*cs > 1 {
			t.Merges = append(t.Merges, gofastcore.Merge{Row: i, Col: j, RowSpan: rs, ColSpan: cs})
		}
		if m := c.more; m != nil {
			t.Cells = append(t.Cells, gofastcore.CellDetail{Row: i, Col: j, Formula: m.formula, Type: m.typ, Value: m.value, Href: m.href})
		}
	}
	t.Merges = noOverlap(t.Merges, h, w)
	for _, rs := range sh.rows {
		if a, b := max(rs.from, reg.r0), min(rs.to, reg.r1); a <= b {
			t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: a - reg.r0, Col: 0, RowSpan: b - a + 1, ColSpan: w, Hidden: rs.hidden})
			all |= rs.hidden
		}
	}
	for _, cs := range sh.cols {
		if a, b := max(cs.from, reg.c0), min(cs.to, reg.c1); a <= b && cs.hidden != 0 {
			t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: 0, Col: a - reg.c0, RowSpan: h, ColSpan: b - a + 1, Hidden: cs.hidden})
			all |= cs.hidden
		}
	}
	*out = append(*out, gofastcore.Block{Kind: gofastcore.KindTable, Origin: gofastcore.OriginInferred, Table: t, Hidden: all,
		Loc: gofastcore.Location{Range: rangeRef(reg.r0, reg.c0, reg.r1, reg.c1)}})
	return true
}

// noOverlap drops merges that overlap one before them, on a grid of the
// table's cells. A hostile file can give many merges over one area, so
// the work is bounded: past a few passes over the table the rest are
// dropped.
func noOverlap(ms []gofastcore.Merge, h, w int) []gofastcore.Merge {
	if len(ms) < 2 {
		return ms
	}
	grid := make([]bool, h*w)
	work := 4 * h * w
	kept := ms[:0]
	for _, m := range ms {
		ok := true
		for i := m.Row; i < m.Row+m.RowSpan && ok; i++ {
			for j := m.Col; j < m.Col+m.ColSpan; j++ {
				work--
				if grid[i*w+j] || work < 0 {
					ok = false
					break
				}
			}
		}
		if work < 0 {
			break
		}
		if !ok {
			continue
		}
		for i := m.Row; i < m.Row+m.RowSpan; i++ {
			for j := m.Col; j < m.Col+m.ColSpan; j++ {
				grid[i*w+j] = true
			}
		}
		kept = append(kept, m)
	}
	return kept
}
