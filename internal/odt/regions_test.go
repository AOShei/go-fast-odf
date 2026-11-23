package odt

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

// findRegionsBefore is findRegions as it was before the column runs were
// merged while read: one run a cell, sorted, then merged.
func findRegionsBefore(cells []sheetCell) []region {
	var out []region
	for i := 0; i < len(cells); {
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

// TestFindRegionsSame checks findRegions against the way it was written
// before, on sheets of cells at random (in row and column order, as a
// sheet gives them, some spanning rows and columns).
func TestFindRegionsSame(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for n := range 20000 {
		rows, cols := 1+rng.IntN(12), 1+rng.IntN(12)
		fill := rng.Float64()
		var cells []sheetCell
		for r := range rows {
			for c := 0; c < cols; c++ {
				if rng.Float64() > fill {
					continue
				}
				cell := sheetCell{row: int32(r), col: int32(c), rows: 1, cols: 1, text: "x"}
				if rng.IntN(6) == 0 {
					cell.rows = int32(1 + rng.IntN(3))
				}
				if rng.IntN(6) == 0 {
					cell.cols = int32(1 + rng.IntN(3))
					c += int(cell.cols) - 1
				}
				cells = append(cells, cell)
			}
		}
		got, want := findRegions(cells), findRegionsBefore(cells)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("sheet %d: regions differ\ngot  %+v\nwant %+v", n, got, want)
		}
	}
}
