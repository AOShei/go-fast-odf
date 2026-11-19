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
//	text.hidden               text a reader cannot see
//	structure.heading_level_clamped
const (
	// WarnUnreadContent: the document holds content of a kind this
	// version does not read (embedded objects, form controls); the
	// message says what and how many.
	WarnUnreadContent = odt.WarnUnreadContent
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
// as the format they are a variant of.
func (e *Extractor) Formats() []string { return []string{"odt"} }

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

// Extract reads size bytes from r and returns the document.
//
// The format is decided from the package itself (its mimetype member,
// its manifest, or the body of its content), never from a file name. An
// error wraps one of the gofastcore errors:
//
//   - ErrMalformed: not a zip archive, or a package too damaged to read.
//   - ErrUnsupported: a zip archive that is not an OpenDocument package;
//     an OpenDocument file of a kind this version does not read
//     (spreadsheets, presentations and drawings are not read yet);
//     an encrypted file when a password is given (decryption is not
//     implemented yet).
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
	pkg, err := odpkg.Open(r, size, odpkg.Limits{MaxDecodedBytes: allowed, MaxMembers: members})
	if err != nil {
		return nil, wrap(err)
	}
	defer func() { decode.Take(allowed - pkg.Remaining()) }()

	mime := pkg.MIME()
	format, known := kinds[mime]
	switch {
	case !known && mime != "" && !pkg.Has("content.xml"):
		return nil, fmt.Errorf("gofastodf: package of type %s: %w", clean.Label(mime), gofastcore.ErrUnsupported)
	case known && format != "odt":
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
	if mime == "" {
		mime = "application/vnd.oasis.opendocument.text"
	}

	// The parts: the body is 1, the stored part 2 and only with the
	// whole document.
	whole := o.Parts == (gofastcore.PartRange{})
	if !whole && !o.Parts.Contains(1) {
		return nil, fmt.Errorf("gofastodf: a text document has 1 part, numbered 1: %w", gofastcore.ErrNoPartsSelected)
	}

	doc := gofastcore.New("odt")
	doc.Source = gofastcore.Source{Format: "odt", MIME: mime, Name: o.Name, Size: size}
	doc.HiddenChecked = odt.HiddenChecked
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
	if hiddenInMetadata(&doc.Metadata) {
		warn(gofastcore.SeverityWarning, gofastcore.WarnTextHidden, "document properties contain invisible characters")
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
	if res.NotText != "" {
		return nil, fmt.Errorf("gofastodf: the document's body is office:%s, which this version does not read: %w", res.NotText, gofastcore.ErrUnsupported)
	}

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

	doc.Parts = append(doc.Parts, res.Body)
	if res.Stored != nil {
		switch {
		case !whole:
		case o.Limits.MaxParts == 1:
			warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "2 parts were asked for; the first 1 is given")
		default:
			doc.Parts = append(doc.Parts, *res.Stored)
		}
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

// hiddenInMetadata reports whether any property value holds invisible
// characters. Properties cannot carry a block flag, so the finding becomes
// a document-level warning.
func hiddenInMetadata(m *gofastcore.Metadata) bool {
	found := func(s string) bool { return gofastcore.InvisibleChars(s) != nil }
	if found(m.Title) || found(m.Subject) || found(m.Language) || found(m.Application) {
		return true
	}
	for _, s := range m.Authors {
		if found(s) {
			return true
		}
	}
	for _, s := range m.Keywords {
		if found(s) {
			return true
		}
	}
	for k, v := range m.Extra {
		if found(k) || found(v) {
			return true
		}
	}
	return false
}
