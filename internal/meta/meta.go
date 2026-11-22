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
	// What the bounds left out: values cut at MaxValueBytes, keywords
	// and user-defined properties past MaxKeywords and MaxUserDefined,
	// names (language, generator, authors, property names) cut at the
	// length of a label, addresses cut at the length of an address.
	CutValues, Keywords, UserDefined, CutNames, CutAddresses int
	// Removed counts control characters removed.
	Removed int
	// Refresh is the address an auto-reload element tells a reader to
	// load, or "".
	Refresh string
}

// Bounds on the metadata kept.
const (
	MaxValueBytes  = 64 << 10
	MaxUserDefined = 256
	MaxKeywords    = 256
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
		if space == ns.Office && local != "meta" && local != "document" && local != "document-meta" && s.Depth() <= 2 {
			// In a flat file the metadata is one element among others:
			// what follows it is not metadata.
			if local == "body" {
				break
			}
			s.Skip()
			continue
		}
		switch {
		case space == ns.DC && local == "title":
			m.Title = value(s, res)
		case space == ns.DC && local == "subject":
			m.Subject = value(s, res)
		case space == ns.DC && local == "description":
			extra(m, "description", value(s, res))
		case space == ns.DC && local == "language":
			m.Language = res.label(value(s, res))
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
			m.Application = res.label(value(s, res))
		case space == ns.Meta && local == "keyword":
			if v := value(s, res); v != "" {
				if len(m.Keywords) < MaxKeywords {
					m.Keywords = append(m.Keywords, v)
				} else {
					res.Keywords++
				}
			}
		case space == ns.Meta && local == "editing-cycles":
			extra(m, "revision", value(s, res))
		case space == ns.Meta && local == "editing-duration":
			extra(m, "editing_duration", value(s, res))
		case space == ns.Meta && local == "template":
			href, _ := s.Attr(ns.XLink, "href")
			v, cut := clean.AddressCut(string(href))
			if cut {
				res.CutAddresses++
			}
			extra(m, "template", v)
		case space == ns.Meta && local == "auto-reload":
			href, _ := s.Attr(ns.XLink, "href")
			res.Refresh = clean.Address(string(href))
		case space == ns.Meta && local == "user-defined":
			// The name is copied before the value is read: reading on
			// reuses the scanner's buffer.
			name, _ := s.Attr(ns.Meta, "name")
			raw := string(name)
			v := value(s, res)
			if v == "" || clean.Label(raw) == "" {
				continue
			}
			if users >= MaxUserDefined {
				res.UserDefined++
				continue
			}
			users++
			key := res.name(raw)
			extra(m, "custom:"+key, v)
		}
	}
	switch {
	case initial != "":
		m.Authors = []string{res.name(initial)}
		if creator != "" && creator != initial {
			extra(m, "last_modified_by", res.name(creator))
		}
	case creator != "":
		m.Authors = []string{res.name(creator)}
	}
}

// label is clean.Label, counting a cut.
func (res *Result) label(s string) string {
	v, cut := clean.LabelCut(s)
	if cut {
		res.CutNames++
	}
	return v
}

// name is clean.Name, counting a cut.
func (res *Result) name(s string) string {
	v, cut := clean.NameCut(s)
	if cut {
		res.CutNames++
	}
	return v
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
		if k == xmlscan.Text && len(text) <= MaxValueBytes {
			text = append(text, s.Text()...)
		}
	}
	if len(text) > MaxValueBytes {
		n := MaxValueBytes
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text = text[:n]
		res.CutValues++
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
