// Package odpkg reads the container of an OpenDocument file: a zip archive
// holding a mimetype member, a manifest (META-INF/manifest.xml) that lists
// the members with their media types and, for an encrypted file, how each
// is encrypted, and the XML streams themselves.
//
// It is the only place the library touches the archive, so it is where the
// limits on decompression are enforced. The approach (a budget charged per
// byte read) is that of go-fast-ooxml's internal/opc; an archive whose
// directory is lost is recovered by go-fast-core's ziprecover.
package odpkg

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AOShei/go-fast-core/ziprecover"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	number "github.com/AOShei/go-fast-odf/internal/num"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

// Errors returned by this package. The extractor maps them to the shared
// gofastcore errors.
var (
	// ErrNotZip: the input is not a zip archive.
	ErrNotZip = errors.New("odpkg: not a zip archive")
	// ErrCutOff: the archive's directory cannot be read and what the
	// members' own headers lead to is not enough to read a document from.
	ErrCutOff = errors.New("odpkg: zip archive whose directory cannot be read (the file is cut short or damaged), with too little left to read a document from")
	// ErrNotPackage: a zip archive that is not an OpenDocument package.
	ErrNotPackage = errors.New("odpkg: zip archive is not an OpenDocument package")
	// ErrDamaged: a member that cannot be read.
	ErrDamaged = errors.New("odpkg: damaged package")
	// ErrNotFound: the named member is not in the package.
	ErrNotFound = errors.New("odpkg: no such member")
	// ErrBudget: reading went over the limit on decompressed bytes.
	ErrBudget = errors.New("odpkg: decompressed size limit reached")
	// ErrTooManyParts: the archive has more members than Limits.MaxMembers.
	ErrTooManyParts = errors.New("odpkg: too many members")
	// ErrEncrypted: the member is encrypted, as the manifest says, or with
	// zip encryption.
	ErrEncrypted = errors.New("odpkg: member is encrypted")
)

// Limits bounds what a package may cost. Values are final: the caller has
// already applied its defaults.
type Limits struct {
	// MaxDecodedBytes is the total number of decompressed bytes that may
	// be read from the package, over all members.
	MaxDecodedBytes int64
	// MaxMembers is the largest number of zip members accepted.
	MaxMembers int
}

// Encryption is what the manifest says about how a member is encrypted.
// The values are kept as the manifest writes them.
type Encryption struct {
	ChecksumType  string
	Checksum      string
	Algorithm     string
	IV            string
	StartKey      string
	StartKeySize  int
	Derivation    string
	KeySize       int
	Iterations    int
	Salt          string
	ArgonMemory   int
	ArgonLanes    int
	ArgonIterates int
}

// Entry is a member as the manifest lists it.
type Entry struct {
	MediaType string
	// Size is the size the manifest gives for an encrypted member before
	// it was compressed and encrypted.
	Size       int64
	Encryption *Encryption
}

// Package is an opened OpenDocument package. It is safe for concurrent
// use.
type Package struct {
	files     map[string]*zip.File
	names     []string
	budget    atomic.Int64
	ambiguous int
	recovered *ziprecover.Recovery
	synced    int64       // the budget as it stood at the last Sync
	flat      io.ReaderAt // a flat file, or nil
	flatSize  int64
	size      int64 // of the archive: bounds an encrypted member it compresses
	manifest  map[string]Entry
	mime      string // what the mimetype member says, or the manifest's root entry
	damaged   bool   // the manifest could not be read to its end
	full      bool   // the manifest has more entries than are read
	cutLabels int    // media types in the manifest cut at the length of a label
	password  string
	ctx       context.Context // stops key derivation; nil for none
	kdfWork   atomic.Int64    // PBKDF2 iterations spent
	argonWork atomic.Int64    // Argon2 KiB-passes spent
	shared    *Package        // whose totals key derivation is charged to, or nil
	keyMu     sync.Mutex
	keys      map[string][]byte // derived keys, by member
}

// MaxManifestEntries bounds the entries read from a manifest. Each costs
// memory, and a real package has one per member.
const MaxManifestEntries = 1 << 17

// errManifestFull: the manifest has more than MaxManifestEntries entries.
var errManifestFull = errors.New("odpkg: manifest has more entries than are read")

// Open reads the archive's directory, the mimetype member and the manifest.
func Open(r io.ReaderAt, size int64, lim Limits) (*Package, error) {
	// go-fast-core's ziprecover reads the directory, or, for a file cut
	// short, finds the members by their own headers and makes one;
	// deflated members are read with go-fast-core's inflate.
	zr, recovered, err := ziprecover.Open(r, size, ziprecover.Limits{MaxMembers: lim.MaxMembers, MaxDecodedBytes: lim.MaxDecodedBytes})
	switch {
	case errors.Is(err, ziprecover.ErrNotZip):
		return nil, fmt.Errorf("%w: %v", ErrNotZip, err)
	case errors.Is(err, ziprecover.ErrBudget):
		return nil, ErrBudget
	case errors.Is(err, ziprecover.ErrTooManyMembers):
		return nil, ErrTooManyParts
	case err != nil:
		return nil, ErrCutOff
	}
	if lim.MaxMembers > 0 && len(zr.File) > lim.MaxMembers {
		return nil, fmt.Errorf("%w: %d, limit %d", ErrTooManyParts, len(zr.File), lim.MaxMembers)
	}
	p := &Package{
		files:     make(map[string]*zip.File, len(zr.File)),
		names:     make([]string, 0, len(zr.File)),
		recovered: recovered,
		size:      size,
		manifest:  map[string]Entry{},
	}
	p.budget.Store(lim.MaxDecodedBytes)
	p.synced = lim.MaxDecodedBytes
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue // a directory entry
		}
		name := Canonical(f.Name)
		if other := unicodePath(f); other != "" && Canonical(other) != name {
			p.ambiguous++
		}
		if _, dup := p.files[name]; dup {
			p.ambiguous++
			continue // the first of two members with one name is read
		}
		p.files[name] = f
		p.names = append(p.names, name)
	}
	if _, ok := p.files["[Content_Types].xml"]; ok {
		if _, odf := p.files["content.xml"]; !odf {
			return nil, fmt.Errorf("%w: it is an Office Open XML package", ErrNotPackage)
		}
	}
	if f, ok := p.files["mimetype"]; ok && f.UncompressedSize64 <= 256 {
		if rc, err := p.Open("mimetype"); err == nil {
			said, _ := io.ReadAll(io.LimitReader(rc, 256))
			rc.Close()
			p.mime = clean.Label(strings.TrimSpace(string(said)))
		}
	}
	if p.Has(manifestName) {
		if err := p.readManifest(); err != nil {
			switch {
			case errors.Is(err, ErrBudget):
				return nil, err
			case errors.Is(err, errManifestFull):
				p.full = true
			default:
				p.damaged = true
			}
		}
	}
	if p.mime == "" {
		p.mime = p.manifest["/"].MediaType
	}
	if p.mime == "" && !p.Has("content.xml") {
		return nil, ErrNotPackage
	}
	return p, nil
}

const manifestName = "META-INF/manifest.xml"

// Canonical returns the form of a member name used as a key: forward
// slashes, no leading slash or "./". OpenDocument names are case-sensitive
// and are kept so.
func Canonical(name string) string {
	if strings.IndexByte(name, '\\') >= 0 {
		name = strings.ReplaceAll(name, "\\", "/")
	}
	for {
		switch {
		case strings.HasPrefix(name, "/"):
			name = name[1:]
		case strings.HasPrefix(name, "./"):
			name = name[2:]
		default:
			return name
		}
	}
}

// Resolve returns the member name that an xlink:href in a stream refers
// to, or "" if it refers to something outside the package. References in
// the streams are relative to the package root.
func Resolve(href string) string {
	if href == "" || IsExternal(href) {
		return ""
	}
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	name := Canonical(href)
	parts := strings.Split(name, "/")
	out := parts[:0]
	for _, seg := range parts {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, unescape(seg))
		}
	}
	return strings.Join(out, "/")
}

// IsExternal reports whether an href names something outside the package:
// it has a URL scheme, or is an absolute or parent-relative file path.
func IsExternal(href string) bool {
	if strings.HasPrefix(href, "../") || strings.HasPrefix(href, "/") || strings.HasPrefix(href, "\\") {
		return true
	}
	for i := 0; i < len(href); i++ {
		c := href[i]
		switch {
		case c == ':':
			return i > 0
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return false
}

func unescape(seg string) string {
	if strings.IndexByte(seg, '%') < 0 {
		return seg
	}
	b := make([]byte, 0, len(seg))
	for i := 0; i < len(seg); i++ {
		if seg[i] == '%' && i+2 < len(seg) {
			if v, err := strconv.ParseUint(seg[i+1:i+3], 16, 8); err == nil {
				b = append(b, byte(v))
				i += 2
				continue
			}
		}
		b = append(b, seg[i])
	}
	return string(b)
}

// MIME returns the media type the package declares: its mimetype member,
// or else the manifest's entry for the root. It is "" if neither says.
func (p *Package) MIME() string { return p.mime }

// Recovered returns what was found by the members' own headers, if the
// archive had no directory that could be read; nil for a sound archive.
func (p *Package) Recovered() *ziprecover.Recovery { return p.recovered }

// Ambiguous returns the number of members that programs disagree about.
func (p *Package) Ambiguous() int { return p.ambiguous }

// ManifestDamaged reports that the manifest could not be read to its end.
func (p *Package) ManifestDamaged() bool { return p.damaged }

// CutLabels returns how many media types in the manifest were cut at the
// length of a label.
func (p *Package) CutLabels() int { return p.cutLabels }

// ManifestFull reports whether the manifest has more entries than are
// read (MaxManifestEntries); what the rest say of their members is not
// known.
func (p *Package) ManifestFull() bool { return p.full }

// Has reports whether the package has the named member.
func (p *Package) Has(name string) bool {
	if p.flat != nil {
		return flatStreams[Canonical(name)]
	}
	_, ok := p.files[Canonical(name)]
	return ok
}

// flatStreams are the streams of a package that a flat file holds, all in
// its one XML document.
var flatStreams = map[string]bool{"content.xml": true, "styles.xml": true, "meta.xml": true}

// OpenFlat opens a flat OpenDocument file: one XML document whose root,
// office:document, holds what a package keeps in its streams. Each of
// those streams reads as the whole file; the readers take from it what
// they read from their stream.
func OpenFlat(r io.ReaderAt, size int64, lim Limits) (*Package, error) {
	var s xmlscan.Scanner
	defer s.Release()
	s.Reset(io.NewSectionReader(r, 0, size), &xmlscan.Options{Spaces: ns.Spaces, BufferSize: 4096})
	for {
		switch s.Next() {
		case xmlscan.Start:
			if s.Space() != ns.Office || string(s.Local()) != "document" {
				return nil, fmt.Errorf("%w: XML whose root is not office:document", ErrNotPackage)
			}
			mime, _ := s.Attr(ns.Office, "mimetype")
			p := &Package{flat: r, flatSize: size, mime: clean.Label(string(mime)), manifest: map[string]Entry{}}
			p.budget.Store(lim.MaxDecodedBytes)
			p.synced = lim.MaxDecodedBytes
			return p, nil
		case xmlscan.EOF:
			return nil, fmt.Errorf("%w: not XML", ErrNotZip)
		}
	}
}

// Flat reports whether the package is a flat file.
func (p *Package) Flat() bool { return p.flat != nil }

// Names returns the member names in archive order. The slice must not be
// modified.
func (p *Package) Names() []string { return p.names }

// Entry returns what the manifest says of a member.
func (p *Package) Entry(name string) (Entry, bool) {
	e, ok := p.manifest[Canonical(name)]
	return e, ok
}

// Encrypted reports whether the manifest says the member is encrypted.
func (p *Package) Encrypted(name string) bool {
	e, ok := p.manifest[Canonical(name)]
	return ok && e.Encryption != nil
}

// MediaType returns the media type the manifest gives a member, or "".
func (p *Package) MediaType(name string) string { return p.manifest[Canonical(name)].MediaType }

// Size returns the uncompressed size the archive declares for a member, or
// -1 if there is no such member.
func (p *Package) Size(name string) int64 {
	if p.flat != nil && p.Has(name) {
		return p.flatSize
	}
	f, ok := p.files[Canonical(name)]
	if !ok {
		return -1
	}
	return int64(f.UncompressedSize64)
}

// Sync settles the package's count of decompressed bytes with the shared
// budget d (gofastcore.DecodeBudget): what was read since the last Sync
// is taken from d, and anything d has lost meanwhile (to an embedded
// document, which takes what it used from d when it closes) is taken from
// the package's own count. It is called before an embedded document is
// handed on, after it, and at the end.
func (p *Package) Sync(d interface {
	Take(int64) bool
	Left() int64
}) {
	if d == nil {
		return
	}
	now := p.budget.Load()
	d.Take(p.synced - now)
	if left := d.Left(); left < now {
		p.budget.Store(left)
	}
	p.synced = p.budget.Load()
}

// Remaining returns how many decompressed bytes may still be read.
func (p *Package) Remaining() int64 { return max(p.budget.Load(), 0) }

// Charge takes n bytes from the decompression budget. It reports false if
// the budget has run out.
func (p *Package) Charge(n int64) bool { return p.budget.Add(-n) >= 0 }

// Open returns a reader for a member's decompressed content. Every byte
// read is charged to the package's budget, and a read that goes over it
// fails with ErrBudget.
func (p *Package) Open(name string) (io.ReadCloser, error) {
	name = Canonical(name)
	if p.flat != nil {
		return p.OpenRaw(name)
	}
	f, ok := p.files[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, clean.Label(name))
	}
	if f.Flags&1 != 0 || p.Encrypted(name) && p.password == "" {
		return nil, fmt.Errorf("%w: %s", ErrEncrypted, clean.Label(name))
	}
	if p.Encrypted(name) {
		return p.openEncrypted(name)
	}
	return p.OpenRaw(name)
}

// OpenRaw is Open without the check for encryption: an encrypted member
// is given as the archive holds it.
func (p *Package) OpenRaw(name string) (io.ReadCloser, error) {
	if p.flat != nil {
		if !p.Has(name) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, clean.Label(name))
		}
		return io.NopCloser(io.NewSectionReader(p.flat, 0, p.flatSize)), nil
	}
	f, ok := p.files[Canonical(name)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, clean.Label(name))
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, clean.Label(name), err)
	}
	return &budgetReader{rc: rc, p: p}, nil
}

type budgetReader struct {
	rc io.ReadCloser
	p  *Package
}

func (b *budgetReader) Read(buf []byte) (int, error) {
	n, err := b.rc.Read(buf)
	if n > 0 && b.p.budget.Add(-int64(n)) < 0 {
		return 0, ErrBudget
	}
	return n, err
}

func (b *budgetReader) Close() error { return b.rc.Close() }

// Scan opens a member and resets s to read it. The caller closes the
// returned closer when done.
func (p *Package) Scan(name string, s *xmlscan.Scanner) (io.Closer, error) {
	rc, err := p.Open(name)
	if err != nil {
		return nil, err
	}
	// The window need not be larger than the stream says it is; it grows
	// if a tag needs more, so a size that lies costs nothing but time.
	opts := ns.Options
	if size := p.Size(name); size >= 0 && size < xmlscan.DefaultBufferSize {
		o := *ns.Options
		o.BufferSize = int(max(size+1, 4096))
		opts = &o
	}
	s.Reset(rc, opts)
	return rc, nil
}

func (p *Package) readManifest() error {
	var s xmlscan.Scanner
	defer s.Release()
	c, err := p.Scan(manifestName, &s)
	if err != nil {
		return err
	}
	defer c.Close()
	var cur string
	var enc *Encryption
	attr := func(local string) string {
		v, _ := s.Attr(ns.Manifest, local)
		return string(v)
	}
	num := func(local string) int {
		n, _ := number.Atoi(strings.TrimSpace(attr(local)))
		return n
	}
	for {
		switch s.Next() {
		case xmlscan.Start:
			if s.Space() != ns.Manifest {
				continue
			}
			switch string(s.Local()) {
			case "file-entry":
				if len(p.manifest) >= MaxManifestEntries {
					return errManifestFull
				}
				full := attr("full-path")
				cur, enc = full, nil
				if full != "/" {
					cur = Canonical(full)
				}
				size, _ := number.ParseInt(strings.TrimSpace(attr("size")), 10, 64)
				mt, cut := clean.LabelCut(attr("media-type"))
				if cut {
					p.cutLabels++
				}
				p.manifest[cur] = Entry{MediaType: mt, Size: size}
			case "encryption-data":
				enc = &Encryption{ChecksumType: attr("checksum-type"), Checksum: attr("checksum")}
				if e, ok := p.manifest[cur]; ok {
					e.Encryption = enc
					p.manifest[cur] = e
				}
			case "algorithm":
				if enc != nil {
					enc.Algorithm, enc.IV = attr("algorithm-name"), attr("initialisation-vector")
				}
			case "start-key-generation":
				if enc != nil {
					enc.StartKey, enc.StartKeySize = attr("start-key-generation-name"), num("key-size")
				}
			case "key-derivation":
				if enc != nil {
					enc.Derivation, enc.KeySize, enc.Iterations, enc.Salt = attr("key-derivation-name"), num("key-size"), num("iteration-count"), attr("salt")
					// ODF 1.4 names the Argon2 parameters in the manifest
					// namespace; LibreOffice wrote them in its own first.
					argon := func(local string) int {
						v, ok := s.Attr(ns.Manifest, local)
						if !ok {
							v, _ = s.Attr(ns.LOExt, local)
						}
						n, _ := number.Atoi(strings.TrimSpace(string(v)))
						return n
					}
					enc.ArgonIterates, enc.ArgonMemory, enc.ArgonLanes = argon("argon2-iterations"), argon("argon2-memory"), argon("argon2-lanes")
				}
			}
		case xmlscan.EOF:
			if err := s.Err(); err != nil {
				if errors.Is(err, ErrBudget) {
					return err
				}
				return fmt.Errorf("%w: manifest: %v", ErrDamaged, err)
			}
			return nil
		}
	}
}

// unicodePath returns the name a member's Info-ZIP Unicode Path extra
// field gives it, or "" if it has no valid one.
func unicodePath(f *zip.File) string {
	extra := f.Extra
	for len(extra) >= 4 {
		id := uint16(extra[0]) | uint16(extra[1])<<8
		size := int(extra[2]) | int(extra[3])<<8
		if size > len(extra)-4 {
			return ""
		}
		field := extra[4 : 4+size]
		extra = extra[4+size:]
		if id != 0x7075 || len(field) < 5 || field[0] != 1 {
			continue
		}
		sum := uint32(field[1]) | uint32(field[2])<<8 | uint32(field[3])<<16 | uint32(field[4])<<24
		if crc32.ChecksumIEEE([]byte(f.Name)) != sum {
			return ""
		}
		return string(field[5:])
	}
	return ""
}
