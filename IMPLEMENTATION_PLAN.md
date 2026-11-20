# go-fast-odf implementation plan

Module `github.com/AOShei/go-fast-odf`, package `gofastodf`. Extracts
OpenDocument 1.0 to 1.3 files into the go-fast-core model (core pinned at
commit ebecf3c). Standard library and go-fast modules only.

Status legend: **done**, **partial**, **todo**.

## Layout

| Path | What |
|---|---|
| `extract.go` | `Extractor`, `Extract`, `ExtractFile`, warning codes, defaults |
| `internal/xmlscan` | Streaming XML tokenizer, copied from go-fast-ooxml (see NOTICE) |
| `internal/clean` | Text conventions (UTF-8, control characters, ligatures), copied from go-fast-ooxml |
| `internal/display` | Settles collected display spans onto a block, copied from go-fast-ooxml's `internal/hidden` |
| `internal/odpkg` | Zip package: limits, recovery of a cut archive, `mimetype`, `META-INF/manifest.xml`, encryption detection |
| `internal/ns` | ODF namespace numbering for xmlscan |
| `internal/meta` | `meta.xml` to `gofastcore.Metadata` |
| `internal/style` | `styles.xml` and automatic styles: outline levels, list styles, declared display (`text:display`, opacity on a definite colour), text formatting |
| `internal/odt` | Text documents: body to blocks, stored part |

## Phases

### Phase 1: package and text documents (run 1)

| Item | Status |
|---|---|
| Repository setup, plan, NOTICE | done |
| Package reading: zip limits, recovery, mimetype, manifest | done |
| Encrypted packages: detected; `ErrPasswordRequired` without a password, `ErrUnsupported` with one (decryption is phase 3) | done |
| `meta.xml` to metadata | done |
| Styles: heading levels, list styles, hidden text, colours, font sizes | done |
| odt: headings (with `text:number` / outline numbering), paragraphs | done |
| odt: lists with markers | done |
| odt: tables with spans as merges, header rows, repeated rows/columns | done |
| odt: sections, frames and text boxes in reading order, images | done |
| odt: footnotes/endnotes, comments, tracked deletions as notes | done |
| odt: hidden text (display none, hidden paragraphs/sections, conditional text, colour near background, tiny text) | done; colour and size checks removed in run 6 |
| odt: fields as text; user-field-decls to the stored part | done (variable-decls hold no values; nothing stored for them) |
| Headers and footers from master pages | done |
| Text budget, limits, warnings | done |
| Tests with synthetic fixtures, Validate on each; fuzz targets | done (see note on fuzzing below) |
| README | done (phase 1 scope) |
| Unit tests inside internal packages (style resolution, number formats, odpkg Resolve/IsExternal) | todo: covered only through the public API |
| Table cell details (`Table.Cells` hrefs with Options.Spans) | todo |

Fuzzing (fixed in run 2): the stall seen in run 1 was the engine minimising
each new-coverage input, which costs the square of its length; inputs were
whole 1-2 KB streams in deflated packages. Inputs are now the inside of a
stream's root element, the packages store their members, and the scanner's
window is sized from the member. Measured 20 s each: 30,000 to 180,000
execs/s per target.

### Phase 2: spreadsheets, presentations, flat XML, CLI (run 2)

| Item | Status |
|---|---|
| Fuzz stall fixed; fuzz targets for every reader | done |
| ods: sheets as parts, used regions as tables with `Loc.Range`, merges, repeated rows/columns bounded, hidden rows/columns/sheets, ISO dates and times, formulas/types/values in `Table.Cells`, validations as `choices` notes, annotations as notes, named and database ranges to the stored part | done |
| odp: slides as parts, `Name` = title, frames in reading order, speaker notes as `note`, hidden slides, footer placeholders as `footer` | done |
| Flat XML (.fodt, .fods, .fodp): one XML stream, `office:binary-data` images | done |
| `cmd/gofastodf` printing JSON, Markdown or text like gofastooxml | done |
| ods: header rows of regions (print titles), declared database ranges as native tables | done (phase 7) |
| ods: cells' formulas without cached values (`odf.formula_no_value`, one per sheet with the count, like ooxml's) | done (phase 7) |
| odp: master-page text (non-placeholder shapes as header/footer, on every slide that shows the master's objects, bounded as in ooxml) | done (phase 7) |
| odp: `presentation:background-objects-visible` | done (phase 7) |
| odp: tables in frames checked with a real slide test | todo |
| Embedded objects (`draw:object` formulas as `formula`, charts, `draw:object-ole` as children) | done (run 3) |
| Form controls and their values | done (run 3) |
| Thematic breaks (core df335f0) for empty bottom-bordered paragraphs | done (run 3); bordered half removed (R7, branch `no-inferred-break`): only empty "Horizontal Line" paragraphs, origin native; read wherever paragraphs are read (lists, frames, notes, headers/footers, slides), not in table cells, whose paragraphs become cell text |
| Sub-documents held whole in a flat file's draw:object handed to children | todo |
| Core's shared outlier rule (17a0e50), corpus test, automatic-colour opacity, external-link cache sheets | done (run 5); outlier rule and link-cache exemption removed in run 6 |
| Hidden flag as a signal (core 4de43b1) | done (run 4); superseded by run 6 |
| Declared, not judged (core 05bbddb, schema 0.2): `Display` with `hidden` and `no_ink` from what the file declares only; low-contrast, tiny and outlier checks, `HiddenChecked`, the `text.hidden` and `text.unshown_outlier` warnings and the link-cache exemption removed; filtered rows and link-cache sheets reported `hidden`; command flag `-exclude-display`; R6 test | done (run 6) |
| Follow-up decisions (2026-11-20): command flags as `COMMAND_FLAGS.md` (`-stored` added, help text aligned, `TestRenderFlags`); a placed form control whose `draw:control` declares `draw:display="none"` (or stands on such a layer) is `hidden` (`TestControlDisplay`), `form:hidden` and unplaced controls stay in the stored part; `CHANGELOG.md` started | done (run 7) |
| Embedded documents in the command (core 787b056, `RenderOptions.Children`): `cmd/gofastodf` sets `Options.Child` to a dispatcher that hands the OpenDocument media types this library reads to its extractor and returns `ErrUnsupported` for the rest; core's `MaxDepth` bounds nesting; flag `-children` (default true); `TestChildren`, `TestChildrenFlag` | done (run 8) |
| Stored kinds (core ebecf3c): conditional-text field notes carry `stored=field_code` (core `Validate` requires it on every `type=field` note; they are the only field notes this library makes); `-stored[=kinds]` as `COMMAND_FLAGS.md` (`storedFlag`, `IsBoolFlag`; kinds are core `StoredKindNames`, this library defines none); `TestRenderFlags` covers every row | done (run 9) |
| Unit tests inside internal packages; Table.Cells hrefs in text-document tables | todo |

### Phase 3: encryption and polish

| Item | Status |
|---|---|
| Decryption (branch `phase-6-decryption`): AES-CBC with PBKDF2, SHA-1 or SHA-256 start key, `SHA1/1K` or `sha256-1k` checksum (ODF 1.2, 1.3); the whole-package `encrypted-package` of ODF 1.4 with AES-GCM and Argon2id (`internal/argon2`, from RFC 9106 and RFC 7693). Tested on LibreOffice 26.8 files in `testdata/encrypted`. Caps: PBKDF2 10 million iterations per member, 30 million per package; Argon2 256 MiB, 64 passes, 64 lanes, memory times passes 1 GiB per key and 2 GiB per package (review 2); a compressed encrypted member inflates to at most the package's size; the ODF 1.4 inner package to at most `MaxInputBytes`, sharing the outer package's key-derivation caps | done |
| Decryption of Blowfish CFB (ODF 1.0, 1.1), `internal/blowfish`: tables computed from pi with `math/big`, checked against Schneier's vectors | done |
| File API (part at a time) like gofastooxml.File | todo |
