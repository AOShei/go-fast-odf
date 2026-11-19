// Package meta reads an OpenDocument meta.xml stream into the shared
// metadata.
package meta

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/odpkg"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// Result is what was read.
type Result struct {
	gofastcore.Metadata
	// Damaged says the stream could not be read to its end; what was read
	// before is kept.
	Damaged bool
	// Budget says the decompression limit was reached while reading it.
	Budget bool
	// Truncated counts values cut to maxValueBytes and user-defined
	// properties left out beyond maxUserDefined.
	Truncated int
	// Removed counts control characters removed.
	Removed int
	// Refresh is the address an auto-reload element tells a reader to
	// load, or "".
	Refresh string
}

const (
	maxValueBytes  = 64 << 10
	maxUserDefined = 256
	maxKeywords    = 256
)

// Read reads meta.xml from the package. A package without one has no
// metadata, which is not an error.
func Read(pkg *odpkg.Package) Result {
	var res Result
	if !pkg.Has("meta.xml") {
		return res
	}
	var s xmlscan.Scanner
	c, err := pkg.Scan("meta.xml", &s)
	if err != nil {
		res.Damaged = !errors.Is(err, odpkg.ErrEncrypted)
		res.Budget = errors.Is(err, odpkg.ErrBudget)
		return res
	}
	defer c.Close()
	ReadFrom(&s, &res)
	return res
}

// ReadFrom reads the office:meta element wherever s finds it: in meta.xml,
// or inside a flat XML document.
func ReadFrom(s *xmlscan.Scanner, res *Result) {
	m := &res.Metadata
	var initial, creator string
	users := 0
	for {
		k := s.Next()
		if k == xmlscan.EOF {
			if err := s.Err(); err != nil {
				res.Damaged = true
				res.Budget = errors.Is(err, odpkg.ErrBudget)
			}
			break
		}
		if k == xmlscan.End && s.Space() == ns.Office && string(s.Local()) == "meta" {
			break
		}
		if k != xmlscan.Start {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch {
		case space == ns.DC && local == "title":
			m.Title = value(s, res)
		case space == ns.DC && local == "subject":
			m.Subject = value(s, res)
		case space == ns.DC && local == "description":
			extra(m, "description", value(s, res))
		case space == ns.DC && local == "language":
			m.Language = clean.Label(value(s, res))
		case space == ns.DC && local == "creator":
			creator = value(s, res)
		case space == ns.Meta && local == "initial-creator":
			initial = value(s, res)
		case space == ns.DC && local == "date":
			m.Modified = parseTime(value(s, res))
		case space == ns.Meta && local == "creation-date":
			m.Created = parseTime(value(s, res))
		case space == ns.Meta && local == "print-date":
			extra(m, "print_date", value(s, res))
		case space == ns.Meta && local == "printed-by":
			extra(m, "printed_by", value(s, res))
		case space == ns.Meta && local == "generator":
			m.Application = clean.Label(value(s, res))
		case space == ns.Meta && local == "keyword":
			if v := value(s, res); v != "" {
				if len(m.Keywords) < maxKeywords {
					m.Keywords = append(m.Keywords, v)
				} else {
					res.Truncated++
				}
			}
		case space == ns.Meta && local == "editing-cycles":
			extra(m, "revision", value(s, res))
		case space == ns.Meta && local == "editing-duration":
			extra(m, "editing_duration", value(s, res))
		case space == ns.Meta && local == "template":
			href, _ := s.Attr(ns.XLink, "href")
			extra(m, "template", clean.Address(string(href)))
		case space == ns.Meta && local == "auto-reload":
			href, _ := s.Attr(ns.XLink, "href")
			res.Refresh = clean.Address(string(href))
		case space == ns.Meta && local == "user-defined":
			name, _ := s.Attr(ns.Meta, "name")
			key := clean.Name(string(name))
			v := value(s, res)
			if key == "" || v == "" {
				continue
			}
			if users >= maxUserDefined {
				res.Truncated++
				continue
			}
			users++
			extra(m, "custom:"+key, v)
		}
	}
	switch {
	case initial != "":
		m.Authors = []string{clean.Name(initial)}
		if creator != "" && creator != initial {
			extra(m, "last_modified_by", clean.Name(creator))
		}
	case creator != "":
		m.Authors = []string{clean.Name(creator)}
	}
}

// value reads the text of the current element, bounded, cleaned.
func value(s *xmlscan.Scanner, res *Result) string {
	var text []byte
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k == xmlscan.Text && len(text) <= maxValueBytes {
			text = append(text, s.Text()...)
		}
	}
	if len(text) > maxValueBytes {
		n := maxValueBytes
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text = text[:n]
		res.Truncated++
	}
	v, removed := clean.String(text, clean.Prose)
	res.Removed += removed
	return v
}

func extra(m *gofastcore.Metadata, key, v string) {
	if v == "" {
		return
	}
	if m.Extra == nil {
		m.Extra = map[string]string{}
	}
	m.Extra[key] = v
}

// parseTime reads an xsd:dateTime or xsd:date. Without a zone the time is
// taken as UTC, since the file does not say where it was written.
func parseTime(v string) time.Time {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}
