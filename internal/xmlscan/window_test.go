package xmlscan

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// tokens reads a stream through s (reset with Reset, so with a window of
// its own or one reused) and writes down every token and attribute.
func tokens(s *Scanner, doc string, size int) string {
	s.Reset(strings.NewReader(doc), &Options{BufferSize: size})
	var b strings.Builder
	for {
		k := s.Next()
		if k == EOF {
			break
		}
		fmt.Fprintf(&b, "%d %s %q;", k, s.Local(), s.Text())
		for i := range s.NumAttr() {
			_, l, v := s.AttrAt(i)
			fmt.Fprintf(&b, " %s=%q", l, v)
		}
	}
	fmt.Fprintf(&b, " err=%v", s.Err())
	return b.String()
}

// doc is a stream of n elements, each marked with tag, so that a byte left
// from another stream in a reused window would show.
func doc(tag string, n int) string {
	var b strings.Builder
	b.WriteString("<root>")
	for i := range n {
		fmt.Fprintf(&b, `<p id="%s%d">%s text %d &amp; more</p>`, tag, i, tag, i)
	}
	b.WriteString("</root>")
	return b.String()
}

// TestWindowReused reads streams through scanners whose windows come from
// streams read before (of every size class, shorter and longer, and
// from goroutines at once) and checks each reads as with a new scanner.
func TestWindowReused(t *testing.T) {
	sizes := []int{4096, 5000, 16 << 10, 64 << 10}
	want := map[string]string{}
	for _, tag := range []string{"a", "b", "c", "d"} {
		for _, n := range []int{1, 50, 3000} {
			for _, size := range sizes {
				var s Scanner
				want[fmt.Sprint(tag, n, size)] = tokens(&s, doc(tag, n), size)
			}
		}
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var s Scanner
			for i := range 200 {
				tag := string(rune('a' + (g+i)%4))
				n := []int{1, 50, 3000}[(g*7+i)%3]
				size := sizes[(g+i*3)%len(sizes)]
				if got := tokens(&s, doc(tag, n), size); got != want[fmt.Sprint(tag, n, size)] {
					t.Errorf("goroutine %d, stream %d (%s, %d elements, window %d): read differs from a new scanner's", g, i, tag, n, size)
					return
				}
				s.Release()
				if k := s.Next(); k != EOF {
					t.Errorf("a released scanner gave %v, not EOF", k)
					return
				}
			}
		}()
	}
	wg.Wait()
}
