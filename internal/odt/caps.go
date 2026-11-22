package odt

import (
	"fmt"
	"math"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/meta"
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
	capInline
	capMetaValue
	capKeywords
	capUserDefined
	capSpaces
)

// maxValue bounds a cell's stored value and its formula, which are
// content: longer than a label, and charged to the text budget.
const maxValue = 64 << 10

type capInfo struct {
	what, unit string
	doc        bool // said on the document: what it lost belongs to no part
	// once: what it lost is lost once for the document, though the
	// content stands on many parts (an embedded document not listed, a
	// picture not read in a master page): in a master it is not said on
	// every slide that shows it.
	once bool
}

var caps = [...]capInfo{
	capTableCols:   {what: "table cells past column " + strconv.Itoa(maxCols) + " were not read", unit: "cells"},
	capTableRows:   {what: "repetitions of a table row past " + strconv.Itoa(maxRepeat) + " were not read", unit: "rows"},
	capSheetCols:   {what: "cells past column " + strconv.Itoa(maxSheetCols) + " of the sheet were not read", unit: "cells"},
	capSheetRows:   {what: "rows past row " + strconv.Itoa(maxSheetRows) + " of the sheet were not read", unit: "rows"},
	capCellValue:   {what: "cell values longer than " + strconv.Itoa(maxValue) + " bytes were cut there", unit: "values"},
	capCellFormula: {what: "cell formulas longer than " + strconv.Itoa(maxValue) + " bytes were cut there", unit: "formulas"},
	capFormula:     {what: "formula text longer than " + strconv.Itoa(maxFormula) + " bytes was cut there", unit: "formulas"},
	capChildren:    {what: "embedded documents past the first " + strconv.Itoa(maxChildren) + " were neither extracted nor listed", unit: "documents", once: true},
	capChartRows:   {what: "rows of a chart's table past " + strconv.Itoa(maxChartRows) + " were not read", unit: "rows"},
	capChartCols:   {what: "cells of a chart's table past column " + strconv.Itoa(maxChartCols) + " were not read", unit: "cells"},
	capChartCell:   {what: "cells of a chart's table longer than " + strconv.Itoa(maxFormula) + " bytes were cut there", unit: "cells"},
	capControls:    {what: "form controls past the first " + strconv.Itoa(maxControls) + " were not read", unit: "controls"},
	capOptions:     {what: "options of a form list past the first " + strconv.Itoa(maxOptions) + " were not read", unit: "options"},
	capDropDown:    {what: "values of a drop-down field past the first " + strconv.Itoa(maxOptions) + " were not read", unit: "values"},
	capValidations: {what: "content validations past the first " + strconv.Itoa(maxValidations) + " were not read; the cells that use them have no choices note", unit: "validations", doc: true},
	capLabel:       {what: "names and labels longer than " + strconv.Itoa(clean.MaxLabel) + " bytes were cut there", unit: "names or labels"},
	capAddress:     {what: "addresses longer than 8192 bytes were cut there", unit: "addresses"},
	capListIDs:     {what: "lists continue one whose identifier or style name was not remembered (past the first " + strconv.Itoa(maxListIDs) + "); their numbering starts again", unit: "lists"},
	capPictures:    {what: "the limit on decompressed bytes was reached; pictures were not read", unit: "pictures", once: true},
	capInline:      {what: "pictures held in the file in more than " + strconv.Itoa(maxInline>>20) + " MiB of base64 were not read", unit: "pictures", once: true},
	capMetaValue:   {what: "metadata values longer than " + strconv.Itoa(meta.MaxValueBytes) + " bytes were cut there", unit: "values", doc: true},
	capKeywords:    {what: "keywords past the first " + strconv.Itoa(meta.MaxKeywords) + " were left out", unit: "keywords", doc: true},
	capUserDefined: {what: "user-defined properties past the first " + strconv.Itoa(meta.MaxUserDefined) + " were left out", unit: "properties", doc: true},
	capSpaces:      {what: "spaces past " + strconv.Itoa(maxSpaces) + " asked for by one text:s were left out", unit: "spaces"},
}

// Lost is what the bounds left out before the document's body was read
// (its metadata and manifest): said on the document, with what the body
// loses that belongs to no part.
type Lost struct {
	MetaValues, Keywords, UserDefined, Labels, Addresses int64
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
			part.Warn(w.Severity, w.Code, w.Message)
		} else {
			// The document's warnings are gathered in Result.Warnings,
			// which the caller gives to Document.Warn.
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

// label is clean.Label for a name or label that reaches the output (a
// note's citation, the number of a list item or heading, an axis title,
// an anchor, an author): a cut is said.
func (r *reader) label(s string) string {
	v, cut := clean.LabelCut(s)
	if cut {
		r.lost(capLabel, 1)
	}
	return v
}

// name is clean.Name, with a cut said.
func (r *reader) name(s string) string {
	v, cut := clean.NameCut(s)
	if cut {
		r.lost(capLabel, 1)
	}
	return v
}

// cutLabel and cutName are clean.Label and clean.Name, counting a cut in
// n, for a label that is said only if what holds it is given.
func cutLabel(s string, n *int64) string {
	v, cut := clean.LabelCut(s)
	if cut {
		*n++
	}
	return v
}

func cutName(s string, n *int64) string {
	v, cut := clean.NameCut(s)
	if cut {
		*n++
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
