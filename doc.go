// Package gofastodf extracts OpenDocument files into the shared document
// model of go-fast-core (github.com/AOShei/go-fast-core). It uses the
// standard library and go-fast-core only.
//
// # What is read
//
// Text documents (odt, and the template ott, master document odm, master
// template otm and web page oth), spreadsheets (ods, ots), presentations
// (odp, otp), and the flat XML forms fodt, fods and fodp. The format is
// decided from the file (its mimetype member or manifest, a flat file's
// office:mimetype, or the body element of its content), never from a file
// name. Templates and master documents are reported as odt, ods or odp;
// flat files as fodt, fods or fodp.
//
// Encrypted packages are decrypted with Options.Password: AES-CBC with a
// PBKDF2 key (ODF 1.2 and 1.3), the encrypted-package member of ODF 1.4
// (AES-GCM with an Argon2id key), and Blowfish CFB (ODF 1.0 and 1.1).
// Without a password an encrypted file gives ErrPasswordRequired.
//
// Drawings (odg, otg, fodg), charts (odc), formulas (odf), images (odi)
// and databases (odb) as documents in their own right, and OpenOffice.org
// 1 text documents (sxw), give ErrUnsupported.
//
// # Use
//
//	doc, err := gofastodf.ExtractFile(ctx, "report.odt", nil)
//	if err != nil {
//		return err
//	}
//	fmt.Println(doc.Markdown())
//
// Extract reads from an io.ReaderAt of a known size, ExtractFile opens a
// file, and ExtractBytes reads a file held in memory. Each uses a zero
// Extractor; an Extractor's own fields set the limits only this library
// has (archive members, table cells). Every other setting and limit is in
// gofastcore.Options. Errors wrap the errors of gofastcore.
//
// # Parts
//
// A text document gives one part of kind body, numbered 1. A spreadsheet
// gives a part of kind sheet for each sheet, a presentation a part of kind
// slide for each slide, numbered from 1. When the whole document is read
// (Options.Parts unset) and it keeps values it never shows (user field
// declarations, named ranges, form controls placed nowhere), a last part
// of kind stored holds them. Embedded documents are handed to
// Options.Child and listed in Document.Children.
//
// # Display
//
// Everything is extracted. The Display of a block, span, table region or
// part records only what the file itself declares: hidden (text:display
// "none" in a style or field, hidden sections and paragraphs, collapsed or
// filtered rows and columns, hidden sheets and slides, draw:display "none"
// on a frame, shape or layer, a header or footer switched off) and no_ink
// (an opacity of 0 on a definite font colour). Colour, size, position and
// overlap are not judged, and conditions are never evaluated.
package gofastodf
