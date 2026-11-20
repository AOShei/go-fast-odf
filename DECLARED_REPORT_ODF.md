# go-fast-odf: declared, not judged (report)

(Written here because the sandbox refused writes to the workspace root; copy it to `../DECLARED_REPORT_ODF.md`.)

## 1. Branch and core

Branch `declared-not-judged` (local; not pushed; no pull request). Pins **go-fast-core `05bbddb`** (schema 0.2). Commits after the parent state `cda82d5`: `940e19d` (builds: declared display only), `f8fcc8f` (tests), `acf278f` (corpus test wording), `bdb4df5` (README, NOTICE), `5353c1f` (plan, comments), and this report.

## 2. Declarations reported (as in the README)

`hidden`:
- `text:display="none"` on a text, paragraph or table-cell style, directly or inherited through `style:parent-style-name`
- `text:display="none"` on a field
- a section with `text:display="none"`
- `text:hidden-paragraph`, `text:hidden-text` and sections with `text:display="condition"` that the file records as hidden (`text:is-hidden="true"`)
- table rows and columns with `table:visibility` `collapse` or `filter`
- a sheet whose table style has `table:display="false"`, LibreOffice's cached copies of linked sheets included
- a slide whose drawing-page style has `presentation:visibility="hidden"`
- a frame or shape with `draw:display="none"`, or on a layer with `draw:display="none"`
- a header or footer switched off with `style:display="false"`

`no_ink`:
- `loext:opacity` of 0 on a definite `fo:color` (not beside the automatic font colour, where LibreOffice writes it in default styles)

Not reported: `text:display="condition"` without `text:is-hidden` (no condition is evaluated; the former "literally true condition" reading is gone) and `draw:display="printer"`. The README states that colour, size, position and overlap are not judged.

## 3. Removed

- The `low_contrast` check and all background resolution (character, paragraph, cell, section, frame, page, master page, slide fill), including the master-background lookup.
- The `tiny` check, with font-size and text-scale resolution.
- `transparent` (now `no_ink`); `invisible_chars` (the metadata scan and its `text.hidden` warning).
- Core's outlier rule as applied here, `TestUnshownOutlier`, `TestUnshownOutlierChars`; the `text.hidden` and `text.unshown_outlier` warnings.
- The link-cache-sheet exemption; `HiddenChecked`; the README text for all of these.
- Command: `-exclude-hidden` became `-exclude-display[=hidden,no_ink]`; `text.hidden` is no longer echoed to standard error.

Size: non-test Go +187 / -472 lines; tests +202 / -136.

## 4. Corpus, before (`cda82d5`) and after (`5353c1f`)

| | dev before | dev after | held-out before | held-out after |
|---|---|---|---|---|
| files | 126 | 126 | 48 | 48 |
| errors (malformed, password) | 1, 1 | 1, 1 | 1, 1 | 1, 1 |
| Validate failures, panics, timeouts | 0 | 0 | 0 | 0 |
| blocks by reason | invisible=3 | hidden=27 | none | none |
| files by reason | invisible=2 | hidden=4 | none | none |
| `text.hidden` warnings | 2 | 0 | 0 | 0 |

Dev after: the filtered rows of `lo-tdf142231.ods` (already reported before), the hidden columns of `lo-tdf154005.ods` (as before), and the link-cache sheets of `lo-lookup_target.fods` (23 blocks on 3 sheets) and `lo-fdo43700test.ods` (1 block), which are newly reported. No inferred reason fired in the corpus before the change.

## 5. Benchmark (`BenchmarkExtract`, ns/op, 3 to 5 runs, same machine)

| file | before | after | allocs/op before → after |
|---|---|---|---|
| .odt | 126,000–131,000 | 128,000–136,000 | 723 → 711 |
| .ods | 193,000–194,000 | 194,000–204,000 | 1,099 → 1,020 |
| .odp | 439,000–444,000 | 443,000–448,000 | 1,276 → 1,239 |

The timings are within noise on a shared machine. Bytes and allocations per op fell. There is no instruction counter here, so these are wall-clock ns/op, not instr/op.

## 6. R6

`TestCharactersKept` (declared_test.go): tag characters (U+E0001 to U+E007F), a right-to-left override with its pop, a run of U+200B/200C/200D/2060 and a variation selector all survive unchanged in an .odt paragraph and an .ods cell, with no reason. `TestHiddenText` also checks that four zero-width spaces are kept with no reason.

## 7. Points for Andrew

- **Specification details to check** (no web access): `loext:opacity` is a LibreOffice extension, not ODF 1.3; I believe the `filter` value of `table:visibility` is in ODF 1.2 and later.
- The `git worktree` route for the "before" run was refused by the sandbox: the corpus path needed an environment variable. So the parent commit was checked out on the clean tree instead, and the branch was then restored. The worktree was removed.
