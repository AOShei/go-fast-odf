// Package ns numbers the XML namespaces of OpenDocument for xmlscan.
package ns

import "github.com/AOShei/go-fast-core/xmlscan"

// The namespaces the readers look at.
const (
	Office xmlscan.Space = xmlscan.FirstSpace + iota
	Style
	Text
	Table
	Draw
	FO
	XLink
	DC
	Meta
	Number
	Presentation
	SVG
	Chart
	Form
	Script
	Manifest
	LOExt
	OfficeOOo
	Math
	XHTML
)

const odf = "urn:oasis:names:tc:opendocument:xmlns:"

// Spaces maps namespace URIs to their numbers.
var Spaces = map[string]xmlscan.Space{
	odf + "office:1.0":                 Office,
	odf + "style:1.0":                  Style,
	odf + "text:1.0":                   Text,
	odf + "table:1.0":                  Table,
	odf + "drawing:1.0":                Draw,
	odf + "xsl-fo-compatible:1.0":      FO,
	"http://www.w3.org/1999/xlink":     XLink,
	"http://purl.org/dc/elements/1.1/": DC,
	odf + "meta:1.0":                   Meta,
	odf + "datastyle:1.0":              Number,
	odf + "presentation:1.0":           Presentation,
	odf + "svg-compatible:1.0":         SVG,
	odf + "chart:1.0":                  Chart,
	odf + "form:1.0":                   Form,
	odf + "script:1.0":                 Script,
	odf + "manifest:1.0":               Manifest,
	"urn:org:documentfoundation:names:experimental:office:xmlns:loext:1.0": LOExt,
	"http://openoffice.org/2009/office":                                    OfficeOOo,
	"http://www.w3.org/1998/Math/MathML":                                   Math,
	"http://www.w3.org/1999/xhtml":                                         XHTML,
}

// Options is the scanner configuration every reader uses.
var Options = &xmlscan.Options{Spaces: Spaces}
