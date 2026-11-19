# go-fast-odf implementation plan

Module `github.com/AOShei/go-fast-odf`, package `gofastodf`. Extracts
OpenDocument 1.0 to 1.3 files into the go-fast-core model (core pinned at
commit 031432a). Standard library and go-fast modules only.

Status legend: **done**, **partial**, **todo**.

## Layout

| Path | What |
|---|---|
| `extract.go` | `Extractor`, `Extract`, `ExtractFile`, warning codes, defaults |
| `internal/xmlscan` | Streaming XML tokenizer, copied from go-fast-ooxml (see NOTICE) |
| `internal/clean` | Text conventions (UTF-8, control characters, ligatures), copied from go-fast-ooxml |
| `internal/hidden` | Settles collected hidden spans onto a block, copied from go-fast-ooxml |
| `internal/odpkg` | Zip package: limits, recovery of a cut archive, `mimetype`, `META-INF/manifest.xml`, encryption detection |
| `internal/ns` | ODF namespace numbering for xmlscan |
| `internal/meta` | `meta.xml` to `gofastcore.Metadata` |
| `internal/style` | `styles.xml` and automatic styles: outline levels, list styles, hidden text, colours, sizes |
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
| odt: hidden text (display none, hidden paragraphs/sections, conditional text, colour near background, tiny text) | done |
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
| ods: header rows of regions (print titles), declared database ranges as native tables | todo |
| ods: cells' formulas without cached values (warning like ooxml's formula_no_value) | todo |
| odp: off-slide frames (`off_page`), master-page text, tables in frames checked with a real slide test | todo |
| Embedded objects (`draw:object` formulas as `formula`, charts, `draw:object-ole` as children) | todo |
| Form controls and their values | todo |
| Unit tests inside internal packages; Table.Cells hrefs in text-document tables | todo |

### Phase 3: encryption and polish

| Item | Status |
|---|---|
| Decryption: PBKDF2 + AES-256-CBC (ODF 1.2), AES-GCM (1.3). Blowfish and Argon2 are not in the standard library: `ErrUnsupported` | todo |
| File API (part at a time) like gofastooxml.File | todo |
