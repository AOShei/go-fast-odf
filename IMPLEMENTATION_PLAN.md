# go-fast-odf implementation plan

Development history, kept for reference. What the library does is in
`README.md`; what changed is in `CHANGELOG.md`.

Module `github.com/AOShei/go-fast-odf`, package `gofastodf`. Extracts
OpenDocument files (ODF 1.0 to 1.3, and ODF 1.4's encrypted package) into
the go-fast-core model (core pinned at commit d4cd8e2, schema 0.2).
Standard library and go-fast modules only.

## Layout

| Path | What |
|---|---|
| `doc.go`, `extract.go` | Package documentation; `Extractor`, `Extract`, `ExtractFile`, `ExtractBytes`, warning codes, defaults |
| `cmd/gofastodf` | The command: JSON, Markdown or text; embedded OpenDocument files read through `Options.Child` |
| `internal/xmlscan` | Streaming XML tokenizer, copied from go-fast-ooxml (see NOTICE) |
| `internal/clean` | Text conventions (UTF-8, control characters, ligatures), copied from go-fast-ooxml |
| `internal/display` | Settles collected display spans onto a block, copied from go-fast-ooxml's `internal/hidden` |
| `internal/odpkg` | Zip package and flat file: limits, recovery of a cut archive, `mimetype`, `META-INF/manifest.xml`, decryption of encrypted members and of ODF 1.4's encrypted package |
| `internal/argon2` | Argon2id and BLAKE2b, from RFC 9106 and RFC 7693 |
| `internal/blowfish` | Blowfish and its CFB mode, tables computed from pi |
| `internal/ns` | ODF namespace numbering for xmlscan |
| `internal/meta` | `meta.xml` to `gofastcore.Metadata` |
| `internal/style` | `styles.xml` and automatic styles: outline levels, list styles, the "Horizontal Line" style, declared display (`text:display`, opacity on a definite colour), emphasis |
| `internal/odt` | Bodies of text documents, spreadsheets and presentations to parts and blocks; embedded objects, form controls, the stored part |

## Done

- Phase 1 (run 1): package reading with zip limits, recovery, mimetype
  and manifest; encrypted packages detected; metadata; text documents
  (headings, lists, tables, sections, frames, images, notes, comments,
  tracked deletions, fields, headers and footers); the stored part for
  user fields; limits, warnings, fuzz targets.
- Phase 2 (runs 2 to 5): spreadsheets, presentations, flat XML, the
  command; embedded formulas, charts and other objects; form controls;
  thematic breaks; a corpus test and benchmarks. The fuzz stall of run 1
  was fixed in run 2: inputs are the inside of a stream's root element
  and the packages store their members.
- Declared, not judged (run 6, core 05bbddb, schema 0.2): `Display` holds
  only `hidden` and `no_ink` from what the file declares. The visibility
  checks of runs 1 to 5 (colour near the background, tiny text, core's
  outlier rule, the link-cache exemption) were removed. Colour is read
  only for an opacity of 0 on a definite font colour; font sizes are not
  read.
- Runs 7 to 9: command flags as the series' `COMMAND_FLAGS.md`
  (`-stored[=kinds]`, `-exclude-display[=reasons]`, `-children`); placed
  form controls declared `draw:display="none"` are `hidden`; embedded
  documents in the command (core 787b056); stored kinds (core ebecf3c).
- Phase 7: ods header rows from print titles and database ranges,
  `odf.formula_no_value`, odp master-page text.
- Decryption: AES-CBC with PBKDF2, ODF 1.4's encrypted package with
  AES-GCM and Argon2id, Blowfish CFB; key-derivation caps (review 2).
- No inferred thematic break (core d4cd8e2): only empty paragraphs in
  LibreOffice's "Horizontal Line" style, origin `native`, wherever
  paragraphs are read.

## Todo

- Unit tests inside `internal/style` and `internal/odt` (covered only
  through the public API).
- `Table.Cells` hrefs in text-document tables (spreadsheets have them).
- odp: tables in frames checked with a real slide test.
- Sub-documents held whole in a flat file's `draw:object` handed to
  children (now `odf.unread_content`).
- A File API (part at a time) like gofastooxml.File.
