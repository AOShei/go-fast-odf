package gofastodf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/meta"
	"github.com/AOShei/go-fast-odf/internal/odpkg"
	"github.com/AOShei/go-fast-odf/internal/odt"
	"github.com/AOShei/go-fast-odf/internal/style"
)

// Defaults for the limits of gofastcore.Options.Limits, used where a limit
// is zero. A negative limit means no limit.
const (
	// DefaultMaxInputBytes bounds the size of the file.
	DefaultMaxInputBytes = 1 << 30
	// DefaultMaxDecodedBytes bounds the decompressed bytes read from the
	// archive, over all its members. It is what stops a decompression
	// bomb.
	DefaultMaxDecodedBytes = 2 << 30
	// DefaultMaxArchiveMembers bounds the number of files in the archive.
	DefaultMaxArchiveMembers = 100_000
	// DefaultMaxCells bounds the table cells kept from a document. Each
	// cell kept costs memory whatever the size of the file; repetition
	// attributes let a few bytes ask for millions.
	DefaultMaxCells = 20_000_000
)

// Warning codes specific to this library. Each states a fact about the
// file. Of the codes shared by all go-fast libraries this library raises,
// besides those about parts and limits:
//
//	input.active_content      the package holds macros (Basic or other
//	                          scripts), event handlers, a link that runs
//	                          a macro, or an applet
//	input.external_reference  a linked picture, a section linked to
//	                          another file, a plug-in or floating frame,
//	                          an instruction to reload from an address
//	input.repaired            the archive's directory could not be read;
//	                          the members were found by their own headers
//	input.ambiguous           the archive names a member twice, in two
//	                          ways, or has two members with one name
//	text.tracked_changes      the document has tracked changes
//	structure.heading_level_clamped
//
// What the file declares about display is on the blocks, spans, table
// regions and parts (gofastcore.Display), not in warnings.
const (
	// WarnUnreadContent: the document holds content of a kind this
	// version does not read (embedded objects, form controls); the
	// message says what and how many.
	WarnUnreadContent = odt.WarnUnreadContent
	// WarnRegionSplit: an area of a sheet whose cells are spread so thinly
	// that the rectangle around them is mostly empty was not written as
	// one table; each run of cells side by side in a row is a table of
	// one row instead.
	WarnRegionSplit = odt.WarnRegionSplit
	// WarnPartDamaged: a supporting stream (manifest, styles, metadata)
	// could not be read to its end; what was read is used.
	WarnPartDamaged = odt.WarnPartDamaged
	// WarnPropertyTruncated: a metadata value was longer than 64 KiB and
	// was cut there, or there were more user-defined properties or
	// keywords than are kept (256 each) and the rest were left out.
	WarnPropertyTruncated = "odf.property_truncated"
	// WarnControlCharsRemoved: control characters with no meaning as text
	// were removed from extracted strings.
	WarnControlCharsRemoved = "odf.control_characters_removed"
)

// Extractor extracts OpenDocument files. The zero value is ready to use
// and safe for concurrent use. Its fields are the settings only this
// library has; everything else is in gofastcore.Options.
type Extractor struct {
	// MaxArchiveMembers bounds the number of files in the zip archive.
	// Zero means DefaultMaxArchiveMembers; negative means no limit.
	MaxArchiveMembers int
	// MaxCells bounds the table cells kept from a document. A table that
	// reaches it is cut off there, with a limit.exceeded warning. Zero
	// means DefaultMaxCells; negative means no limit.
	MaxCells int64
}

var _ gofastcore.Extractor = (*Extractor)(nil)

// Formats lists the formats this library extracts, as they appear in
// gofastcore.Source.Format. Templates and master documents are reported
// as the format they are a variant of; flat XML files as fodt, fods and
// fodp.
func (e *Extractor) Formats() []string { return []string{"odt", "ods", "odp", "fodt", "fods", "fodp"} }

// Extract reads an OpenDocument file with default settings. It is
// (&Extractor{}).Extract.
func Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).Extract(ctx, r, size, opts)
}

// ExtractFile opens the named file and extracts it with default settings.
func ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).ExtractFile(ctx, path, opts)
}

// ExtractBytes extracts a file held in memory with default settings.
func ExtractBytes(ctx context.Context, data []byte, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).Extract(ctx, bytes.NewReader(data), int64(len(data)), opts)
}

// ExtractFile opens the named file and extracts it. The file name is
// recorded as the document's source name unless opts.Name is set.
func (e *Extractor) ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var o gofastcore.Options
	if opts != nil {
		o = *opts
	}
	if o.Name == "" {
		o.Name = path
	}
	return e.Extract(ctx, f, info.Size(), &o)
}

// kinds maps the media types of OpenDocument to formats.
var kinds = map[string]string{
	"application/vnd.oasis.opendocument.text":                  "odt",
	"application/vnd.oasis.opendocument.text-template":         "odt",
	"application/vnd.oasis.opendocument.text-master":           "odt",
	"application/vnd.oasis.opendocument.text-master-template":  "odt",
	"application/vnd.oasis.opendocument.text-web":              "odt",
	"application/vnd.oasis.opendocument.spreadsheet":           "ods",
	"application/vnd.oasis.opendocument.spreadsheet-template":  "ods",
	"application/vnd.oasis.opendocument.presentation":          "odp",
	"application/vnd.oasis.opendocument.presentation-template": "odp",
	"application/vnd.oasis.opendocument.graphics":              "odg",
	"application/vnd.oasis.opendocument.graphics-template":     "odg",
	"application/vnd.oasis.opendocument.chart":                 "odc",
	"application/vnd.oasis.opendocument.formula":               "odf",
	"application/vnd.oasis.opendocument.image":                 "odi",
	"application/vnd.oasis.opendocument.database":              "odb",
	"application/vnd.sun.xml.writer":                           "sxw",
	"application/vnd.oasis.opendocument.spreadsheet-flat-xml":  "fods",
	"application/vnd.oasis.opendocument.text-flat-xml":         "fodt",
	"application/vnd.oasis.opendocument.presentation-flat-xml": "fodp",
	"application/vnd.oasis.opendocument.graphics-flat-xml":     "fodg",
}

// read are the formats this version reads.
var read = map[string]bool{"odt": true, "ods": true, "odp": true, "fodt": true, "fods": true, "fodp": true}

// mimeOf gives the media type of a format, for a file that does not say.
var mimeOf = map[string]string{
	"odt":  "application/vnd.oasis.opendocument.text",
	"ods":  "application/vnd.oasis.opendocument.spreadsheet",
	"odp":  "application/vnd.oasis.opendocument.presentation",
	"fodt": "application/vnd.oasis.opendocument.text-flat-xml",
	"fods": "application/vnd.oasis.opendocument.spreadsheet-flat-xml",
	"fodp": "application/vnd.oasis.opendocument.presentation-flat-xml",
}

// Extract reads size bytes from r and returns the document.
//
// The input is a package (a zip archive) or a flat file (one XML
// document). The format is decided from the file itself (its mimetype
// member, its manifest, the office:mimetype of a flat file, or the body of
// its content), never from a file name. An error wraps one of the
// gofastcore errors:
//
//   - ErrMalformed: neither a zip archive nor XML, or a package too
//     damaged to read.
//   - ErrUnsupported: a zip archive or XML document that is not
//     OpenDocument; an OpenDocument file of a kind this version does not
//     read (drawings, charts, formulas, databases); an encrypted file
//     when a password is given (decryption is not implemented yet).
//   - ErrPasswordRequired: the file is encrypted and Options.Password is
//     empty.
//   - ErrLimitExceeded: the input is larger than Limits.MaxInputBytes, or
//     a limit was reached before anything could be read.
//   - ErrNoPartsSelected: Options.Parts selects none of the parts.
func (e *Extractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var o gofastcore.Options
	if opts != nil {
		o = *opts
	}
	if r == nil {
		return nil, fmt.Errorf("gofastodf: no input to read: %w", gofastcore.ErrMalformed)
	}
	if limit := gofastcore.ResolveLimit(o.Limits.MaxInputBytes, DefaultMaxInputBytes); size > limit {
		return nil, fmt.Errorf("gofastodf: input is %d bytes, limit is %d: %w", size, limit, gofastcore.ErrLimitExceeded)
	}
	if size < 4 {
		return nil, fmt.Errorf("gofastodf: input is %d bytes: %w", size, gofastcore.ErrMalformed)
	}
	ctx, decode := gofastcore.WithDecodeBudget(ctx, o.Limits, DefaultMaxDecodedBytes)
	defer decode.Close()
	ctx, text := gofastcore.WithTextBudget(ctx, o.Limits)
	defer text.Close()

	members := int(gofastcore.ResolveLimit(int64(e.MaxArchiveMembers), DefaultMaxArchiveMembers))
	allowed := max(decode.Left(), 0)
	limits := odpkg.Limits{MaxDecodedBytes: allowed, MaxMembers: members}
	pkg, err := odpkg.Open(r, size, limits)
	if errors.Is(err, odpkg.ErrNotZip) {
		// Not an archive: perhaps a flat file, one XML document.
		if flat, ferr := odpkg.OpenFlat(r, size, limits); ferr == nil {
			pkg, err = flat, nil
		} else if errors.Is(ferr, odpkg.ErrNotPackage) {
			err = ferr
		}
	}
	if err != nil {
		return nil, wrap(err)
	}
	defer pkg.Sync(decode)

	mime := pkg.MIME()
	format, known := kinds[mime]
	switch {
	case !known && mime != "" && !pkg.Has("content.xml"):
		return nil, fmt.Errorf("gofastodf: package of type %s: %w", clean.Label(mime), gofastcore.ErrUnsupported)
	case known && !read[format]:
		return nil, fmt.Errorf("gofastodf: %s files are not read by this version: %w", format, gofastcore.ErrUnsupported)
	}
	if encrypted(pkg) {
		if o.Password == "" {
			return nil, fmt.Errorf("gofastodf: the document is encrypted: %w", gofastcore.ErrPasswordRequired)
		}
		return nil, fmt.Errorf("gofastodf: the document is encrypted, and decryption is not implemented: %w", gofastcore.ErrUnsupported)
	}
	if !pkg.Has("content.xml") {
		if pkg.Recovered() != nil {
			return nil, wrap(odpkg.ErrCutOff)
		}
		return nil, fmt.Errorf("gofastodf: the package has no content.xml: %w", gofastcore.ErrMalformed)
	}

	doc := gofastcore.New("odt")
	md := meta.Read(pkg)
	doc.Metadata = md.Metadata
	if pkg.Encrypted("meta.xml") {
		doc.Metadata.Encrypted = true
	}
	warn := func(sev gofastcore.Severity, code, msg string) { doc.Warn(sev, code, msg) }
	if rec := pkg.Recovered(); rec != nil {
		msg := fmt.Sprintf("the archive's directory cannot be read: the file is cut short or damaged. %d members were found by their own headers", rec.Members)
		if rec.Cut != "" {
			msg += "; the last, " + clean.Label(rec.Cut) + ", breaks off"
		} else {
			msg += "; any that followed are lost"
		}
		warn(gofastcore.SeverityWarning, gofastcore.WarnInputRepaired, msg)
	}
	if n := pkg.Ambiguous(); n > 0 {
		warn(gofastcore.SeverityWarning, gofastcore.WarnInputAmbiguous, fmt.Sprintf("%d archive members are named twice or share a name; programs differ in which they read", n))
	}
	if pkg.ManifestDamaged() {
		warn(gofastcore.SeverityWarning, WarnPartDamaged, "META-INF/manifest.xml could not be read to its end")
	}
	switch {
	case md.Budget:
		warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading meta.xml")
	case md.Damaged:
		warn(gofastcore.SeverityWarning, WarnPartDamaged, "meta.xml could not be read to its end; the properties read before are kept")
	}
	if md.Truncated > 0 {
		warn(gofastcore.SeverityWarning, WarnPropertyTruncated, fmt.Sprintf("%d values", md.Truncated))
	}
	if md.Removed > 0 {
		warn(gofastcore.SeverityInfo, WarnControlCharsRemoved, fmt.Sprintf("%d in document properties", md.Removed))
	}
	external := 0
	if md.Refresh != "" {
		external++
	}

	set := style.New()
	if err := set.ReadStyles(pkg); err != nil {
		if errors.Is(err, odpkg.ErrBudget) {
			warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading styles.xml")
		} else {
			warn(gofastcore.SeverityWarning, WarnPartDamaged, "styles.xml could not be read to its end; the styles read before are used")
		}
	}
	if set.Truncated {
		warn(gofastcore.SeverityWarning, WarnPartDamaged, "the document defines more styles than are read; the rest are not used")
	}

	res, err := odt.Read(ctx, pkg, set, &o, odt.Config{MaxCells: gofastcore.ResolveLimit(e.MaxCells, DefaultMaxCells)})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, wrap(err)
	}
	byBody := map[string]string{"text": "odt", "": "odt", "spreadsheet": "ods", "presentation": "odp"}
	if !known {
		f, ok := byBody[res.Kind]
		if !ok {
			return nil, fmt.Errorf("gofastodf: the document's body is office:%s, which this version does not read: %w", res.Kind, gofastcore.ErrUnsupported)
		}
		format = f
		if pkg.Flat() {
			format = "f" + f
		}
		mime = mimeOf[format]
	} else if res.Kind == "" && len(res.Parts) == 1 {
		// No body: one empty part of the kind the format has.
		switch strings.TrimPrefix(format, "f") {
		case "ods":
			res.Parts[0].Kind = gofastcore.PartSheet
		case "odp":
			res.Parts[0].Kind = gofastcore.PartSlide
		}
	} else if strings.TrimPrefix(format, "f") != byBody[res.Kind] {
		return nil, fmt.Errorf("gofastodf: a %s file whose body is office:%s: %w", format, res.Kind, gofastcore.ErrMalformed)
	}
	if pkg.Flat() {
		// A flat file names the kind of document it holds, which is that
		// of a package; it is reported as the flat format.
		format = "f" + strings.TrimPrefix(format, "f")
		mime = mimeOf[format]
	}
	doc.Source = gofastcore.Source{Format: format, MIME: mime, Name: o.Name, Size: size}

	active := res.ActiveContent + set.Scripts
	for _, name := range pkg.Names() {
		if strings.HasPrefix(name, "Basic/") || strings.HasPrefix(name, "Scripts/") {
			active++
		}
	}
	if active > 0 {
		warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent, "the document holds macros, scripts or event handlers; none of it is read or run")
	}
	for _, w := range res.Warnings {
		if w.Code == gofastcore.WarnInputExternalReference && external > 0 {
			w.Message += "; and an instruction to reload the document from an address"
			external = 0
		}
		doc.Warnings = append(doc.Warnings, w)
	}
	if external > 0 {
		warn(gofastcore.SeverityInfo, gofastcore.WarnInputExternalReference, "the document's metadata tells a reader to reload it from an address; nothing was fetched")
	}

	// The parts Options.Parts selects; the stored part comes only with
	// the whole document.
	whole := o.Parts == (gofastcore.PartRange{})
	for _, p := range res.Parts {
		if o.Parts.Contains(p.Number) {
			doc.Parts = append(doc.Parts, p)
		}
	}
	doc.Children = res.Children
	if whole && res.Stored != nil {
		doc.Parts = append(doc.Parts, *res.Stored)
	}
	if len(doc.Parts) == 0 {
		return nil, fmt.Errorf("gofastodf: the document has %d parts, numbered from 1: %w", len(res.Parts), gofastcore.ErrNoPartsSelected)
	}
	if limit := o.Limits.MaxParts; limit > 0 && len(doc.Parts) > limit {
		warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, fmt.Sprintf("%d parts were asked for; the first %d are given", len(doc.Parts), limit))
		doc.Parts = doc.Parts[:limit]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return doc, nil
}

// encrypted reports whether the document's content is encrypted: its
// content stream is, or it has none and some member is (an encrypted
// package within the package).
func encrypted(pkg *odpkg.Package) bool {
	if pkg.Encrypted("content.xml") {
		return true
	}
	if pkg.Has("content.xml") {
		return false
	}
	for _, name := range pkg.Names() {
		if pkg.Encrypted(name) {
			return true
		}
	}
	return false
}

// wrap turns a package error into one that wraps the matching shared
// error.
func wrap(err error) error {
	shared := gofastcore.ErrMalformed
	switch {
	case errors.Is(err, odpkg.ErrNotPackage):
		shared = gofastcore.ErrUnsupported
	case errors.Is(err, odpkg.ErrBudget), errors.Is(err, odpkg.ErrTooManyParts):
		shared = gofastcore.ErrLimitExceeded
	case errors.Is(err, odpkg.ErrEncrypted):
		shared = gofastcore.ErrPasswordRequired
	}
	return fmt.Errorf("gofastodf: %s: %w", clean.Label(err.Error()), shared)
}
