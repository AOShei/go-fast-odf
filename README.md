# go-fast-odf

Extracts OpenDocument files into the shared [go-fast-core](https://github.com/AOShei/go-fast-core) document model. Standard library only.

```go
import "github.com/AOShei/go-fast-odf" // package gofastodf

doc, err := gofastodf.ExtractFile(ctx, "report.odt", nil)
if err != nil { ... }
fmt.Println(doc.Markdown())
```

**Status: phase 2.** Text documents (`.odt`), spreadsheets (`.ods`), presentations (`.odp`), their templates, and the flat XML variants (`.fodt`, `.fods`, `.fodp`) are read. Decryption comes in phase 3. See `IMPLEMENTATION_PLAN.md`.

## API

- `Extract(ctx, r io.ReaderAt, size, opts)`, `ExtractFile(ctx, path, opts)`, `ExtractBytes(ctx, data, opts)`.
- `Extractor{MaxArchiveMembers, MaxCells}` implements `gofastcore.Extractor`; its zero value is ready and safe for concurrent use. `Formats()` is `odt ods odp fodt fods fodp`.

The format is decided from the file (a package's `mimetype` member or manifest root entry, a flat file's `office:mimetype`, or the body element of the content), never from the file name. A flat file is reported as `fodt`, `fods` or `fodp` with the flat-XML media type, whatever kind its `office:mimetype` names.

## Command

`go install github.com/AOShei/go-fast-odf/cmd/gofastodf@latest`

```
gofastodf [flags] file-or-directory...
  -format json|markdown|text   -parts 2-5   -images meta|data|none
  -spans  -cells  -furniture  -stored  -part-markers  -exclude-display[=reasons]
  -max-cells N  -max-decoded-bytes N  -max-text-bytes N  -o file  -quiet
```

Documents go to standard output; warnings of severity error go to standard error; the exit status is 1 if any input could not be read.

The render flags are the series' shared ones (`COMMAND_FLAGS.md` in the workspace) and affect Markdown and text only; JSON always has everything:

| Flag | Meaning |
|---|---|
| `-furniture` | include running headers and footers |
| `-stored` | include the stored part (data the file keeps but shows nowhere: user fields, named ranges, form fields no page places) |
| `-part-markers` | put a line before each part saying which part it is |
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
| `text:conditional-text` | its current value in the text; condition and both values as a `note` of type `field`, with no display reason |
| fields (`text:date`, `text:page-number`, `text:author-name`, variables, ...) | their displayed value, in the text |
| an empty paragraph of the body whose style (after inheritance) has a bottom border and no top, left or right border, or is LibreOffice's "Horizontal Line" style (`Horizontal_20_Line`) | `thematic_break`, `Origin` `inferred` (ODF has no rule element; this is what LibreOffice's AutoCorrect makes of `---`). Not when a paragraph beside it has the same border (a box), and never in table cells, list items, notes, comments, frames, headers or footers. Bordered paragraphs with text and literal `* * *` stay paragraphs |
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
- **Speaker notes** (`presentation:notes`) are one `note` block of type `speaker` at the end of the slide.

## Flat files

A flat file is one XML document (`office:document`) holding metadata, styles, master pages and body. It is read like a package whose streams are that document. Pictures held in it (`office:binary-data`) are decoded (up to 64 MiB of base64 each, charged to `MaxDecodedBytes`); their `Image.ID` is `inline-` and the first 16 hex digits of the SHA-256 of their bytes, so equal pictures have equal IDs; their MIME type and size come from decoding PNG, JPEG and GIF headers.

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

Colour, size, position and overlap are not judged: text colour is not compared with any background, font sizes and text scale are not read for display, and shapes over text are not looked at. Characters are kept as written, zero-width and bidirectional controls and tag characters included. Data OpenDocument files keep and never show (metadata, user field declarations, named ranges and expressions, database ranges, conditional-text conditions and their other value, formula strings, comments, tracked deletions, form values) is extracted as data with no reason; the `stored` part is left out by renderers unless `RenderOptions.Stored`.

## Warnings

Shared codes raised: `part.failed`, `part.no_text`, `text.tracked_changes`, `structure.heading_level_clamped`, `input.repaired`, `input.ambiguous`, `input.active_content` (Basic or other scripts in the package, event handlers, links that run macros, applets; none of it is read or run), `input.external_reference` (linked pictures, linked sections, plug-ins, an auto-reload address; nothing is fetched), `limit.exceeded`.

Codes of this library:

| Code | What happened |
|---|---|
| `odf.unread_content` | The document holds content this version does not read: an embedded formula or chart it could not read, a document held whole in a flat file, plug-ins, pictures over 64 MiB held in a flat file. The message counts them |
| `odf.region_split` | An area of a sheet with thinly spread cells was written as one table per run of cells in a row |
| `odf.part_damaged` | A supporting stream (manifest, styles, metadata) could not be read to its end; what was read is used |
| `odf.property_truncated` | A metadata value was cut at 64 KiB, or keywords or user-defined properties beyond 256 were left out |
| `odf.control_characters_removed` | Control characters with no meaning as text were removed |

## Errors

`ErrMalformed` (not a zip archive, no `content.xml`, archive too damaged), `ErrUnsupported` (not OpenDocument; a kind not read yet; an encrypted file when a password is given), `ErrPasswordRequired` (encrypted, no password), `ErrLimitExceeded`, `ErrNoPartsSelected`.

## Limits

Every limit of `gofastcore.Options.Limits` applies. `MaxDecodedBytes` (default 2 GiB) is charged per byte read from the archive and shared with embedded documents. `MaxTextBytes` (default 256 MiB) is charged for every block (`BlockCost` plus its text and marker), every table cell (`CellCost` plus its text) and link targets; a paragraph stops growing when it would exceed what is left, so `text:s` with a huge count cannot expand past the budget. `Extractor.MaxCells` (default 20 million) bounds table cells; repetition attributes are capped at 2^20 rows or cells each and tables at 16,384 columns. The XML scanner refuses DTDs and bounds nesting depth and token size. A cut archive is read through the members' own headers (`input.repaired`).

## Corpus test

`TestCorpus` runs every odt, ods, odp and flat file of the public corpus (`GOFAST_CORPUS`, else `../.testdata/corpus`; skipped when absent) under `dev/` and `heldout/`, each with a 10 s limit, 32 MiB of text and 512 MiB of decompression, and logs a tally per set (files, results and errors by kind, blocks and files by display reason, warnings by code). It fails on a panic, a Validate failure or a timeout. With `-v` it also lists each display reason in `dev/` files with an excerpt. Held-out files are counted, never shown.

## Fuzzing

```
go test -run '^$' -fuzz '^FuzzContent$' -fuzztime=20s .
```

Targets: `FuzzExtract` (whole files), `FuzzContent`, `FuzzSheet`, `FuzzSlides`, `FuzzStyles`, `FuzzMeta`, `FuzzManifest`, `FuzzFlat`. A target's input is the inside of a stream's root element (the target writes the root and its namespace declarations), and the packages it builds store their members uncompressed. Both matter: the fuzzing engine minimises each input that finds new coverage at a cost that grows with the square of its length, and with whole streams of 1–2 KB in compressed packages a worker spent nearly all its time minimising. `TestMutations` runs a seeded set of mutations of every target under plain `go test`.

## Embedded objects and form controls

In every kind of document, `draw:object` and `draw:object-ole` in a frame are read like this:

- **Formulas** (a sub-document `Object N/` whose `content.xml` is MathML, or `math:math` held in the element in a flat file) become a `formula` block, `Attrs["notation"]="plain"`. The text is **converted from the MathML**: tokens as written, `a^b`, `a_b`, `(a)/(b)`, `sqrt(x)`, `root(x, n)`, fences as `( , )`, matrix rows separated by `;`. LibreOffice's StarMath source, from `<annotation encoding="StarMath 5.0">`, is kept in `Attrs["starmath"]`, and is the text when the MathML gives none.
- **Charts** (a sub-document with `chart:chart`) give a `caption` block with the chart's title, then a `table` block of the data the chart keeps in its own table (header rows as `HeaderRows`), with `Attrs`: `role=chart`, `chart` the chart class (`bar`, `line`, ...), `x_axis` and `y_axis` the axis titles, `name` the frame's name.
- When a formula or chart became blocks, the frame's replacement picture (`ObjectReplacements/Object N`) is not given: it is a drawing of the same thing.
- **Other sub-documents** (a text document or spreadsheet embedded in another) are packed as a package of their own (members stored, `mimetype` first, the media type from the manifest or else from its body element) and handed to `Options.ExtractChild`; **OLE objects** (`draw:object-ole`, a member such as `Object 4`) are handed on as stored, with the manifest's media type. Each appears in `Document.Children`, extracted or listed. The decompression budget is settled with the shared budget before and after each. Linked objects (an address outside the package) are counted in `input.external_reference` and not fetched.
- **Form controls** (`office:forms`, placed by `draw:control`) give a paragraph where they stand: label and value as `label: value` (a check box's value is `checked`), `Attrs["field"]` the control's name and `Attrs["control"]` its kind; a list box or combo box also gives a `choices` note of its options. Controls that no `draw:control` places go to the `stored` part with `stored=form_field`. Form values are data: the values of hidden controls, password fields and unplaced controls are extracted with no display reason. A placed control is `hidden` when its `draw:control` declares `draw:display="none"` or stands on a layer with `draw:display="none"`; that is the only switch ODF 1.3 gives a placed control (`form:printable` concerns printing only and is not read; the graphic style of a control has no display property).

## Not read (yet)

Drawings (`.odg`), charts, formulas and databases as documents in their own right; decryption; documents held whole inside a flat file's `draw:object`; change-tracked insertions are not marked in the text; master-page text of slides; the OpenOffice 1.x formats (`.sxw`).

## Provenance

The XML scanner, text cleaning, display-span settling and zip recovery are copied from go-fast-ooxml (same author and licence); see `NOTICE`.
