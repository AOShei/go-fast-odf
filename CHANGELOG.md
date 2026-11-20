# Changelog

All notable changes to go-fast-odf are recorded here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). No version has
been released yet.

## [Unreleased]

### Changed: declared, not judged (branch `declared-not-judged`)

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
