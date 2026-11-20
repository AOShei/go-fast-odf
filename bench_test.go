package gofastodf

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkExtract extracts three files of the public corpus's dev set,
// one each of .odt, .ods and .odp, held in memory. It skips when the
// corpus is absent.
func BenchmarkExtract(b *testing.B) {
	root := os.Getenv("GOFAST_CORPUS")
	if root == "" {
		root = filepath.Join("..", ".testdata", "corpus")
	}
	for _, f := range []string{"odt/lo-MadeByLO7.odt", "ods/lo-tdf154005.ods", "odp/tika-LibreOfficeImpress_odp_1.3.odp"} {
		data, err := os.ReadFile(filepath.Join(root, "dev", f))
		if err != nil {
			b.Skip("no corpus file", f)
		}
		b.Run(filepath.Ext(f)[1:], func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Extract(context.Background(), bytes.NewReader(data), int64(len(data)), nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
