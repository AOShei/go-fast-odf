package odt

import (
	"fmt"
	"math"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
)

// The fixed bounds of this package that can leave content out. Each is
// said once per part it left something out of, as limit.exceeded, with
// how much; a bound that only bounds work loses nothing and is not here.
type capID uint8

const (
	capTableCols capID = iota
	capTableRows
	capSheetCols
	capSheetRows
	capCellValue
	capCellFormula
	capFormula
	capChildren
	capChartRows
	capChartCols
	capChartCell
	capControls
	capOptions
	capDropDown
	capValidations
	capLabel
	capAddress
	capListIDs
	capPictures
)

// maxValue bounds a cell's stored value and its formula, which are
// content: longer than a label, and charged to the text budget.
const maxValue = 64 << 10

type capInfo struct {
	what, unit string
	doc        bool // said on the document: what it lost belongs to no part
}

var caps = [...]capInfo{
	capTableCols:   {what: "table cells past column " + strconv.Itoa(maxCols) + " were not read", unit: "cells"},
	capTableRows:   {what: "repetitions of a table row past " + strconv.Itoa(maxRepeat) + " were not read", unit: "rows"},
	capSheetCols:   {what: "cells past column " + strconv.Itoa(maxSheetCols) + " of the sheet were not read", unit: "cells"},
	capSheetRows:   {what: "rows past row " + strconv.Itoa(maxSheetRows) + " of the sheet were not read", unit: "rows"},
	capCellValue:   {what: "cell values longer than " + strconv.Itoa(maxValue) + " bytes were cut there", unit: "values"},
	capCellFormula: {what: "cell formulas longer than " + strconv.Itoa(maxValue) + " bytes were cut there", unit: "formulas"},
	capFormula:     {what: "formula text longer than " + strconv.Itoa(maxFormula) + " bytes was cut there", unit: "formulas"},
	capChildren:    {what: "embedded documents past the first " + strconv.Itoa(maxChildren) + " were neither extracted nor listed", unit: "documents"},
	capChartRows:   {what: "rows of a chart's table past " + strconv.Itoa(maxChartRows) + " were not read", unit: "rows"},
	capChartCols:   {what: "cells of a chart's table past column " + strconv.Itoa(maxChartCols) + " were not read", unit: "cells"},
	capChartCell:   {what: "cells of a chart's table longer than " + strconv.Itoa(maxFormula) + " bytes were cut there", unit: "cells"},
	capControls:    {what: "form controls past the first " + strconv.Itoa(maxControls) + " were not read", unit: "controls"},
	capOptions:     {what: "options of a form list past the first " + strconv.Itoa(maxOptions) + " were not read", unit: "options"},
	capDropDown:    {what: "values of a drop-down field past the first " + strconv.Itoa(maxOptions) + " were not read", unit: "values"},
	capValidations: {what: "content validations past the first " + strconv.Itoa(maxValidations) + " were not read; the cells that use them have no choices note", unit: "validations", doc: true},
	capLabel:       {what: "labels longer than 512 bytes (note citations, list and heading numbers, axis titles) were cut there", unit: "labels"},
	capAddress:     {what: "link addresses longer than 8192 bytes were cut there", unit: "addresses"},
	capListIDs:     {what: "lists continue one whose identifier or style name was not remembered (past the first " + strconv.Itoa(maxListIDs) + "); their numbering starts again", unit: "lists"},
	capPictures:    {what: "the limit on decompressed bytes was reached; pictures were not read", unit: "pictures"},
}

type capHit struct {
	id capID
	n  int64
}

func (h capHit) warning() gofastcore.Warning {
	c := caps[h.id]
	return gofastcore.Warning{Code: gofastcore.WarnLimitExceeded, Severity: gofastcore.SeverityError,
		Message: fmt.Sprintf("%s (%d %s)", c.what, h.n, c.unit)}
}

// lost records that a bound left n items out. The bounds are kept in the
// order met, so that the warnings come in an order that does not vary.
func (r *reader) lost(id capID, n int64) {
	if n <= 0 {
		return
	}
	for i := range r.hits {
		if r.hits[i].id == id {
			r.hits[i].n = satAdd(r.hits[i].n, n)
			return
		}
	}
	r.hits = append(r.hits, capHit{id: id, n: n})
}

// takeHits takes what the bounds left out of the part just read from
// r.hits, leaving what belongs to the document.
func (r *reader) takeHits() []capHit {
	var own []capHit
	kept := r.hits[:0]
	for _, h := range r.hits {
		if caps[h.id].doc {
			kept = append(kept, h)
		} else {
			own = append(own, h)
		}
	}
	r.hits = kept
	return own
}

// mergeHits adds the counts of b to a, bound by bound, in the order met.
func mergeHits(a, b []capHit) []capHit {
	for _, h := range b {
		found := false
		for i := range a {
			if a[i].id == h.id {
				a[i].n = satAdd(a[i].n, h.n)
				found = true
				break
			}
		}
		if !found {
			a = append(a, h)
		}
	}
	return a
}

// sayLost gives what the bounds left out of the part just read as its
// warnings, once per bound, and forgets it. With part nil it gives all
// that is left, the document's own included, to warn.
func (r *reader) sayLost(part *gofastcore.Part, warn *[]gofastcore.Warning) {
	kept := r.hits[:0]
	for _, h := range r.hits {
		c := caps[h.id]
		if part != nil && c.doc {
			kept = append(kept, h)
			continue
		}
		w := h.warning()
		if part != nil {
			part.Warnings = append(part.Warnings, w)
		} else {
			*warn = append(*warn, w)
		}
	}
	r.hits = kept
}

// satAdd and satMul add and multiply counts that are not negative,
// stopping at the largest int64 rather than wrapping: a file can ask for
// any number of repetitions.
func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func satMul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// label is clean.Label for a label that is content (a note's citation,
// the number of a list item or heading, an axis title): a cut is said.
func (r *reader) label(s string) string {
	v, cut := clean.LabelCut(s)
	if cut {
		r.lost(capLabel, 1)
	}
	return v
}

// address is clean.Address, with a cut said.
func (r *reader) address(s string) string {
	v, cut := clean.AddressCut(s)
	if cut {
		r.lost(capAddress, 1)
	}
	return v
}
