package gofastodf

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
)

// TestWindowsReusedConcurrently reads documents of every kind the fuzz
// seeds make, one at a time and then from goroutines at once, many times
// over, so that scanners take windows other documents gave back: each
// must read as it did alone.
func TestWindowsReusedConcurrently(t *testing.T) {
	type input struct {
		mime string
		st   stream
		seed string
	}
	var inputs []input
	for _, s := range textSeeds {
		inputs = append(inputs, input{textMIME, contentStream, s})
	}
	for _, s := range objectSeeds {
		inputs = append(inputs, input{textMIME, contentStream, s})
	}
	for _, s := range stylesSeeds {
		inputs = append(inputs, input{textMIME, stylesStream, s})
	}
	for _, s := range sheetSeeds {
		inputs = append(inputs, input{sheetMIME, contentStream, s})
	}
	for _, s := range slideSeeds {
		inputs = append(inputs, input{slideMIME, contentStream, s})
	}
	files := make([][]byte, len(inputs))
	want := make([]string, len(inputs))
	read := func(b []byte) string {
		doc, err := (&Extractor{}).Extract(context.Background(), bytes.NewReader(b), int64(len(b)), richOpts())
		if err != nil {
			return "error: " + err.Error()
		}
		j, _ := json.Marshal(doc)
		return string(j) + doc.Text()
	}
	for i, in := range inputs {
		files[i] = fuzzPackage(t, in.mime, in.st, []byte(in.seed))
		want[i] = read(files[i])
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range 20 * len(files) {
				i := (k*7 + g) % len(files)
				if got := read(files[i]); got != want[i] {
					t.Errorf("goroutine %d: input %d reads differently from when read alone", g, i)
					return
				}
			}
		}()
	}
	wg.Wait()
}
