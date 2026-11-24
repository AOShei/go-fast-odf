# Benchmarks against the tools in use today

go-fast-odf is measured against the OpenDocument readers that data and AI pipelines use:
- from Python, odfpy, pandas `read_excel` with its odf engine, and Kreuzberg (which has a Rust core);
- pandoc, run once per file as pypandoc runs it;
- from Rust, the `calamine` crate.

The tables are in [`comparison.md`](comparison.md). This file says what they show and how to make them again.

## What the comparison shows

The inputs are ODT, ODS and ODP files found in Common Crawl and kept as served. The go-fast-odf figures are for v0.2.2 (go-fast-core v0.1.6), measured on 2026-11-24; the other tools' are from 2026-11-23. Every file gives the same text, or the same error, as with the earlier figures (commit 461e740, after #18), whose output was byte-identical to v0.2.0's.

**ODT** (2,457 files, 197 MB)
- **Speed.** go-fast-odf takes 1.70 s with one worker and 0.24 s with 24 (2.24 s and 0.38 s in the earlier figures).
  - Kreuzberg takes 2.33 s and 0.37 s, 1.4 and 1.5 times as long.
  - odfpy is 31 times slower and pandoc (one process per file) 135 times.
- **Accuracy.** Against LibreOffice's text export of 285 files, go-fast-odf has the highest F1:

  | Tool | F1 |
  |---|---|
  | go-fast-odf | 0.983 |
  | pandoc | 0.973 |
  | odfpy | 0.961 |
  | Kreuzberg | 0.717 |

  Kreuzberg's recall is 0.632: it leaves out much of the text the reference holds.
- **Memory.** go-fast-odf peaks at 43 MB with one worker and 175 MB with 24 (35 MB and 120 MB in the earlier figures). With 24 workers, Kreuzberg peaks at 1.4 GB, odfpy at 2.2 GB and pandoc at 5.2 GB.
- **Failures.** go-fast-odf failed on none. Kreuzberg failed on 1, pandoc on 15 and odfpy on 19.

**ODS** (1,265 files, 87 MB)
- **Speed.** go-fast-odf takes 4.99 s with one worker and 0.86 s with 24 (5.54 s and 0.98 s in the earlier figures).
  - The `calamine` crate is faster: 3.39 s and 0.61 s. In this harness it reads the cells' values only.
  - go-fast-odf also gives each table's merged cells and its hidden rows and columns, and the notes, each at its cell's address.
  - Kreuzberg is 2.5 and 2.9 times slower than go-fast-odf, odfpy and pandas 56 to 63 times.
- **Memory.** With 24 workers, go-fast-odf peaks at 392 MB. The others peak at:
  - calamine: 1.6 GB;
  - Kreuzberg: 3.8 GB;
  - odfpy and pandas: 10.6 and 11.6 GB.
- **Failures.** go-fast-odf failed on none. calamine and Kreuzberg failed on 11, pandas on 6 and odfpy on 1.
- **Accuracy depends on which text of a cell counts.** A cell holds its value (42956178) and, beside it, the text it displays (42,956,178).
  - go-fast-odf and odfpy give the displayed text; calamine, pandas and Kreuzberg give the value.
  - Against LibreOffice's CSV export as displayed, go-fast-odf has the highest F1, 0.986, with recall 0.996. calamine scores 0.871.
  - Against the export of values, the order turns round: calamine scores 0.976 and go-fast-odf 0.881.
  - Both are in the tables.
- **Earlier version.** Before #18, ODS took 8.52 s and 2.00 s. Its allocation (11.3 GB for the batch) kept the 24 workers waiting on the garbage collector.

**ODP** (61 files, 61 MB)
- go-fast-odf takes 0.22 s and 0.04 s; odfpy takes 5.43 s and 0.84 s and fails on 2 files.
- Kreuzberg does not read ODP.

## `GOGC`

Go's garbage collector can be given more memory for less work, with `GOGC` in the environment or `debug.SetGCPercent` in a program. The library keeps Go's default, 100. The table gives wall time, with peak memory in brackets.

| Set | `GOGC` | One worker | 24 workers |
|---|---|---|---|
| ODT | 100 (default) | 1.70 s (39 MB) | 0.26 s (177 MB) |
| ODT | 200 | 1.63 s (40 MB) | 0.23 s (273 MB) |
| ODT | 400 | 1.56 s (91 MB) | 0.20 s (473 MB) |
| ODS | 100 (default) | 4.94 s (242 MB) | 0.87 s (438 MB) |
| ODS | 200 | 4.79 s (239 MB) | 0.80 s (569 MB) |
| ODS | 400 | 4.68 s (466 MB) | 0.77 s (871 MB) |
| ODP | 100 (default) | 0.22 s (25 MB) | 0.04 s (95 MB) |
| ODP | 200 | 0.19 s (34 MB) | 0.04 s (157 MB) |
| ODP | 400 | 0.20 s (50 MB) | 0.04 s (135 MB) |

## How it is measured

- Each tool reads every file in one long-lived process, as a pipeline does, first with one worker and then with 24.
  - The time of a file is the call alone, timed after its bytes are read.
  - Wall time, CPU time and peak memory are those of the whole process tree.
- The machine is shared, so `measure.py` waits for it to be quiet before each run.
  - A run is repeated if other processes used it meanwhile.
  - The tables mark a run that stayed disturbed after five attempts.
- Accuracy is scored by words, compared as multisets (`score_text.py`, the same as for go-fast-html).
  - Precision is the share of a tool's words that are in the reference. Recall is the share of the reference's words that the tool gives.
  - Scores are averaged over files. A file a tool failed on scores 0.

## Reproducing

The Python tools are pinned in `requirements-text.txt` (the same environment as go-fast-html's benchmark), and pandoc must be on the `PATH`. Set `DATA` to a directory outside the repository and `SCRATCH` to a directory for LibreOffice's profile:

```sh
# Files: RTF and OpenDocument found in 61 of the 300 files of the crawl's
# columnar index, then at most 2,500 a type, at most 25 from one host,
# kept once by SHA-256.
curl -O https://data.commoncrawl.org/crawl-data/CC-MAIN-2025-43/cc-index-table.paths.gz
python -I bench/cc_find.py cc-index-table.paths.gz $DATA/found \
  $( (seq 0 14; seq 30 35; seq 60 64; seq 90 95; seq 120 136; seq 240 251) | xargs printf 'part-%05d ')
python -I bench/cc_fetch.py $DATA/found $DATA 2500 25     # $DATA/odt/, ods/, odp/, manifest.tsv
for t in odt ods odp; do ls $DATA/$t/*.$t > $DATA/$t.list; done

# Tools.
(cd bench/godoc && go build -o godoc .)
(cd bench/rsdoc && CARGO_TARGET_DIR=../../../bench-env/rust-target cargo build --release)
R=bench/run_all_doc.sh; export BENCH_ENV=../bench-env
$R odf $DATA/odt.list $DATA/results-odt go-fast odfpy pandoc kreuzberg > $DATA/run.log
$R odf $DATA/ods.list $DATA/results-ods go-fast odfpy pandas-odf kreuzberg rs-calamine >> $DATA/run.log
$R odf $DATA/odp.list $DATA/results-odp go-fast odfpy >> $DATA/run.log
for t in odt ods odp; do for g in 100 200 400; do for w in 1 24; do
  GOGC=$g python -I bench/measure.py $DATA/gogc/$t/gogc$g-$w/measure.json -- \
    bench/godoc/godoc odf $DATA/$t.list $DATA/gogc/$t/gogc$g-$w $w
done; done; done

# Accuracy, and the report.
for t in odt ods; do
  python -I bench/reference_doc.py $DATA/$t.list $DATA/reference-$t 300 $SCRATCH
  python -I bench/score_text.py $DATA/reference-$t $DATA/results-$t/*-1 > $DATA/scores-$t.json
done
python -I bench/reference_doc.py $DATA/ods.list $DATA/reference-ods-shown 300 $SCRATCH shown
python -I bench/score_text.py $DATA/reference-ods-shown $DATA/results-ods/*-1 > $DATA/scores-ods-shown.json
python -I bench/report_doc.py odf $DATA $DATA/gogc > bench/comparison.md
```

`reference_doc.py` runs LibreOffice inside limits:
- its own session and process group, killed as a group;
- a file-size limit of 256 MB;
- a private profile under `$SCRATCH`, removed at the end.

`run_doc.py`, `run_all_doc.sh`, `report_doc.py` and `rsdoc` also serve the go-fast-email and go-fast-rtf benchmarks; `godoc` here runs go-fast-odf alone.
