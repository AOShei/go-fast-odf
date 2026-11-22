package odt

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif" // pixel sizes of pictures
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf/internal/clean"
	"github.com/AOShei/go-fast-odf/internal/ns"
	"github.com/AOShei/go-fast-odf/internal/odpkg"
	"github.com/AOShei/go-fast-odf/internal/style"
	"github.com/AOShei/go-fast-odf/internal/xmlscan"
)

type imageInfo struct {
	mime          string
	width, height int
}

// maxHeader bounds what is read of a picture to learn its size.
const maxHeader = 1 << 20

var mimeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".bmp": "image/bmp", ".tif": "image/tiff", ".tiff": "image/tiff", ".svg": "image/svg+xml",
	".wmf": "image/x-wmf", ".emf": "image/x-emf", ".svm": "image/x-svm", ".webp": "image/webp",
}

// drawing reads a drawing element whose Start is current: a frame, a
// group, a link around drawings, or a shape, whose text is read as blocks.
func (r *reader) drawing(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	// A shape not displayed, by its own attribute or its layer's.
	if r.attr(ns.Draw, "display") == "none" || r.set.LayerHidden(r.attr(ns.Draw, "layer")) {
		c.hidden |= gofastcore.DisplayHidden
	}
	switch string(s.Local()) {
	case "frame":
		r.frame(c, dst)
	case "a":
		if isMacro(r.attr(ns.XLink, "href")) {
			r.active++
		}
		r.drawChildren(c, dst)
	case "g":
		r.drawChildren(c, dst)
	case "control":
		r.placeControl(c, dst)
	case "image":
		r.imageAt(c, "", dst)
	default:
		// A shape: its text is block content; the rest of it is geometry.
		r.blocks(c, dst)
	}
}

func (r *reader) drawChildren(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return
		}
		if k == xmlscan.Start && r.alive() {
			if s.Space() == ns.Draw {
				r.drawing(c, dst)
			} else {
				r.blockAt(c, dst)
			}
		}
	}
}

// frame reads draw:frame: a text box, a picture, an embedded object.
func (r *reader) frame(c ctxt, dst *[]gofastcore.Block) {
	s := &r.s
	var title, desc string
	var img *gofastcore.Block
	imgAt := -1
	frameName := r.attr(ns.Draw, "name") // as the file gives it: a chart's name, cut where it is given
	replaced := false                    // an object of the frame became blocks of its own
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch {
		case space == ns.Draw && local == "text-box":
			r.blocks(c, dst)
		case space == ns.Draw && local == "image":
			if img != nil {
				s.Skip() // another form of the same picture
				continue
			}
			var tmp []gofastcore.Block
			r.imageAt(c, "", &tmp)
			if len(tmp) > 0 {
				b := tmp[0]
				img = &b
				imgAt = len(*dst)
				*dst = append(*dst, tmp[1:]...)
			}
		case space == ns.Draw && (local == "object" || local == "object-ole"):
			if r.object(c, frameName, dst) {
				replaced = true
			}
		case space == ns.Draw && local == "applet":
			r.active++
			s.Skip()
		case space == ns.Draw && (local == "plugin" || local == "floating-frame"):
			if href := r.attr(ns.XLink, "href"); href != "" {
				r.external++
			}
			r.addUnread("plug-ins and floating frames")
			s.Skip()
		case space == ns.SVG && local == "title":
			title = r.plain(s.AppendText(nil))
		case space == ns.SVG && local == "desc":
			desc = r.plain(s.AppendText(nil))
		default:
			s.Skip()
		}
	}
	if img == nil {
		return
	}
	if replaced {
		// The picture is the object's drawing of itself, given already.
		r.budget.Return(cost(img))
		return
	}
	alt := desc
	if alt == "" {
		alt = title
	}
	if alt != "" {
		if !r.budget.Take(len(alt)) {
			r.stop(textCut)
			return
		}
		img.Text = alt
	}
	whole(img, c.hidden)
	// The picture stands where the frame began, before what its text box
	// or caption held.
	*dst = append(*dst, gofastcore.Block{})
	copy((*dst)[imgAt+1:], (*dst)[imgAt:])
	(*dst)[imgAt] = *img
}

func (r *reader) plain(b []byte) string {
	v, removed := clean.String(b, clean.Prose)
	r.removed += removed
	return v
}

// imageAt reads draw:image: a picture in the package, a linked one, or
// (in a flat file) one held in the element. The block goes first in dst.
func (r *reader) imageAt(c ctxt, alt string, dst *[]gofastcore.Block) {
	s := &r.s
	href := r.attr(ns.XLink, "href")
	var inline []byte
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k == xmlscan.Start && s.Space() == ns.Office && string(s.Local()) == "binary-data" && inline == nil {
			inline = r.binaryData()
		} else if k == xmlscan.Start {
			s.Skip()
		}
	}
	if r.opts.Images == gofastcore.ImagesNone {
		return
	}
	b := gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: &gofastcore.Image{}}
	switch {
	case href != "" && odpkg.IsExternal(href):
		r.external++
		b.Attrs = map[string]string{"href": r.address(href)}
	case href != "":
		name := odpkg.Resolve(href)
		if name == "" || !r.pkg.Has(name) {
			b.Image.ID = r.label(name)
			break
		}
		info := r.imageInfo(name)
		b.Image.ID, b.Image.MIME, b.Image.Width, b.Image.Height = name, info.mime, info.width, info.height
		if r.opts.Images == gofastcore.ImagesData && !r.dataSent[name] {
			if data, err := r.readMember(name); err == nil {
				b.Image.Data = data
				r.dataSent[name] = true
			} else if errors.Is(err, odpkg.ErrBudget) {
				r.lost(capPictures, 1)
			}
		}
	case len(inline) > 0:
		// A picture held in a flat file. It is identified by its content,
		// so that equal pictures have equal IDs.
		sum := sha256.Sum256(inline)
		id := "inline-" + hex.EncodeToString(sum[:8])
		b.Image.ID = id
		if cfg, format, err := image.DecodeConfig(bytes.NewReader(inline)); err == nil {
			b.Image.MIME, b.Image.Width, b.Image.Height = "image/"+format, cfg.Width, cfg.Height
		}
		if r.opts.Images == gofastcore.ImagesData && !r.dataSent[id] {
			b.Image.Data = inline
			r.dataSent[id] = true
		}
	}
	whole(&b, c.hidden)
	if !r.emit(dst, b) {
		return
	}
}

// maxInline bounds the encoded size of a picture held in a flat file. A
// variable so that tests can lower it.
var maxInline = 64 << 20

// SetMaxInlineForTest sets maxInline and returns a function that restores
// it. For tests only; not safe while documents are read.
func SetMaxInlineForTest(n int) (restore func()) {
	old := maxInline
	maxInline = n
	return func() { maxInline = old }
}

// binaryData reads office:binary-data (base64) and decodes it. Its size
// is charged to the decompression budget; a picture over maxInline, or
// one the budget cannot hold, is skipped and nil returned.
func (r *reader) binaryData() []byte {
	s := &r.s
	var enc []byte
	over := false
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k == xmlscan.Text && !over {
			for _, c := range s.Text() {
				if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
					enc = append(enc, c)
				}
			}
			over = len(enc) > maxInline
		}
	}
	if over || len(enc) == 0 {
		if over {
			r.lost(capInline, 1)
		}
		return nil
	}
	if !r.pkg.Charge(int64(len(enc))) {
		r.lost(capPictures, 1)
		return nil
	}
	raw := bytes.TrimRight(enc, "=")
	out := make([]byte, max(base64.StdEncoding.DecodedLen(len(enc)), base64.RawStdEncoding.DecodedLen(len(raw))))
	n, err := base64.StdEncoding.Decode(out, enc)
	if err != nil {
		n, _ = base64.RawStdEncoding.Decode(out, raw)
	}
	if n == 0 {
		return nil
	}
	return out[:n]
}

// imageInfo learns a picture's media type and pixel size, once.
func (r *reader) imageInfo(name string) imageInfo {
	if info, ok := r.images[name]; ok {
		return info
	}
	info := imageInfo{mime: r.pkg.MediaType(name)}
	if info.mime == "" {
		info.mime = mimeByExt[strings.ToLower(path.Ext(name))]
	}
	if rc, err := r.pkg.Open(name); err == nil {
		if cfg, _, err := image.DecodeConfig(io.LimitReader(rc, maxHeader)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
			info.width, info.height = cfg.Width, cfg.Height
		}
		rc.Close()
	}
	if len(r.images) < 1<<16 {
		r.images[name] = info
	}
	return info
}

func (r *reader) readMember(name string) ([]byte, error) {
	rc, err := r.pkg.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// masterPages reads the headers and footers of the master pages in
// styles.xml. Each distinct one is kept once; one switched off
// (style:display="false") is kept, with the reason hidden.
func (r *reader) masterPages(part *gofastcore.Part) (headers, footers []gofastcore.Block) {
	if !r.pkg.Has("styles.xml") {
		return nil, nil
	}
	c, err := r.pkg.Scan("styles.xml", &r.s)
	if err != nil {
		return nil, nil
	}
	defer c.Close()
	r.sc = style.Styles
	defer func() { r.sc = style.Content }()
	seen := map[string]bool{}
	s := &r.s
	inMasters := false
	for {
		k := s.Next()
		if k == xmlscan.EOF {
			break
		}
		if k != xmlscan.Start || !r.alive() {
			continue
		}
		space, local := s.Space(), string(s.Local())
		switch {
		case space == ns.Office && local == "master-styles":
			inMasters = true
		case space == ns.Office && local == "body":
			return headers, footers // a flat file: the master pages came before
		case space == ns.Office && s.Depth() == 2:
			s.Skip() // the styles themselves, read by the style package
		case !inMasters:
		case (space == ns.Style || space == ns.LOExt) && (strings.HasPrefix(local, "header") || strings.HasPrefix(local, "footer")) && !strings.HasSuffix(local, "-style"):
			kind := gofastcore.KindHeader
			if strings.HasPrefix(local, "footer") {
				kind = gofastcore.KindFooter
			}
			var hidden gofastcore.Display
			if r.attr(ns.Style, "display") == "false" {
				hidden = gofastcore.DisplayHidden
			}
			var tmp []gofastcore.Block
			r.blocks(ctxt{hidden: hidden}, &tmp)
			for _, b := range tmp {
				if b.Table == nil && b.Image == nil && b.Kind != gofastcore.KindNote && b.Kind != gofastcore.KindFootnote && b.Kind != gofastcore.KindThematicBreak {
					if b.Marker != "" {
						b.Text = b.Marker + " " + b.Text
						for i := range b.Spans {
							b.Spans[i].Start += len(b.Marker) + 1
							b.Spans[i].End += len(b.Marker) + 1
						}
						b.Marker = ""
					}
					b.Kind, b.Level, b.Ordered = kind, 0, false
				}
				key := b.Kind.String() + "\x00" + b.Text + "\x00" + b.Display.String()
				if b.Table == nil && b.Image == nil && seen[key] {
					r.budget.Return(cost(&b))
					continue
				}
				seen[key] = true
				if kind == gofastcore.KindHeader {
					headers = append(headers, b)
				} else {
					footers = append(footers, b)
				}
			}
		}
	}
	if err := s.Err(); err != nil && !r.stopped {
		r.noteFailure(part, "styles.xml", err)
	}
	return headers, footers
}
