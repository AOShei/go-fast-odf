# go-fast-odf

Extracts OpenDocument files into the shared [go-fast-core](https://github.com/AOShei/go-fast-core) document model. Standard library only.

```go
import "github.com/AOShei/go-fast-odf" // package gofastodf

doc, err := gofastodf.ExtractFile(ctx, "report.odt", nil)
if err != nil { ... }
fmt.Println(doc.Markdown())
```

**Status: v0.2.2 (2026-11-24).** Text documents (`.odt`, with the template `.ott`, master documents `.odm` and `.otm`, and web pages `.oth`), spreadsheets (`.ods`, `.ots`), presentations (`.odp`, `.otp`), and the flat XML variants (`.fodt`, `.fods`, `.fodp`) are read. Password-protected packages are decrypted (AES-CBC with PBKDF2; ODF 1.4's AES-GCM with Argon2id; Blowfish CFB of ODF 1.0 and 1.1). Drawings, charts, formulas, images and databases as documents in their own right, and the OpenOffice.org 1 formats, give `ErrUnsupported`.

**Speed and memory.** Against the Python and Rust tools in use, on files from Common Crawl, see [`bench/`](bench/README.md). Go's garbage collector can be given more memory for less work with `GOGC` in the environment (or `debug.SetGCPercent` in a program); the library and its command keep Go's default, 100. Each file read from disk as a pipeline does (wall time, peak memory):

| Files | `GOGC` | One worker | 24 workers |
|---|---|---|---|
| 2,457 ODT (197 MB) | 100 (default) | 2.24 s, 35 MB | 0.38 s, 120 MB |
| | 200 | 2.08 s, 58 MB | 0.29 s, 231 MB |
| | 400 | 2.00 s, 86 MB | 0.27 s, 472 MB |
| 1,265 ODS (87 MB) | 100 (default) | 5.54 s, 232 MB | 0.98 s, 402 MB |
| | 200 | 5.33 s, 344 MB | 0.92 s, 456 MB |
| | 400 | 5.19 s, 344 MB | 0.84 s, 764 MB |
| 61 ODP (61 MB) | 100 (default) | 0.27 s, 30 MB | 0.05 s, 120 MB |
| | 200 | 0.27 s, 35 MB | 0.05 s, 130 MB |
| | 400 | 0.25 s, 51 MB | 0.05 s, 156 MB |

## API

- `Extract(ctx, r io.ReaderAt, size, opts)`, `ExtractFile(ctx, path, opts)`, `ExtractBytes(ctx, data, opts)`.
- `Extractor{MaxArchiveMembers, MaxCells}` implements `gofastcore.Extractor`; its zero value is ready and safe for concurrent use. `Formats()` is `odt ods odp fodt fods fodp`.

The format is decided from the file (a package's `mimetype` member or manifest root entry, a flat file's `office:mimetype`, or the body element of the content), never from the file name. A flat file is reported as `fodt`, `fods` or `fodp` with the flat-XML media type, whatever kind its `office:mimetype` names.

## Command

`go install github.com/AOShei/go-fast-odf/cmd/gofastodf@latest`

```
gofastodf [flags] file-or-directory...
  -format json|markdown|text   -parts 2-5   -images meta|data|none   -password P
  -spans  -cells  -furniture  -stored[=kinds]  -part-markers  -children  -exclude-display[=reasons]
  -max-cells N  -max-decoded-bytes N  -max-text-bytes N  -o file  -quiet  -version
```

A directory stands for the files in it and below it whose names end in `.odt`, `.ott`, `.odm`, `.ods`, `.ots`, `.odp`, `.otp`, `.fodt`, `.fods` or `.fodp` (in any case), LibreOffice's `.~lock.` files left out; other files in it are not read, `.otm` and `.oth` among them. A file named on the command line is read whatever its name. With one input the JSON is indented; with several, each document is one line.

Documents go to standard output; warnings of severity error go to standard error; the exit status is 1 if any input could not be read, 2 for a usage error. `-password` is also tried on embedded documents. `-version` prints the module version the command was built from.

The command reads embedded documents: an object whose media type is one of the OpenDocument formats this library reads (`application/vnd.oasis.opendocument.text`, `-spreadsheet`, `-presentation`, their templates, master and web variants and flat forms) is extracted with this library, as deep as core's `Limits.MaxDepth` (default 4) allows. Anything else (an OLE object, a drawing) is listed in `Children` with `child.unsupported` and not read.

The render flags are the series' shared ones (`COMMAND_FLAGS.md` in the workspace) and affect Markdown and text only; JSON always has everything:

| Flag | Meaning |
|---|---|
| `-furniture` | include running headers and footers |
| `-stored[=kinds]` | in Markdown and text, include stored data (data the file keeps but shows nowhere): all of it, or with =kinds only those kinds (such as field_code, document_variable, defined_name). Alone, `=true` or `=all` mean every kind; `=false` or `=` none; kinds are separated by `,` or `+`; an unknown kind is an error listing the kinds (exit status 2) |
| `-part-markers` | put a line before each part saying which part it is |
| `-children` (default true) | include embedded documents, each after a line naming it (`[child 1: Object 1]`); `-children=false` leaves them out |
| `-exclude-display[=reasons]` | leave out what the file declares not displayed: all of it (`-exclude-display`, `=true`, `=all`), or only those reasons (`=hidden`, `=no_ink`, `=hidden,no_ink`; comma or `+`); `=false` or `=` is off; any other value, `unknown` included, is an error |

## How a text document maps

One part of kind `body`, numbered 1, and, when the document stores values it never shows, a last part of kind `stored`.

| Source | Block |
|---|---|
| `text:h` | `heading`; level from `text:outline-level`, else the paragraph style's `style:default-outline-level`, else 1. Levels above 6 are 6 with `Attrs["level"]` and `structure.heading_level_clamped`. `Marker` is the heading's `text:number` if the file gives one, else the number the outline style computes |
| `text:p` | `paragraph`; white space collapsed as ODF asks (§6.1.2): runs of spaces, tabs and line ends are one space; `text:s`, `text:tab`, `text:line-break` give spaces, a tab and `\n` |
| `text:list` / `text:list-item` | `list_item`, `Level` the nesting depth, `Marker` computed from the list style (`1`, `a`, `A`, `i`, `I`, prefix, suffix, display levels, start values, `continue-numbering`, `continue-list`); a bullet's character; `Ordered` for numbers. Further paragraphs of an item are `list_item` without a marker; `text:list-header` has none |
| `text:numbered-paragraph` | `list_item` at `text:level` |
| `table:table` | `table`; `Attrs["name"]` the table name; header rows from `table:table-header-rows`; spanned cells top-left with `Merges`; repeated rows and columns written out, trailing empty ones dropped; cell paragraphs joined with `\n`, nested tables as tab-separated lines |
| `text:section` | its content |
| `draw:frame` | text boxes as blocks after the paragraph that anchors them; a picture as an `image` block (alt text from `svg:desc`, else `svg:title`; ID the member name; MIME from the manifest; pixel size for PNG, JPEG and GIF); shapes' text as blocks |
| `text:note` | `footnote` with the citation as `Marker`; footnotes after their paragraph, endnotes at the end of the body |
| `office:annotation` | `note`, type `comment`, with `author` and `date` |
| `text:tracked-changes` | the body is the text with every change accepted; each deletion is a `note` of type `deletion` where `text:change` marks it (or at the end if nothing marks it) |
| `text:drop-down` | its value in the text; the values offered as a `note` of type `choices`, `field` its name |
| `text:conditional-text` | its current value in the text; condition and both values as a `note` of type `field` with `stored=field_code` (stored data, no display reason; rendered only with `-stored` or `-stored=field_code`) |
| fields (`text:date`, `text:page-number`, `text:author-name`, variables, ...) | their displayed value, in the text |
| an empty paragraph (not a heading) in LibreOffice's "Horizontal Line" style (`Horizontal_20_Line`) or a style inheriting from it, wherever paragraphs are read | `thematic_break`, `Origin` `native` (ODF has no rule element; this style is how LibreOffice writes an HTML `<hr>`). In list items, frames, text boxes, shapes and slides it stands in place; in a header or footer it goes with that header's or footer's blocks; in a footnote or endnote, whose paragraphs become one block's text, it follows that block. In a table cell it gives nothing: the cell's paragraphs become the cell's text, which cannot hold a break, so the empty paragraph is left out. Nor in a comment, a speaker note or a tracked deletion: those are not the document's text, and a break there would split the text where the author did not. Breaks are emitted only where the file declares one: an empty paragraph with only a bottom border is a border and gives no block; paragraphs with text and literal `* * *` stay paragraphs |
| `text:ruby` | base text, then the annotation in brackets |
| indexes (tables of contents, ...) | their body as paragraphs; their templates are not text |
| master page headers and footers (`styles.xml`) | `header` / `footer` blocks, first and last; each distinct one once |
| `text:user-field-decls` | the `stored` part: `name: value` paragraphs, `Attrs["stored"]="document_variable"`, `field` the name |

`meta.xml`: `dc:title`, `dc:subject`, `meta:keyword` (one entry each), `dc:language`, `meta:creation-date`, `dc:date`, `meta:generator`. `Authors` is `meta:initial-creator`, or `dc:creator` when there is no initial creator; a different `dc:creator` is `Extra["last_modified_by"]`. Also in `Extra`: `description`, `revision` (editing cycles), `editing_duration`, `print_date`, `printed_by`, `template`, and each user-defined property as `custom:<name>`. Dates without a zone are read as UTC.

`Loc.Anchor` is the paragraph's `xml:id` (for headings also `text:id`). With `Options.Spans`, spans carry bold, italic, underline, strike, superscript and subscript set by a span's own text style, and link targets of `text:a`.

## How a spreadsheet maps

One part of kind `sheet` per `table:table`, `Name` the sheet name, `Display` `hidden` when its table style says `table:display="false"`.

- **Cells** are read with the text reader (spans, display reasons, links, comments). Repeated rows and columns are counted, not stored, while empty; repeated cells with content are written out, bounded by `MaxCells` and the text budget. Coordinates are capped at 65,536 columns and 16 million rows. While a sheet is read its cells are held at about 48 bytes each before its tables are made, and that is counted against what is left of `MaxTextBytes`, so a sheet holds at most about 5 million cells at the default limit (a few bytes of repeated cells cannot fill memory).
- **Used regions** are tables, `Origin` `inferred`, `Loc.Range` in A1 notation (`A1:C3`): rows with cells that follow one another (or that a merged cell spans) form a band, and within a band touching columns with cells form a region. A region whose rectangle is more than four times its cells (plus 1,024) is written as one table per run of cells in a row, with `odf.region_split`.
- **Header rows** come only from what the file declares, never from formatting. Rows in `table:table-header-rows` (print titles, repeated on each printed page) are a region's `HeaderRows` when they are its first rows (title rows above the region are empty). A region that is exactly a `table:database-range` containing a header (`table:contains-header` is true unless set to `false`) has `HeaderRows: 1` at least and `Origin` `native`.
- **Text** is what the cell displays (its paragraphs joined with `\n`); a cell with a value and no paragraph shows the value. **Dates and times stored as values are written in ISO 8601** (`2026-03-04`, `14:30:00` from `PT14H30M00S`), whatever the display, as core asks.
- **Merged cells** (`number-columns-spanned`, `number-rows-spanned`) are `Merges`; overlapping merges after the first are dropped.
- With `Options.Cells`, `Table.Cells` has each cell's formula (`table:formula` as written, never evaluated), and its type and stored value where the text does not show the value (`number`, `date`, `bool`). With `Options.Spans`, a cell's link is its `href`.
- **Display**: collapsed or filtered rows and columns are `DisplayCells` regions with `hidden`; a cell's style is read for `text:display="none"` and for `loext:opacity` of 0.
- **Comments** (`office:annotation` in a cell) are `note` blocks of type `comment` with `Loc.Range` the cell.
- **Validation lists** (`cell-content-is-in-list`) are `note` blocks of type `choices` after the tables, `Loc.Range` the bounding range of the cells that use the validation, `field` its name. A list of values gives one per line; a list taken from cells gives `=` and the reference as the file writes it.
- **Named ranges, named expressions and database ranges** (workbook-wide and sheet-local, the latter as `Sheet!name`) go to the `stored` part as `name: address` paragraphs with `stored=defined_name`; database ranges also have `Attrs["kind"]="database_range"`.
- Shapes on a sheet (`table:shapes`) give their text and pictures after the tables.

## How a presentation maps

One part of kind `slide` per `draw:page`. `Name` is the text of the title frame (`presentation:class="title"`), else the page's `draw:name`. `Display` `hidden` when the page's drawing-page style says `presentation:visibility="hidden"`.

- Frames, shapes and groups are put in reading order: the title first, then from top to bottom (`svg:y`) and, at one height, left to right (`svg:x`); an item with no position keeps the place of the one before it.
- The title's paragraphs are `heading` level 1. Footer, date-time and page-number placeholders are `footer` blocks, header placeholders `header` blocks. Other text is paragraphs and list items as in a text document.
- **Master-page text.** The shapes of a slide's master page (`draw:master-page-name` → `style:master-page` in styles.xml) that are not placeholders (no `presentation:class`, not `presentation:placeholder="true"`) are given on every slide that uses the master and shows its objects, before its notes, in reading order, as go-fast-ooxml gives a layout's and master's own text. A slide whose drawing-page style (or a parent of it) declares `presentation:background-objects-visible="false"` does not show them and gets none. A hidden slide's copy is hidden with the slide. The text of a master is on every slide that uses it, so a long master and many slides can ask for much more than the file holds: each copy is charged to the text budget, at most 2^20 blocks are repeated in this way over a presentation, and each slide past that says so with `limit.exceeded`, the first with the number of slides left without their copy. What a bound leaves out of a master's own content is said on each slide that shows it, in the one warning that slide gives for that bound; an embedded document or picture it leaves out is said once, on the document. Their text is `header` if the shape starts in the upper half of the master's page layout (`fo:page-height`; 15.75 cm when none is given), `footer` if in the lower; pictures and tables keep their kinds. The master's placeholders (title, outline, header, footer, date-time, page-number) and its notes master are not content. Masters no slide uses are not read.
- **Speaker notes** (`presentation:notes`) are one `note` block of type `speaker` at the end of the slide.

## Flat files

A flat file is one XML document (`office:document`) holding metadata, styles, master pages and body. It is read like a package whose streams are that document. Pictures held in it (`office:binary-data`) are decoded (up to 64 MiB of base64 each, charged to `MaxDecodedBytes`; a larger one is `limit.exceeded`); their `Image.ID` is `inline-` and the first 16 hex digits of the SHA-256 of their bytes, so equal pictures have equal IDs; their MIME type and size come from decoding PNG, JPEG and GIF headers.

## What the file declares

Everything is extracted. `Display` (go-fast-core schema 0.2) on a block, span, table region or part records only what the file itself states about display; a block with no `Display` says nothing about whether a reader can see it. Following the file's own style inheritance to find a property counts as reading it.

`hidden` is set for:

- `text:display="none"` on a text, paragraph or table-cell style, directly or inherited through `style:parent-style-name`
- `text:display="none"` on a field
- a section with `text:display="none"`
- `text:hidden-paragraph`, `text:hidden-text` and sections with `text:display="condition"` that the file records as hidden (`text:is-hidden="true"`)
- table rows and columns with `table:visibility` `collapse` or `filter`
- a sheet whose table style has `table:display="false"`, LibreOffice's cached copies of linked sheets included
- a slide whose drawing-page style has `presentation:visibility="hidden"`
- a frame or shape with `draw:display="none"`, or on a layer with `draw:display="none"`; this includes a form control placed by a `draw:control` with `draw:display="none"` or on such a layer, whose paragraph (and choices note) is `hidden`
- a header or footer switched off with `style:display="false"`

`no_ink` is set for:

- `loext:opacity` of 0 on a definite `fo:color` (LibreOffice writes `loext:opacity="0%"` beside the automatic font colour in its default styles; there it describes no colour and is not read as no ink)

Not reported: a condition that would have to be evaluated (`text:display="condition"` without `text:is-hidden`; conditions are never evaluated), and `draw:display="printer"`.

Colour, size, position and overlap are not judged: text colour is not compared with any background, font sizes and text scale are not read for display, and shapes over text are not looked at. Characters are kept as written, zero-width and bidirectional controls and tag characters included. Data OpenDocument files keep and never show (metadata, user field declarations, named ranges and expressions, database ranges, conditional-text conditions and their other value, formula strings, comments, tracked deletions, form values) is extracted as data with no reason; the `stored` part, and blocks marked `stored` elsewhere (conditional-text field codes), are left out by renderers unless `RenderOptions.Stored`, or `RenderOptions.StoredKinds` names their kind.

## Warnings

Shared codes raised: `part.failed`, `part.no_text`, `text.tracked_changes`, `structure.heading_level_clamped`, `child.failed` (an embedded file could not be taken out of the package; it is listed in `Children` unread), `input.repaired`, `input.ambiguous`, `input.active_content` (Basic or other scripts in the package, event handlers, links that run macros, applets; none of it is read or run), `input.external_reference` (linked pictures, linked sections, plug-ins and floating frames, linked objects, an auto-reload address; nothing is fetched), `limit.exceeded`.

Codes for the events below are shared by every go-fast library (go-fast-core v0.1.2); before v0.2.0 this library raised them as `odf.unread_content`, `odf.region_split`, `odf.formula_no_value`, `odf.part_damaged` and `odf.control_characters_removed`. The Go names `WarnUnreadContent`, `WarnRegionSplit`, `WarnFormulaNoValue`, `WarnPartDamaged` and `WarnControlCharsRemoved` are deprecated and have the shared values. This library has no codes of its own but the deprecated `odf.property_truncated`, no longer raised.

| Code | What happened |
|---|---|
| `input.unread_content` (shared) | The document holds content this version does not read: an embedded formula or chart it could not read, an embedded object whose directory has no `content.xml`, a document held whole in a flat file, plug-ins and floating frames. The message counts them |
| `table.region_split` (shared) | An area of a sheet with thinly spread cells was written as one table per run of cells in a row (info) |
| `table.formula_no_value` (shared) | Cells of a sheet hold formulas with no stored result (no value attribute, no text), so they show nothing; one warning per sheet with the count (info). The formulas stay in `Table.Cells` |
| `input.stream_damaged` (shared) | A supporting stream (manifest, styles, metadata) could not be read to its end; what was read is used. More styles or manifest entries than are read is `limit.exceeded` |
| `text.control_characters_removed` (shared) | Control characters with no meaning as text were removed, from the text of a part or from the document properties; the message gives the count (info) |
| `input.decrypted` (shared) | The package was decrypted, with `Metadata.Encrypted` set; the message names the cipher and key derivation, and that the password given opened it (info) |

## Errors

`ErrMalformed` (not a zip archive, no `content.xml`, archive too damaged), `ErrUnsupported` (not OpenDocument, including a package whose media type is not OpenDocument's and whose `content.xml` has no OpenDocument body; a kind not read, as listed under "Not read"; an encryption this version does not decrypt), `ErrPasswordRequired` (encrypted, no password), `ErrWrongPassword` (the checksum or authentication tag does not match the password given), `ErrLimitExceeded` (also: the manifest asks for more key-derivation work than the caps below), `ErrNoPartsSelected`.

## Encryption

With `Options.Password`, an encrypted package is decrypted:

- AES-CBC (256, 192 or 128-bit) with a PBKDF2 key, from a SHA-1 or SHA-256 start key, checked by `SHA1/1K` or `sha256-1k` checksum: what ODF 1.2 and 1.3 packages hold, and what LibreOffice writes by default.
- The `encrypted-package` member of ODF 1.4, which holds the whole package: AES-GCM with an Argon2id key (LibreOffice's experimental `loext:` parameter names and ODF 1.4's `manifest:` ones). Argon2id and BLAKE2b are in `internal/argon2`, written from RFC 9106 and RFC 7693.
- Blowfish CFB (ODF 1.0 and 1.1, OpenOffice.org 1 and 2) with a PBKDF2 key from a SHA-1 start key, in `internal/blowfish`. Its tables, the hexadecimal digits of pi, are computed with `math/big` on first use, not typed in; the tests check them and Schneier's test vectors.

A decrypted document has `Metadata.Encrypted` set and the info warning `input.decrypted`, which names the cipher and key derivation. An algorithm, key derivation or checksum other than these gives `ErrUnsupported`. Without a password an encrypted file is `ErrPasswordRequired`; a password is never guessed. The password is passed to embedded documents as core says; objects inside an encrypted package are decrypted with it. `Metadata.Encrypted` is set on a decrypted document.

The manifest sets the cost of a key, so it is capped: PBKDF2 at 10 million iterations for a member and 30 million for a package; Argon2id at 256 MiB of memory, 64 passes, 64 lanes, and memory times passes at 1 GiB for a key (2 GiB for a package). Inflated bytes of decrypted members count against `MaxDecodedBytes` like any others. An encrypted member that the archive also compresses (writers store them) may inflate to no more than the size of the package before it is decrypted; past that the package is damaged (`ErrMalformed`). ODF 1.4's inner package, decrypted and inflated, may be no larger than `MaxInputBytes` (counted as it is inflated; `ErrLimitExceeded` past it), and its members share the outer package's key-derivation caps.

## Limits

Every limit of `gofastcore.Options.Limits` applies. `MaxDecodedBytes` (default 2 GiB) is charged per byte read from the archive and shared with embedded documents. `MaxTextBytes` (default 256 MiB) is charged for every block (`BlockCost` plus its text and marker), every table cell (`CellCost` plus its text) and link targets; a paragraph stops growing when it would exceed what is left, so `text:s` with a huge count cannot expand past the budget. `Extractor.MaxCells` (default 20 million) bounds table cells; repetition attributes are capped at 2^20 rows or cells each (a sheet's rows at 2^24, so that every row keeps its address), tables of a text document at 16,384 columns, and sheets at 65,536 columns and 2^24 rows. The XML scanner refuses DTDs and bounds nesting depth and token size. A cut archive is read through the members' own headers (`input.repaired`).

Fixed bounds that can leave content out say so with `limit.exceeded` (severity error), once per bound on the part concerned (on the document for content validations, styles and the manifest; a part not selected by `Options.Parts` is not read and says nothing), with what and how much was left out: table cells past those columns and rows; repetitions of a text table's row past 2^20; a cell's stored value and formula past 64 KiB; a formula's text and StarMath source past 64 KiB; a chart's table past 16,384 rows, 1,024 columns or 64 KiB in a cell; embedded documents past 4,096; form controls past 16,384, a list's options and a drop-down field's values past 4,096; content validations past 16,384; names and labels that reach the output past 4 KiB, cut where a character starts (note citations, list and heading numbers, axis titles, chart classes, anchors, authors and dates of comments and deletions, names of sheets, slides, tables, charts, fields, controls, named ranges and validations, embedded documents' member names, manifest media types, the language, generator and author names and property names of the metadata), counted only where what holds them is given; addresses past 8 KiB (links, linked pictures, the metadata's template); metadata values past 64 KiB, keywords and user-defined properties past 256 (on the document); pictures held in a flat file in more than 64 MiB of base64; spaces past 65,536 asked for by one `text:s`; a list that continues one whose identifier or style name was not remembered (past 16,384 of each; its numbering restarts); styles past 65,536; manifest entries past 131,072. A picture whose bytes `MaxDecodedBytes` cannot hold is said the same way. No other code is used for these cuts: `odf.property_truncated` is no longer raised, and `odf.unread_content` no longer covers large pictures.

## Corpus test

`TestCorpus` runs every odt, ods, odp and flat file of the public corpus (`GOFAST_CORPUS`, else `../.testdata/corpus`; skipped when absent) under `dev/` and `heldout/`, each with a 10 s limit, 32 MiB of text and 512 MiB of decompression, and logs a tally per set (files, results and errors by kind, blocks and files by display reason, warnings by code). It fails on a panic, a Validate failure or a timeout. With `-v` it also lists each display reason in `dev/` files with an excerpt. Held-out files are counted, never shown.

## Fuzzing

```
go test -run '^$' -fuzz '^FuzzContent$' -fuzztime=20s .
```

Targets: `FuzzExtract` (whole files), `FuzzContent`, `FuzzSheet`, `FuzzSlides`, `FuzzStyles`, `FuzzMeta`, `FuzzManifest`, `FuzzFlat`, and in `internal/odpkg` `FuzzEncryptionData` (a manifest's `manifest:encryption-data` and the encrypted member's bytes, with the key-derivation caps lowered so each input is cheap). The input of a stream target (all but `FuzzExtract` and `FuzzEncryptionData`) is the inside of a stream's root element (the target writes the root and its namespace declarations), and the packages it builds store their members uncompressed. Both matter: the fuzzing engine minimises each input that finds new coverage at a cost that grows with the square of its length, and with whole streams of 1–2 KB in compressed packages a worker spent nearly all its time minimising. `TestMutations` runs a seeded set of mutations of every target of the root package under plain `go test`.

## Embedded objects and form controls

In every kind of document, `draw:object` and `draw:object-ole` in a frame are read like this:

- **Formulas** (a sub-document `Object N/` whose `content.xml` is MathML, or `math:math` held in the element in a flat file) become a `formula` block, `Attrs["notation"]="plain"`. The text is **converted from the MathML**: tokens as written, `a^b`, `a_b`, `(a)/(b)`, `sqrt(x)`, `root(x, n)`, fences as `( , )`, matrix rows separated by `;`. LibreOffice's StarMath source, from `<annotation encoding="StarMath 5.0">`, is kept in `Attrs["starmath"]`, and is the text when the MathML gives none.
- **Charts** (a sub-document with `chart:chart`) give a `caption` block with the chart's title, then a `table` block of the data the chart keeps in its own table (header rows as `HeaderRows`), with `Attrs`: `role=chart`, `chart` the chart class (`bar`, `line`, ...), `x_axis` and `y_axis` the axis titles, `name` the frame's name.
- When a formula or chart became blocks, the frame's replacement picture (`ObjectReplacements/Object N`) is not given: it is a drawing of the same thing.
- **Other sub-documents** (a text document or spreadsheet embedded in another) are packed as a package of their own (members stored, `mimetype` first, the media type from the manifest or else from its body element) and handed to `Options.ExtractChild`; **OLE objects** (`draw:object-ole`, a member such as `Object 4`) are handed on as stored, with the manifest's media type. Each appears in `Document.Children`, extracted or listed. The decompression budget is settled with the shared budget before and after each. Linked objects (an address outside the package) are counted in `input.external_reference` and not fetched.
- **Form controls** (`office:forms`, placed by `draw:control`) give a paragraph where they stand: label and value as `label: value` (a check box's value is `checked`), `Attrs["field"]` the control's name and `Attrs["control"]` its kind; a list box or combo box also gives a `choices` note of its options. Controls that no `draw:control` places go to the `stored` part with `stored=form_field`. Form values are data: the values of hidden controls, password fields and unplaced controls are extracted with no display reason. A placed control is `hidden` when its `draw:control` declares `draw:display="none"` or stands on a layer with `draw:display="none"`; that is the only switch ODF 1.3 gives a placed control (`form:printable` concerns printing only and is not read; the graphic style of a control has no display property).

## Not read

Drawings (`.odg`, `.otg`, `.fodg`), charts (`.odc`), formulas (`.odf`), images (`.odi`) and databases (`.odb`) as documents in their own right (`ErrUnsupported`); documents held whole inside a flat file's `draw:object` (`odf.unread_content`); change-tracked insertions are not marked in the text. The OpenOffice.org 1 formats (`.sxw`, `.sxc`, `.sxi`, `.sxd`, `.sxm`, `.sxg` and the templates `.stw`, `.stc`, `.sti`, `.std`) are refused (`ErrUnsupported`).

## Provenance

The XML scanner, text cleaning and display-span settling are copied from go-fast-ooxml (same author and licence); see `NOTICE`. Zip recovery is go-fast-core's `ziprecover`.
