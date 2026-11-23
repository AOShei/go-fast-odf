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
//	child.failed              an embedded file could not be taken out
//	                          of the package to be handed on
//	structure.heading_level_clamped
//
// What the file declares about display is on the blocks, spans, table
// regions and parts (gofastcore.Display), not in warnings.
const (
	// WarnUnreadContent was odf.unread_content, raised when the document holds content of a kind this version does not read.
	//
	// Deprecated: the event is go-fast-core's shared code input.unread_content
	// (gofastcore.WarnInputUnreadContent) since v0.2.0; this name has that value.
	WarnUnreadContent = gofastcore.WarnInputUnreadContent
	// WarnRegionSplit was odf.region_split, raised when a sparse area of a sheet was written as one-row tables.
	//
	// Deprecated: the event is go-fast-core's shared code table.region_split
	// (gofastcore.WarnTableRegionSplit) since v0.2.0; this name has that value.
	WarnRegionSplit = gofastcore.WarnTableRegionSplit
	// WarnFormulaNoValue was odf.formula_no_value, raised when formula cells have no stored result.
	//
	// Deprecated: the event is go-fast-core's shared code table.formula_no_value
	// (gofastcore.WarnTableFormulaNoValue) since v0.2.0; this name has that value.
	WarnFormulaNoValue = gofastcore.WarnTableFormulaNoValue
	// WarnPartDamaged was odf.part_damaged, raised when a supporting stream (manifest, styles, metadata) could not be read to its end.
	//
	// Deprecated: the event is go-fast-core's shared code input.stream_damaged
	// (gofastcore.WarnInputStreamDamaged) since v0.2.0; this name has that value.
	WarnPartDamaged = gofastcore.WarnInputStreamDamaged
	// WarnPropertyTruncated was raised by v0.1.1 and earlier when a
	// metadata value was cut at 64 KiB or keywords or user-defined
	// properties past 256 were left out.
	//
	// Deprecated: these cuts are reported as gofastcore.WarnLimitExceeded,
	// as every bound that cuts output is; this code is no longer raised.
	WarnPropertyTruncated = "odf.property_truncated"
	// WarnControlCharsRemoved was odf.control_characters_removed, raised when control characters with no meaning as text were removed.
	//
	// Deprecated: the event is go-fast-core's shared code text.control_characters_removed
	// (gofastcore.WarnTextControlCharactersRemoved) since v0.2.0; this name has that value.
	WarnControlCharsRemoved = gofastcore.WarnTextControlCharactersRemoved
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
	"application/vnd.sun.xml.writer.template":                  "stw",
	"application/vnd.sun.xml.writer.global":                    "sxg",
	"application/vnd.sun.xml.calc":                             "sxc",
	"application/vnd.sun.xml.calc.template":                    "stc",
	"application/vnd.sun.xml.impress":                          "sxi",
	"application/vnd.sun.xml.impress.template":                 "sti",
	"application/vnd.sun.xml.draw":                             "sxd",
	"application/vnd.sun.xml.draw.template":                    "std",
	"application/vnd.sun.xml.math":                             "sxm",
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
//     read (drawings, charts, formulas, images, databases, the
//     OpenOffice.org 1 formats sxw, sxc, sxi, sxd, sxm, sxg and their
//     templates); a file whose media type is not OpenDocument's and whose
//     content.xml has no OpenDocument body; an encryption this version does not decrypt
//     (an algorithm, key derivation or checksum other than AES-CBC,
//     AES-GCM, Blowfish CFB, PBKDF2, Argon2id, SHA-1 and SHA-256).
//   - ErrPasswordRequired: the file is encrypted and Options.Password is
//     empty.
//   - ErrWrongPassword: the file is encrypted and its checksum or
//     authentication tag does not match Options.Password.
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
		pkg.SetPassword(o.Password)
		pkg.SetContext(ctx)
		if outer, ok := ctx.Value(innerKey{}).(*odpkg.Package); ok {
			pkg.ShareWork(outer)
		}
		if !pkg.Has("content.xml") && pkg.Encrypted(encryptedPackage) {
			return e.extractWhole(ctx, pkg, decode, size, &o)
		}
		if pkg.Has("content.xml") {
			// The password is checked on the content first, so that a
			// wrong one is an error rather than a damaged stream.
			if err := pkg.Check("content.xml"); err != nil {
				return nil, wrap(err)
			}
		}
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
	warn := func(sev gofastcore.Severity, code, msg string) { doc.Warn(sev, code, msg) }
	if outer, ok := ctx.Value(innerKey{}).(*odpkg.Package); ok {
		// The whole package was encrypted (ODF 1.4); this is what it
		// decrypted to.
		doc.Metadata.Encrypted = true
		warn(gofastcore.SeverityInfo, gofastcore.WarnInputDecrypted,
			"the whole package was encrypted with "+outer.Scheme(encryptedPackage)+", and was decrypted with the password given")
	} else if encrypted(pkg) || pkg.Encrypted("meta.xml") {
		doc.Metadata.Encrypted = true
		name := "content.xml"
		if !pkg.Encrypted(name) {
			name = "meta.xml"
		}
		warn(gofastcore.SeverityInfo, gofastcore.WarnInputDecrypted,
			"the package's members were encrypted with "+pkg.Scheme(name)+", and were decrypted with the password given")
	}
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
		warn(gofastcore.SeverityWarning, gofastcore.WarnInputStreamDamaged, "META-INF/manifest.xml could not be read to its end")
	}
	if pkg.ManifestFull() {
		warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, fmt.Sprintf("META-INF/manifest.xml has more than %d entries; the rest were not read", odpkg.MaxManifestEntries))
	}
	switch {
	case md.Budget:
		warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading meta.xml")
	case md.Damaged:
		warn(gofastcore.SeverityWarning, gofastcore.WarnInputStreamDamaged, "meta.xml could not be read to its end; the properties read before are kept")
	}
	if md.Removed > 0 {
		warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, controlsMessage(md.Removed, "the document properties"))
	}
	external := 0
	if md.Refresh != "" {
		external++
	}

	// The styles are read and used only while the document is: nothing
	// in it refers to them.
	set := style.Get()
	defer set.Release()
	if err := set.ReadStyles(pkg); err != nil {
		if errors.Is(err, odpkg.ErrBudget) {
			warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading styles.xml")
		} else {
			warn(gofastcore.SeverityWarning, gofastcore.WarnInputStreamDamaged, "styles.xml could not be read to its end; the styles read before are used")
		}
	}
	if set.Truncated {
		warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, fmt.Sprintf("the document defines more than %d styles; the rest are not used", style.MaxStyles))
	}

	lost := odt.Lost{MetaValues: int64(md.CutValues), Keywords: int64(md.Keywords), UserDefined: int64(md.UserDefined),
		Labels: int64(md.CutNames + pkg.CutLabels()), Addresses: int64(md.CutAddresses)}
	res, err := odt.Read(ctx, pkg, set, &o, odt.Config{MaxCells: gofastcore.ResolveLimit(e.MaxCells, DefaultMaxCells), Lost: lost})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, wrap(err)
	}
	byBody := map[string]string{"text": "odt", "": "odt", "spreadsheet": "ods", "presentation": "odp"}
	if !known {
		if res.Kind == "" && !res.Failed {
			// Neither the media type nor the content says what the file
			// is: its content.xml was read and has no OpenDocument body.
			// One that could not be read so far is a damaged document,
			// read for what it has (metadata, headers and footers) with a
			// warning, as before.
			return nil, fmt.Errorf("gofastodf: the package's content has no OpenDocument body: %w", gofastcore.ErrUnsupported)
		}
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
		warn(w.Severity, w.Code, w.Message)
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

// encryptedPackage is the member in which ODF 1.4 encrypts a whole
// package (LibreOffice writes it with AES-GCM and Argon2id): its content,
// decrypted and inflated, is a package in its own right.
const encryptedPackage = "encrypted-package"

// innerKey marks the context of a package read from an encrypted-package
// member, which must not hold another.
type innerKey struct{}

// extractWhole reads a package held, encrypted, in one member.
func (e *Extractor) extractWhole(ctx context.Context, pkg *odpkg.Package, decode *gofastcore.DecodeBudget, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
	if ctx.Value(innerKey{}) != nil {
		return nil, fmt.Errorf("gofastodf: an encrypted package holds another: %w", gofastcore.ErrMalformed)
	}
	// The inner package is a file in its own right: it may be no larger
	// than MaxInputBytes, counted as it is inflated, and it is counted
	// against the decompression limit too.
	limit := gofastcore.ResolveLimit(o.Limits.MaxInputBytes, DefaultMaxInputBytes)
	inner, err := pkg.ReadEncryptedPackage(encryptedPackage, limit)
	if err != nil {
		return nil, wrap(err)
	}
	pkg.Sync(decode)
	// Its members share the outer package's key-derivation caps.
	doc, err := e.Extract(context.WithValue(ctx, innerKey{}, pkg), bytes.NewReader(inner), int64(len(inner)), o)
	if err != nil {
		return nil, err
	}
	doc.Source.Size = size
	doc.Metadata.Encrypted = true
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
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	shared := gofastcore.ErrMalformed
	switch {
	case errors.Is(err, odpkg.ErrNotPackage):
		shared = gofastcore.ErrUnsupported
	case errors.Is(err, odpkg.ErrBudget), errors.Is(err, odpkg.ErrTooManyParts), errors.Is(err, odpkg.ErrWork):
		shared = gofastcore.ErrLimitExceeded
	case errors.Is(err, odpkg.ErrWrongPassword):
		shared = gofastcore.ErrWrongPassword
	case errors.Is(err, odpkg.ErrCipher):
		shared = gofastcore.ErrUnsupported
	case errors.Is(err, odpkg.ErrEncrypted):
		shared = gofastcore.ErrPasswordRequired
	}
	return fmt.Errorf("gofastodf: %s: %w", clean.Label(err.Error()), shared)
}

// controlsMessage says that n control characters were removed from what.
func controlsMessage(n int, what string) string {
	if n == 1 {
		return "1 control character with no meaning as text was removed from " + what
	}
	return fmt.Sprintf("%d control characters with no meaning as text were removed from %s", n, what)
}
