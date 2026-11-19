package odt

import (
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// maxCols bounds the columns of a table. A text document's table has a
// few; repetition attributes can ask for billions.
const maxCols = 1 << 14

// maxRepeat bounds what one repetition attribute may ask for.
const maxRepeat = 1 << 20

type cellIn struct {
	text             string
	hidden           gofastcore.Hidden
	repeat           int
	colSpan, rowSpan int
}

type rowIn struct {
	cells  []cellIn
	repeat int
	header bool
	hidden gofastcore.Hidden
}

type tableIn struct {
	rows  []rowIn
	cols  []gofastcore.Hidden // per column, as far as declared
	after []gofastcore.Block
}

func repeat(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxRepeat)
}

func visibility(v string) gofastcore.Hidden {
	if v == "collapse" || v == "filter" {
		return gofastcore.HiddenInvisible
	}
	return 0
}

// table reads table:table.
func (r *reader) table(c ctxt, dst *[]gofastcore.Block) {
	name := clean.Name(r.attr(ns.Table, "name"))
	t := &tableIn{}
	r.tableBody(c, t, false)
	if r.stopped {
		return
	}
	if b, ok := r.buildTable(t); ok {
		if name != "" {
			b.Attrs = map[string]string{gofastcore.AttrName: name}
		}
		whole(&b, c.hidden)
		*dst = append(*dst, b) // charged as it was built
	}
	*dst = append(*dst, t.after...)
}

func (r *reader) tableBody(c ctxt, t *tableIn, header bool) {
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
		if s.Space() != ns.Table {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "table-column":
			n := min(repeat(r.attr(ns.Table, "number-columns-repeated")), maxCols-len(t.cols))
			h := visibility(r.attr(ns.Table, "visibility"))
			for range n {
				t.cols = append(t.cols, h)
			}
			s.Skip()
		case "table-columns", "table-header-columns", "table-column-group", "table-rows", "table-row-group":
			r.tableBody(c, t, header)
		case "table-header-rows":
			r.tableBody(c, t, true)
		case "table-row":
			r.row(c, t, header)
		default:
			s.Skip()
		}
	}
}

func (r *reader) row(c ctxt, t *tableIn, header bool) {
	s := &r.s
	row := rowIn{repeat: repeat(r.attr(ns.Table, "number-rows-repeated")), header: header,
		hidden: visibility(r.attr(ns.Table, "visibility"))}
	width := 0
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
		cell := cellIn{repeat: min(repeat(r.attr(ns.Table, "number-columns-repeated")), maxCols-width),
			colSpan: repeat(r.attr(ns.Table, "number-columns-spanned")), rowSpan: repeat(r.attr(ns.Table, "number-rows-spanned"))}
		if string(s.Local()) == "covered-table-cell" {
			cell.colSpan, cell.rowSpan = 1, 1
		}
		inner := ctxt{hidden: c.hidden, bg: c.bg}
		if bg := r.set.Box(r.sc, "table-cell", r.attr(ns.Table, "style-name")).Background; bg.Known() {
			inner.bg = bg
		}
		var content []gofastcore.Block
		r.blocks(inner, &content)
		text, _, hid, rest := r.flatten(content)
		cell.text, cell.hidden = text, hid
		t.after = append(t.after, rest...)
		if cell.repeat <= 0 {
			continue // past the last column kept
		}
		width += cell.repeat
		row.cells = append(row.cells, cell)
	}
	t.rows = append(t.rows, row)
}

// buildTable makes the table block, charging its cells to the budget and
// the cell limit. It reports false for a table with no text.
func (r *reader) buildTable(t *tableIn) (gofastcore.Block, bool) {
	// The columns that matter end at the last cell with text or a span.
	width := 0
	lastRow := -1
	for i, row := range t.rows {
		col := 0
		for _, cell := range row.cells {
			if cell.text != "" {
				width = max(width, col+cell.repeat)
				lastRow = i
			}
			col += cell.repeat
		}
	}
	if width == 0 {
		return gofastcore.Block{}, false
	}
	rows := t.rows[:lastRow+1]
	if !r.budget.Take(gofastcore.BlockCost) {
		r.stop(textCut)
		return gofastcore.Block{}, false
	}
	tb := &gofastcore.Table{Rows: [][]string{}}
	var hiddenAll gofastcore.Hidden
	headerDone := false
	type span struct{ row, col, rows, cols int }
	var spans []span
	cut := false
	for _, row := range rows {
		for rep := 0; rep < row.repeat && !cut; rep++ {
			if r.maxCells >= 0 && r.cells+int64(width) > r.maxCells {
				r.stop("the limit on table cells was reached; the document is cut off there")
				cut = true
				break
			}
			cells := make([]string, width)
			need := gofastcore.CellCost * width
			col := 0
			at := len(tb.Rows)
			for _, cell := range row.cells {
				for k := 0; k < cell.repeat && col < width; k++ {
					cells[col] = cell.text
					need += len(cell.text)
					if cell.hidden != 0 {
						tb.Hidden = append(tb.Hidden, gofastcore.HiddenCells{Row: at, Col: col, RowSpan: 1, ColSpan: 1, Hidden: cell.hidden})
						hiddenAll |= cell.hidden
					}
					if cell.colSpan > 1 || cell.rowSpan > 1 {
						spans = append(spans, span{at, col, cell.rowSpan, cell.colSpan})
					}
					col++
				}
			}
			if !r.budget.Take(need) {
				r.stop(textCut)
				cut = true
				// Regions recorded for this row go with it.
				kept := tb.Hidden[:0]
				for _, h := range tb.Hidden {
					if h.Row < at {
						kept = append(kept, h)
					}
				}
				tb.Hidden = kept
				break
			}
			r.cells += int64(width)
			tb.Rows = append(tb.Rows, cells)
			if row.hidden != 0 {
				tb.Hidden = append(tb.Hidden, gofastcore.HiddenCells{Row: at, Col: 0, RowSpan: 1, ColSpan: width, Hidden: row.hidden})
				hiddenAll |= row.hidden
			}
			if row.header && !headerDone {
				tb.HeaderRows++
			} else {
				headerDone = true
			}
		}
		if cut {
			break
		}
	}
	if len(tb.Rows) == 0 {
		return gofastcore.Block{}, false
	}
	if tb.HeaderRows >= len(tb.Rows) {
		tb.HeaderRows = 0
	}
	// Merges, cut to the table as it is, and none over another.
	covered := map[[2]int]bool{}
	for _, sp := range spans {
		if sp.row >= len(tb.Rows) || covered[[2]int{sp.row, sp.col}] {
			continue
		}
		rs, cs := min(sp.rows, len(tb.Rows)-sp.row), min(sp.cols, width-sp.col)
		if rs*cs <= 1 {
			continue
		}
		overlap := false
		for i := sp.row; i < sp.row+rs && !overlap; i++ {
			for j := sp.col; j < sp.col+cs; j++ {
				if covered[[2]int{i, j}] {
					overlap = true
					break
				}
			}
		}
		if overlap {
			continue
		}
		for i := sp.row; i < sp.row+rs; i++ {
			for j := sp.col; j < sp.col+cs; j++ {
				covered[[2]int{i, j}] = true
			}
		}
		tb.Merges = append(tb.Merges, gofastcore.Merge{Row: sp.row, Col: sp.col, RowSpan: rs, ColSpan: cs})
	}
	for j := 0; j < width && j < len(t.cols); j++ {
		if h := t.cols[j]; h != 0 {
			tb.Hidden = append(tb.Hidden, gofastcore.HiddenCells{Row: 0, Col: j, RowSpan: len(tb.Rows), ColSpan: 1, Hidden: h})
			hiddenAll |= h
		}
	}
	return gofastcore.Block{Kind: gofastcore.KindTable, Table: tb, Hidden: hiddenAll}, true
}
