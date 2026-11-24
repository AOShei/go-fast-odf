package odpkg

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/AOShei/go-fast-core/inflate"
)

// readMember reads the member's content through zr, as far as it goes,
// and the error that ended it.
func readMember(t *testing.T, zr *zip.Reader, name string) ([]byte, error) {
	t.Helper()
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	t.Fatalf("no member %s", name)
	return nil, nil
}

// TestInflateLikeCompressFlate: a deflated member reads as it did with
// compress/flate, whole, cut off, and damaged at many places: the same
// bytes, and an error where there was one. The CRC-32 is archive/zip's,
// as before.
//
// Damaged members differ in one way, archive/zip's (see
// inflate.Decompressor): what comes before the error, which can be up to
// 32 KiB more than with compress/flate, or at most 4 KiB less. Both
// readings fail, and both outputs are the start of the same bytes.
func TestInflateLikeCompressFlate(t *testing.T) {
	var text bytes.Buffer
	for i := range 4000 {
		fmt.Fprintf(&text, "<text:p text:style-name=\"P%d\">Paragraph %d of the member, long enough to need more than one piece.</text:p>\n", i%40, i)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("content.xml")
	w.Write(text.Bytes())
	zw.Close()
	pkg := buf.Bytes()
	zr, _ := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	off, _ := zr.File[0].DataOffset()
	end := off + int64(zr.File[0].CompressedSize64)

	more, less := 0, 0
	check := func(data []byte, what string) {
		std, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		ours, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		ours.RegisterDecompressor(zip.Deflate, inflate.Decompressor)
		want, wantErr := readMember(t, std, "content.xml")
		got, gotErr := readMember(t, ours, "content.xml")
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("%s: error %v, compress/flate %v", what, gotErr, wantErr)
		}
		if bytes.Equal(got, want) {
			return
		}
		short, long := got, want
		if len(short) > len(long) {
			short, long = long, short
		}
		if gotErr == nil || !bytes.HasPrefix(long, short) || len(want)-len(got) > 4<<10 {
			t.Fatalf("%s: %d bytes (error %v), compress/flate %d (error %v)", what, len(got), gotErr, len(want), wantErr)
		}
		if len(got) > len(want) {
			more++
		} else {
			less++
		}
	}
	check(pkg, "whole")
	for at := off; at < end; at += 1 + (end-off)/400 {
		bad := append([]byte(nil), pkg...)
		bad[at] ^= 0x5a
		check(bad, fmt.Sprintf("byte %d damaged", at))
	}
	// A member whose compressed bytes the archive cuts short.
	for cut := off + 1; cut < end; cut += 1 + (end-off)/50 {
		short := append([]byte(nil), pkg...)
		copy(short[cut:end], make([]byte, end-cut))
		check(short, fmt.Sprintf("zeros from %d", cut))
	}
	t.Logf("damaged members that read differently: %d with more of their text, %d with less", more, less)
}
