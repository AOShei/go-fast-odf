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
| Package reading: zip limits, recovery, mimetype, manifest | todo |
| Encrypted packages: detected; `ErrPasswordRequired` without a password, `ErrUnsupported` with one (decryption is phase 3) | todo |
| `meta.xml` to metadata | todo |
| Styles: heading levels, list styles, hidden text, colours, font sizes | todo |
| odt: headings (with `text:number` / outline numbering), paragraphs | todo |
| odt: lists with markers | todo |
| odt: tables with spans as merges, header rows, repeated rows/columns | todo |
| odt: sections, frames and text boxes in reading order, images | todo |
| odt: footnotes/endnotes, comments, tracked deletions as notes | todo |
| odt: hidden text (display none, hidden paragraphs/sections, conditional text, colour near background, tiny text) | todo |
| odt: fields as text; user-field-decls / variables to the stored part | todo |
| Headers and footers from master pages | todo |
| Text budget, limits, warnings | todo |
| Tests with synthetic fixtures, Validate on each; fuzz targets | todo |
| README | todo |

### Phase 2: spreadsheets, presentations, flat XML, CLI (run 2)

| Item | Status |
|---|---|
| ods: sheets as parts, used regions as tables with `Loc.Range`, repeated rows/columns bounded, hidden rows/columns/sheets, validations as `choices` notes, named ranges to the stored part | todo |
| odp: slides as parts, `Name` = title, speaker notes as `note`, hidden slides | todo |
| Flat XML (.fodt, .fods, .fodp): one XML stream, embedded `office:binary-data` images | todo |
| `cmd/gofastodf` printing JSON like gofastooxml | todo |
| Embedded objects (`draw:object` formulas as `formula`, charts, `draw:object-ole` as children) | todo |
| Form controls and their values | todo |

### Phase 3: encryption and polish

| Item | Status |
|---|---|
| Decryption: PBKDF2 + AES-256-CBC (ODF 1.2), AES-GCM (1.3). Blowfish and Argon2 are not in the standard library: `ErrUnsupported` | todo |
| File API (part at a time) like gofastooxml.File | todo |
