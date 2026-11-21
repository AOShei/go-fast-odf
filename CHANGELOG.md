# Changelog

All notable changes to go-fast-odf are recorded here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## v0.1.0 (not yet tagged)

The first release. Built on go-fast-core schema 0.2, pinned at `d4cd8e2`.

- Reads OpenDocument text documents (odt, ott, odm, otm, oth),
  spreadsheets (ods, ots) and presentations (odp, otp), and the flat XML
  forms fodt, fods and fodp. The format is decided from the file, never
  from its name. Drawings, charts, formulas, images and databases as
  documents in their own right, and sxw, give `ErrUnsupported`.
- Decrypts encrypted packages with `Options.Password`: AES-CBC with
  PBKDF2 (ODF 1.2, 1.3), ODF 1.4's encrypted package (AES-GCM with
  Argon2id), and Blowfish CFB (ODF 1.0, 1.1). The key-derivation work a
  manifest may ask for is capped.
- Parts: a text document is one `body` part; a spreadsheet one `sheet`
  part per sheet, its used regions as tables; a presentation one `slide`
  part per slide, with master-page text and speaker notes. Values the
  file keeps and never shows go to a last `stored` part. Embedded
  formulas and charts become blocks, form controls paragraphs, and other
  embedded documents are handed to `Options.Child`.
- `Display` records only what the file declares: `hidden` and `no_ink`.
- The command `gofastodf` prints JSON, Markdown or text. Flags:
  `-format`, `-parts`, `-images`, `-password`, `-spans`, `-cells`,
  `-furniture`, `-stored[=kinds]`, `-part-markers`, `-children`,
  `-exclude-display[=reasons]`, `-max-cells`, `-max-decoded-bytes`,
  `-max-text-bytes`, `-o`, `-quiet`, `-version`.

## Before v0.1.0

Development history, newest first. Nothing was released before v0.1.0.

### Core d4cd8e2

- go-fast-core pinned at `d4cd8e2`: `Validate` now rejects an inferred
  `thematic_break`; this library emits only declared ones and passes.

### Changed: no inferred thematic break

- A `thematic_break` is emitted only where the file declares one: an
  empty paragraph of the body in LibreOffice's "Horizontal Line" style
  (or one inheriting from it), now with `Origin` `native` (was
  `inferred`).
- Removed: an empty paragraph with only a bottom border no longer
  becomes a break; it is a border and gives no block. The adjacency
  check that took a break back when the next paragraph shared its
  border, and the paragraph-border reading only it used, are gone.
- The "Horizontal Line" break is now read wherever paragraphs are read,
  not only in the body: list items, frames and text boxes, shapes,
  presentation and drawing text, headers and footers (the break goes
  with the header's or footer's blocks), footnotes and endnotes (the
  break follows the note block, whose text cannot hold it). Not in table
  cells, whose paragraphs become the cell's text, nor in comments, speaker
  notes or tracked deletions, which are not the document's text: there
  the empty paragraph is left out as before.
  Headings in that style are not breaks.

### Changed: stored kinds

- Pins go-fast-core `ebecf3c` (`RenderOptions.StoredKinds`,
  `StoredKindNames`, and the `Validate` rule that every `type=field` note
  carries `stored=field_code`).
- Conditional-text field notes (condition and both values) carry
  `stored=field_code`: Markdown and text show the field's current value
  and leave the condition out unless stored data is asked for.
- Command: `-stored[=kinds]`, as `COMMAND_FLAGS.md`: alone, `=true` or
  `=all` for every kind, `=false` or `=` for none, `=k1,k2` (or `+`) for
  those kinds; an unknown kind is an error listing the kinds, exit 2.

### Added: sheets and slides

- `odf.formula_no_value` (info): a sheet's cells that hold a formula and
  no stored result (no `office:*value` attribute, no text) are counted,
  one warning per sheet, worded as go-fast-ooxml's
  `ooxml.formula_no_value`. The formulas stay in `Table.Cells`.
- ods header rows: `table:table-header-rows` (print titles) give a
  region's `HeaderRows` when they are its first rows; a region exactly
  equal to a declared `table:database-range` with a header (the default)
  gets `HeaderRows: 1` and `Origin` `native` (was `inferred`).
- odp master-page text: shapes of a used `style:master-page` that are not
  placeholders are given on every slide that uses the master and shows
  its objects (`presentation:background-objects-visible`, followed
  through the drawing-page style's parents), before the notes, as
  `header` (upper half) or `footer` (lower half), as go-fast-ooxml does.
  A hidden slide's copy is hidden with it. The copies are bounded at
  2^20 blocks over a presentation, each charged to the text budget; a
  slide past the bound gets `limit.exceeded`.

### Changed: decryption limits

- Argon2id is capped at 256 MiB of memory per key (was 1 GiB), memory
  times passes at 1 GiB per key (was 4 GiB) and 2 GiB per package (was
  8 GiB). LibreOffice's 64 MiB, 3 passes, 4 lanes is well within.
- An encrypted member that the archive also compresses is inflated no
  further than the size of the package; past it, `ErrMalformed`, before
  any decryption. Stored encrypted members, which writers produce, are
  read as before.
- ODF 1.4's inner package is capped at `Options.Limits.MaxInputBytes`
  (core's default when unset), counted as it is inflated; past it,
  `ErrLimitExceeded`. Its members share the outer package's
  key-derivation caps instead of having their own.

### Added: decryption

- Packages encrypted with AES-CBC and PBKDF2 (SHA-1 or SHA-256 start
  key; `SHA1/1K` or `sha256-1k` checksum) are decrypted with
  `Options.Password`; a password that does not match gives
  `ErrWrongPassword`, none still `ErrPasswordRequired`.
- ODF 1.4's `encrypted-package` (AES-GCM, Argon2id) is decrypted and read
  as the package it holds. `internal/argon2` implements Argon2id and
  BLAKE2b from RFC 9106 and RFC 7693, tested on their vectors.
- Caps on the key-derivation work a manifest may ask for (PBKDF2
  iterations; Argon2 memory, passes, lanes): over one gives
  `ErrLimitExceeded`. Decrypted members are inflated within the decode
  budget.
- `Metadata.Encrypted` is set on every decrypted document.
- `testdata/encrypted`: LibreOffice 26.8 files, password `go-fast`.
- Blowfish CFB (ODF 1.0, 1.1) is decrypted: `internal/blowfish`, its
  tables computed from pi with `math/big`, tested on Schneier's vectors.

### Added: embedded documents in the command

- Pins go-fast-core `787b056` (`RenderOptions.Children`).
- `cmd/gofastodf` sets `Options.Child`: embedded objects and documents of
  the OpenDocument media types this library reads are extracted with it,
  bounded by core's depth limit; anything else is `ErrUnsupported` and
  listed unread.
- Flag `-children` (default true): in Markdown and text, embedded
  documents follow, each after a line naming it; `-children=false` leaves
  them out.

### Changed: declared, not judged

- Pins go-fast-core `05bbddb`, schema 0.2. Blocks, spans, table regions
  and parts carry `Display` with only what the file declares: `hidden`
  (`text:display="none"` in styles and fields, hidden sections,
  paragraphs and conditional text the file records as hidden, collapsed
  and filtered rows and columns, sheets with `table:display="false"`,
  hidden slides, frames, shapes and layers with `draw:display="none"`,
  headers and footers switched off) and `no_ink` (`loext:opacity` 0 on a
  definite colour).
- Everything is extracted; characters are kept as written, zero-width and
  bidirectional controls and tag characters included.
- A form control placed by a `draw:control` declared `draw:display="none"`
  (or on a layer so declared) gives `hidden` paragraphs; `form:hidden` and
  unplaced controls stay in the stored part as `form_field`, password
  fields keep their value, all with no reason.
- Filtered rows and LibreOffice's cached copies of linked sheets are now
  reported `hidden`.
- Command: `-exclude-hidden` became `-exclude-display[=reasons]`
  (`hidden`, `no_ink`, `all`; `unknown` is an error); `-stored` added;
  the render flags and their help text follow the series' shared table.
  `text.hidden` is no longer echoed to standard error.

### Removed

- The checks that judged visibility, kept only as history here: the
  low-contrast check with all background resolution, the tiny-text check
  with font-size and text-scale resolution, the `transparent` and
  `invisible_chars` flags, core's outlier rule as applied here, the
  `text.hidden` and `text.unshown_outlier` warnings, `HiddenChecked`, the
  link-cache-sheet exemption, and the reading of a "literally true"
  condition as hidden.
- `after_end` does not apply to OpenDocument and is not produced.

### Added: earlier phases

- Phase 1: package reading with zip limits, recovery, mimetype and
  manifest; encrypted packages detected (`ErrPasswordRequired`,
  `ErrUnsupported`); metadata; text documents (headings, lists, tables
  with merges, sections, frames, images, notes, comments, tracked
  deletions, fields, headers and footers); the stored part for user
  fields; limits, warnings, fuzz targets.
- Phase 2: spreadsheets (sheets as parts, used regions, merges, repeats
  bounded, cell formulas, types and values, validations, annotations,
  named and database ranges), presentations (slides, speaker notes,
  footers), flat XML files, and the command `gofastodf` printing JSON,
  Markdown or text.
- Run 3: thematic breaks; embedded formulas (MathML to plain text, the
  StarMath source kept), charts as tables, other objects and OLE handed
  to `ExtractChild`; form controls and their values.
- Runs 4 and 5: bounds on paragraphs inside paragraphs, overlapping
  merges and sheet cells; a corpus test and benchmarks. The visibility
  checks added then were removed by the change above.
