package odt

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/num"
	"github.com/AOShei/go-fast-odf/internal/style"
)

// counters are the numbers of a list at each level.
type counters struct {
	n       [10]int
	started [10]bool
}

// next counts an item at level (from 1) and returns the counters.
func (c *counters) next(level, start int) {
	i := min(max(level, 1), 10) - 1
	if !c.started[i] {
		c.n[i] = start
		c.started[i] = true
	} else {
		c.n[i]++
	}
	for j := i + 1; j < 10; j++ {
		c.started[j] = false
	}
}

func (c *counters) restart(level, start int) {
	i := min(max(level, 1), 10) - 1
	c.n[i] = start - 1
	c.started[i] = true
	for j := i + 1; j < 10; j++ {
		c.started[j] = false
	}
}

// listState carries numbering from one list to the next that continues
// it.
type listState struct {
	byID    listMap
	byStyle listMap
	last    *counters
}

func (l *listState) init() {
	l.byID.init()
	l.byStyle.init()
}

// maxListIDs bounds the list identifiers, and the list style names,
// remembered.
const maxListIDs = 1 << 14

// listMap holds the counters list identifiers or style names stand for,
// at most maxListIDs of them. Of the names not kept because it was full
// it keeps a hash (as many again), so that a list that continues one is
// known to have lost its numbering, and one that names a list there never
// was is not taken for one.
type listMap struct {
	m         map[string]*counters
	forgotten map[uint64]bool // hash -> missed and counted already
}

func (l *listMap) init() {
	l.m = map[string]*counters{}
	l.forgotten = map[uint64]bool{}
}

// get returns the counters key stands for, or nil; missed reports that
// key was not kept because the map was full (or, past what is kept of
// the names not kept, may not have been). A name not kept is missed
// once: what continues it after is the same list, counted already.
func (l *listMap) get(key string) (c *counters, missed bool) {
	if c := l.m[key]; c != nil {
		return c, false
	}
	if len(l.m) < maxListIDs {
		return nil, false
	}
	h := hashKey(key)
	said, ok := l.forgotten[h]
	switch {
	case ok && !said:
		l.forgotten[h] = true
		return nil, true
	case ok:
		return nil, false
	}
	return nil, len(l.forgotten) >= maxListIDs
}

// set records the counters key stands for. A key already there is
// updated; a new one past the bound is not kept, and its hash is.
func (l *listMap) set(key string, c *counters) {
	if _, ok := l.m[key]; ok || len(l.m) < maxListIDs {
		l.m[key] = c
		return
	}
	if h := hashKey(key); len(l.forgotten) < maxListIDs {
		if _, ok := l.forgotten[h]; !ok {
			l.forgotten[h] = false // not yet missed
		}
	}
}

func hashKey(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// listCtx is a text:list being read.
type listCtx struct {
	style *style.ListStyle
	name  string
	depth int
	count *counters
}

func (l *listCtx) ordered() bool {
	lv := l.style.Level(l.depth)
	return lv.Kind == 'n' && lv.Format != ""
}

// itemCtx is the list item whose first paragraph is being read.
type itemCtx struct {
	list  *listCtx
	depth int
	start int // a start value the item sets, or -1
	label string
	cut   bool // the label was cut at the length of a label
	done  bool
}

// marker counts the item and returns its label.
func (it *itemCtx) marker() (string, bool) {
	if it.done {
		return it.label, it.list.ordered()
	}
	it.done = true
	l := it.list
	lv := l.style.Level(it.depth)
	switch lv.Kind {
	case 'b':
		it.label, it.cut = clean.LabelCut(lv.Bullet)
		if it.label == "" {
			it.label = "•"
		}
		return it.label, false
	case 'i':
		it.label = "•"
		return it.label, false
	case 'n':
		start := lv.Start
		if it.start >= 0 {
			l.count.restart(it.depth, it.start)
		}
		l.count.next(it.depth, start)
		it.label, it.cut = clean.LabelCut(formatNumber(l.style, l.count, it.depth, lv))
		return it.label, lv.Format != ""
	}
	// No level style: a plain bullet.
	it.label = "•"
	return it.label, false
}

// formatNumber writes the label of an item at depth, as the style gives
// it: the caller makes it a label.
func formatNumber(ls *style.ListStyle, c *counters, depth int, lv style.ListLevel) string {
	if lv.Format == "" {
		return lv.Prefix + lv.Suffix
	}
	first := max(depth-lv.DisplayLevels+1, 1)
	var parts []string
	for d := first; d <= depth; d++ {
		f := ls.Level(d)
		if d < depth && f.Format == "" {
			continue
		}
		i := min(d, 10) - 1
		n := c.n[i]
		if !c.started[i] {
			n = f.Start
		}
		format := f.Format
		if d == depth {
			format = lv.Format
		}
		parts = append(parts, number(n, format, f.LetterSync))
	}
	return lv.Prefix + strings.Join(parts, ".") + lv.Suffix
}

// number writes n in a list numbering format: 1, a, A, i, I. Formats
// this reader does not know are written as arabic numerals.
func number(n int, format string, sync bool) string {
	switch format {
	case "a", "A":
		if n <= 0 {
			return strconv.Itoa(n)
		}
		base := byte('a')
		if format == "A" {
			base = 'A'
		}
		if sync {
			ch := base + byte((n-1)%26)
			return strings.Repeat(string(ch), min((n-1)/26+1, 64))
		}
		var b []byte
		for n > 0 && len(b) < 64 {
			n--
			b = append([]byte{base + byte(n%26)}, b...)
			n /= 26
		}
		return string(b)
	case "i", "I":
		if n <= 0 || n >= 4000 {
			return strconv.Itoa(n)
		}
		vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
		syms := []string{"m", "cm", "d", "cd", "c", "xc", "l", "xl", "x", "ix", "v", "iv", "i"}
		var sb strings.Builder
		for i, v := range vals {
			for n >= v {
				sb.WriteString(syms[i])
				n -= v
			}
		}
		if format == "I" {
			return strings.ToUpper(sb.String())
		}
		return sb.String()
	}
	return strconv.Itoa(n)
}

// list reads text:list.
func (r *reader) list(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	name := r.attr(ns.Text, "style-name")
	l := &listCtx{depth: 1}
	if c.list != nil {
		l.depth = c.list.depth + 1
		l.count = c.list.count
		l.name, l.style = c.list.name, c.list.style
		if name != "" {
			l.name, l.style = name, r.set.List(r.sc, name)
		}
	} else {
		l.name = name
		l.style = r.set.List(r.sc, name)
		cont := r.attr(ns.Text, "continue-list")
		var byID *counters
		missed := false
		if cont != "" {
			byID, missed = r.lists.byID.get(cont)
		}
		switch {
		case byID != nil:
			l.count = byID
		case r.attr(ns.Text, "continue-numbering") == "true":
			var lost bool
			l.count, lost = r.lists.byStyle.get(name)
			missed = missed || lost
			if l.count == nil {
				l.count = r.lists.last
			}
		}
		if missed {
			// The list it continues was not remembered: its numbering
			// starts again (or goes on from the list before).
			r.lost(capListIDs, 1)
		}
		if l.count == nil {
			l.count = &counters{}
		}
		if id := r.attr(xmlscan.SpaceXML, "id"); id != "" {
			r.lists.byID.set(id, l.count)
		}
		r.lists.byStyle.set(name, l.count)
		r.lists.last = l.count
	}
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		if s.Space() != ns.Text {
			r.blockAt(ctxt{hidden: c.hidden, list: l}, dst)
			continue
		}
		switch string(s.Local()) {
		case "list-item":
			start := -1
			if n, err := num.Atoi(strings.TrimSpace(r.attr(ns.Text, "start-value"))); err == nil && n >= 0 {
				start = n
			}
			r.item(c, l, &itemCtx{list: l, depth: l.depth, start: start}, dst)
		case "list-header":
			r.item(c, l, nil, dst)
		default:
			r.blockAt(ctxt{hidden: c.hidden, list: l}, dst)
		}
	}
}

// item reads text:list-item (it set) or text:list-header (it nil: no
// label).
func (r *reader) item(c ctxt, l *listCtx, it *itemCtx, dst *[]gofastcore.Block) {
	s := &r.s
	inner := ctxt{hidden: c.hidden, list: l}
	first := true
	var given string // a text:number the item carries
	var givenCut bool
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch {
		case space == ns.Text && local == "number":
			v, _ := clean.String(s.AppendText(nil), clean.Prose)
			given, givenCut = clean.LabelCut(v)
		case space == ns.Text && (local == "p" || local == "h"):
			var use *itemCtx
			if first {
				first = false
				if it != nil {
					use = it
					if given != "" {
						use.marker() // counted all the same
						use.label, use.cut = given, givenCut
					}
				} else {
					use = &itemCtx{list: l, depth: l.depth, done: true}
				}
			}
			r.paragraph(inner, local == "h", use, dst)
		case space == ns.Text && local == "list":
			r.list(inner, dst)
		default:
			r.blockAt(inner, dst)
		}
	}
}

// numberedParagraph reads text:numbered-paragraph (ODF 1.2): a paragraph
// numbered as an item of a list at text:level.
func (r *reader) numberedParagraph(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	name := r.attr(ns.Text, "style-name")
	level := 1
	if n, err := num.Atoi(strings.TrimSpace(r.attr(ns.Text, "level"))); err == nil && n > 0 {
		level = min(n, 10)
	}
	id := r.attr(ns.Text, "list-id")
	var count *counters
	if id != "" {
		var missed bool
		if count, missed = r.lists.byID.get(id); missed {
			r.lost(capListIDs, 1)
		}
	}
	if count == nil {
		count = &counters{}
		if id != "" {
			r.lists.byID.set(id, count)
		}
	}
	l := &listCtx{style: r.set.List(r.sc, name), name: name, depth: level, count: count}
	it := &itemCtx{list: l, depth: level, start: -1}
	if n, err := num.Atoi(strings.TrimSpace(r.attr(ns.Text, "start-value"))); err == nil && n >= 0 {
		it.start = n
	}
	var given string
	var givenCut bool
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k != xmlscan.Start {
			continue
		}
		switch {
		case s.Space() == ns.Text && string(s.Local()) == "number":
			v, _ := clean.String(s.AppendText(nil), clean.Prose)
			given, givenCut = clean.LabelCut(v)
		case s.Space() == ns.Text && (string(s.Local()) == "p" || string(s.Local()) == "h"):
			if given != "" {
				it.marker()
				it.label, it.cut = given, givenCut
			}
			r.paragraph(c, string(s.Local()) == "h", it, dst)
		default:
			r.blockAt(c, dst)
		}
	}
}

// outlineMarker counts a heading at level and returns its number, if the
// outline style numbers headings.
func (r *reader) outlineMarker(level int) (string, bool) {
	ls := r.set.Outline()
	if ls == nil {
		return "", false
	}
	lv := ls.Level(level)
	if lv.Kind != 'n' {
		return "", false
	}
	r.outline.next(level, lv.Start)
	if lv.Format == "" {
		return clean.LabelCut(lv.Prefix + lv.Suffix)
	}
	return clean.LabelCut(formatNumber(ls, &r.outline, level, lv))
}
