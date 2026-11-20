package gofastodf

import (
	"runtime"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Paragraphs nested in paragraphs (a text box in a frame anchored in a
// paragraph) each grow their own buffer before anything is charged. The
// text they hold together, and the white space thrown away when a
// paragraph is trimmed, are bounded by the text budget.
func TestNestedParagraphBuffersBounded(t *testing.T) {
	const levels = 20
	fill := strings.Repeat(`<text:s text:c="65536"/>`, 32) // 2 MiB of spaces
	var sb strings.Builder
	for range levels {
		sb.WriteString(`<text:p>` + fill + `<draw:frame><draw:text-box>`)
	}
	for range levels {
		sb.WriteString(`</draw:text-box></draw:frame></text:p>`)
	}
	sb.WriteString(`<text:p>after</text:p>`)
	data := textDoc(t, "", sb.String())
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 2 << 20}})
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 24<<20 {
		t.Errorf("allocated %d MiB with a text budget of 2 MiB", got>>20)
	}
}

// White space that a paragraph is trimmed of was still written: a file of
// paragraphs of nothing but text:s must not write without bound.
func TestTrimmedSpacesCharged(t *testing.T) {
	data := textDoc(t, "", strings.Repeat(`<text:p>`+strings.Repeat(`<text:s text:c="65536"/>`, 16)+`</text:p>`, 64))
	doc := extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 4 << 20}})
	if !doc.Parts[0].Has(gofastcore.WarnLimitExceeded) {
		t.Error("64 MiB of spaces written with a 4 MiB budget and no limit warning")
	}
}
