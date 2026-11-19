# go-fast-odf

Extracts OpenDocument files into the shared [go-fast-core](https://github.com/AOShei/go-fast-core) document model. Standard library only.

```go
import "github.com/AOShei/go-fast-odf" // package gofastodf

doc, err := gofastodf.ExtractFile(ctx, "report.odt", nil)
if err != nil { ... }
fmt.Println(doc.Markdown())
```

**Status: phase 1.** Text documents (`.odt`, templates, master documents) are read. Spreadsheets (`.ods`), presentations (`.odp`), the flat XML variants (`.fodt`, `.fods`, `.fodp`) and the command-line tool come in phase 2; decryption in phase 3. See `IMPLEMENTATION_PLAN.md`.

## API

- `Extract(ctx, r io.ReaderAt, size, opts)`, `ExtractFile(ctx, path, opts)`, `ExtractBytes(ctx, data, opts)`.
- `Extractor{MaxArchiveMembers, MaxCells}` implements `gofastcore.Extractor`; its zero value is ready and safe for concurrent use.

The format is decided from the package (its `mimetype` member, the manifest's root entry, or the body element of `content.xml`), never from the file name.

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
| `text:conditional-text` | its current value in the text; condition and both values as a `note` of type `field`, flagged `invisible` |
| fields (`text:date`, `text:page-number`, `text:author-name`, variables, ...) | their displayed value, in the text |
| `text:ruby` | base text, then the annotation in brackets |
| indexes (tables of contents, ...) | their body as paragraphs; their templates are not text |
| master page headers and footers (`styles.xml`) | `header` / `footer` blocks, first and last; each distinct one once |
| `text:user-field-decls` | the `stored` part: `name: value` paragraphs, `Attrs["stored"]="document_variable"`, `field` the name |

`meta.xml`: `dc:title`, `dc:subject`, `meta:keyword` (one entry each), `dc:language`, `meta:creation-date`, `dc:date`, `meta:generator`. `Authors` is `meta:initial-creator`, or `dc:creator` when there is no initial creator; a different `dc:creator` is `Extra["last_modified_by"]`. Also in `Extra`: `description`, `revision` (editing cycles), `editing_duration`, `print_date`, `printed_by`, `template`, and each user-defined property as `custom:<name>`. Dates without a zone are read as UTC.

`Loc.Anchor` is the paragraph's `xml:id` (for headings also `text:id`). With `Options.Spans`, spans carry bold, italic, underline, strike, superscript and subscript set by a span's own text style, and link targets of `text:a`.

## Hidden text

`HiddenChecked` is `invisible+tiny+low_contrast+invisible_chars`. All checks are cheap and always run; `Options.DeepHidden` changes nothing.

| Reason | When |
|---|---|
| `invisible` | `text:display="none"` in a text, paragraph or inherited style; `text:hidden-paragraph` and `text:hidden-text` that the file records as hidden (`text:is-hidden="true"`) or whose condition is literally true; a section with `text:display="none"` or `"condition"` (conditions are not evaluated, so a conditional section is flagged); fields with `text:display="none"`; collapsed or filtered table rows and columns; a header or footer switched off (`style:display="false"`); conditional-text notes and the stored part |
| `tiny` | font size under 2 pt after relative sizes and character scaling (`style:text-scale`) |
| `low_contrast` | `fo:color` against the nearest background: the characters' own background, else the paragraph's, else the table cell's, section's or frame's, else the page's (the first master page's layout), else white |
| `transparent` | LibreOffice's `loext:opacity` of 0 (found, not listed as checked) |
| `invisible_chars` | core's scan, on every block |

Limits of the checks: text whose colour is the program's choice (`style:use-window-font-color`) is not compared; the page background is that of the first master page; frames and shapes drawn over text are not compared (`occluded` is not checked); Asian and complex-script font sizes are not read.

## Warnings

Shared codes raised: `part.failed`, `part.no_text`, `text.hidden`, `text.tracked_changes`, `structure.heading_level_clamped`, `input.repaired`, `input.ambiguous`, `input.active_content` (Basic or other scripts in the package, event handlers, links that run macros, applets; none of it is read or run), `input.external_reference` (linked pictures, linked sections, plug-ins, an auto-reload address; nothing is fetched), `limit.exceeded`.

Codes of this library:

| Code | What happened |
|---|---|
| `odf.unread_content` | The document holds content this version does not read: embedded objects, form controls, plug-ins. The message counts them |
| `odf.part_damaged` | A supporting stream (manifest, styles, metadata) could not be read to its end; what was read is used |
| `odf.property_truncated` | A metadata value was cut at 64 KiB, or keywords or user-defined properties beyond 256 were left out |
| `odf.control_characters_removed` | Control characters with no meaning as text were removed |

## Errors

`ErrMalformed` (not a zip archive, no `content.xml`, archive too damaged), `ErrUnsupported` (not OpenDocument; a kind not read yet; an encrypted file when a password is given), `ErrPasswordRequired` (encrypted, no password), `ErrLimitExceeded`, `ErrNoPartsSelected`.

## Limits

Every limit of `gofastcore.Options.Limits` applies. `MaxDecodedBytes` (default 2 GiB) is charged per byte read from the archive and shared with embedded documents. `MaxTextBytes` (default 256 MiB) is charged for every block (`BlockCost` plus its text and marker), every table cell (`CellCost` plus its text) and link targets; a paragraph stops growing when it would exceed what is left, so `text:s` with a huge count cannot expand past the budget. `Extractor.MaxCells` (default 20 million) bounds table cells; repetition attributes are capped at 2^20 rows or cells each and tables at 16,384 columns. The XML scanner refuses DTDs and bounds nesting depth and token size. A cut archive is read through the members' own headers (`input.repaired`).

## Not read (yet)

Spreadsheets, presentations, drawings, flat XML files; decryption; embedded objects (formulas, charts, OLE) beyond their replacement picture; form controls; pictures held inline in a flat file; change-tracked insertions are not marked in the text; the OpenOffice 1.x formats (`.sxw`).

## Provenance

The XML scanner, text cleaning, hidden-span settling and zip recovery are copied from go-fast-ooxml (same author and licence); see `NOTICE`.
