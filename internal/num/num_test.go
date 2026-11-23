package num

import (
	"strconv"
	"testing"
)

// TestSameAsStrconv: every string parses as strconv parses it, and only
// "" fails with ErrEmpty.
func TestSameAsStrconv(t *testing.T) {
	for _, s := range []string{"", " ", "0", "7", "-3", "+4", "x", "12a", "99999999999999999999", "1_000", "0x10"} {
		n, err := Atoi(s)
		wn, werr := strconv.Atoi(s)
		if n != wn || (err == nil) != (werr == nil) || s == "" && err != ErrEmpty || s != "" && err == ErrEmpty {
			t.Errorf("Atoi(%q) = %d, %v; strconv %d, %v", s, n, err, wn, werr)
		}
		m, err := ParseInt(s, 10, 64)
		wm, werr := strconv.ParseInt(s, 10, 64)
		if m != wm || (err == nil) != (werr == nil) {
			t.Errorf("ParseInt(%q) = %d, %v; strconv %d, %v", s, m, err, wm, werr)
		}
	}
	if a := testing.AllocsPerRun(100, func() { Atoi("") }); a != 0 {
		t.Errorf("Atoi(\"\") allocates %v", a)
	}
}
