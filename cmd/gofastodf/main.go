// Command gofastodf extracts OpenDocument files (odt, ods, odp, odg and
// the flat fodt, fods, fodp, fodg) into the shared document model of the go-fast
// libraries and prints them as JSON, Markdown or plain text.
//
//	gofastodf [flags] file-or-directory...
//
// A directory stands for the files in it and below it whose extensions
// are in the extensions table below (not .otm or .oth). With one input
// the JSON is indented; with several, each document is one line. Only
// the documents go to standard output: everything said about them goes
// to standard error, and the exit status is 1 if any input could not be
// read.
//
// Its shape follows go-fast-ooxml's command gofastooxml (same author, MIT
// License).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-odf"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// The extensions of the files this command picks out of a directory.
var extensions = map[string]bool{
	".odt": true, ".ott": true, ".odm": true, ".ods": true, ".ots": true, ".odp": true, ".otp": true,
	".odg": true, ".otg": true, ".fodt": true, ".fods": true, ".fodp": true, ".fodg": true,
}

// childTypes are the media types of the embedded documents this command
// hands to this library: the OpenDocument formats it reads. Anything else
// (an OLE object, a drawing, a chart or formula held alone) is listed and
// not read.
var childTypes = map[string]bool{
	"application/vnd.oasis.opendocument.text":                  true,
	"application/vnd.oasis.opendocument.text-template":         true,
	"application/vnd.oasis.opendocument.text-master":           true,
	"application/vnd.oasis.opendocument.text-master-template":  true,
	"application/vnd.oasis.opendocument.text-web":              true,
	"application/vnd.oasis.opendocument.spreadsheet":           true,
	"application/vnd.oasis.opendocument.spreadsheet-template":  true,
	"application/vnd.oasis.opendocument.presentation":          true,
	"application/vnd.oasis.opendocument.presentation-template": true,
	"application/vnd.oasis.opendocument.graphics":              true,
	"application/vnd.oasis.opendocument.graphics-template":     true,
	"application/vnd.oasis.opendocument.text-flat-xml":         true,
	"application/vnd.oasis.opendocument.spreadsheet-flat-xml":  true,
	"application/vnd.oasis.opendocument.presentation-flat-xml": true,
	"application/vnd.oasis.opendocument.graphics-flat-xml":     true,
}

// run is the command: it reads args and writes documents to stdout and
// messages to stderr, and returns the exit status.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gofastodf", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		format   = flags.String("format", "json", "what to print: json, markdown or text")
		output   = flags.String("o", "", "write to this file instead of standard output")
		parts    = flags.String("parts", "", "parts to read, by number: 3, or 2-5 (the body of a text document is part 1; sheets and slides count from 1)")
		images   = flags.String("images", "meta", "pictures: meta (a block for each, with its place, size and alternative text), data (their bytes too) or none")
		spans    = flags.Bool("spans", false, "report bold, italic and the like, and links, as spans")
		cells    = flags.Bool("cells", false, "report the formulas, types and stored values of spreadsheet cells")
		password = flags.String("password", "", "password of an encrypted file (AES with PBKDF2 or Argon2id, Blowfish), also tried on embedded documents")
		maxCells = flags.Int64("max-cells", 0, "table cells kept from a document (0: the default, 20 million; -1: no limit)")
		maxBytes = flags.Int64("max-decoded-bytes", 0, "decompressed bytes read from a file (0: the default, 2 GiB; -1: no limit)")
		maxText  = flags.Int64("max-text-bytes", 0, "bytes of text a document may hold (0: the default, 256 MiB; -1: no limit)")
		quiet    = flags.Bool("quiet", false, "say nothing about files that were read; failures are still reported")
		version  = flags.Bool("version", false, "print the version and exit")
	)
	// The render flags shared by every go-fast command (COMMAND_FLAGS.md).
	renderFlags := gofastcore.AddRenderFlags(flags)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: gofastodf [flags] file-or-directory...")
		fmt.Fprintln(stderr, "Extracts OpenDocument files. A directory stands for the OpenDocument files in it and below it.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *version {
		fmt.Fprintln(stdout, "gofastodf", moduleVersion())
		return 0
	}
	fail := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "gofastodf: "+format+"\n", args...)
		return 2
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	if *format != "json" && *format != "markdown" && *format != "text" {
		return fail("unknown format %q: json, markdown or text", *format)
	}
	opts := gofastcore.Options{Spans: *spans, Cells: *cells, Password: *password}
	opts.Limits.MaxDecodedBytes, opts.Limits.MaxTextBytes = *maxBytes, *maxText
	switch *images {
	case "none":
		opts.Images = gofastcore.ImagesNone
	case "meta":
		opts.Images = gofastcore.ImagesMeta
	case "data":
		opts.Images = gofastcore.ImagesData
	default:
		return fail("unknown value %q for -images: none, meta or data", *images)
	}
	if *parts != "" {
		first, last, ok := strings.Cut(*parts, "-")
		if !ok {
			last = first
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(first))
		b, err2 := strconv.Atoi(strings.TrimSpace(last))
		if err1 != nil || err2 != nil || a < 1 || b < a {
			return fail("-parts wants a number or a range such as 2-5, not %q", *parts)
		}
		opts.Parts = gofastcore.PartRange{First: a, Last: b}
	}
	extractor := &gofastodf.Extractor{MaxCells: *maxCells}
	// An embedded object or document is read too if it is one of the
	// formats of this library, decided by its media type. Core's depth
	// limit (Limits.MaxDepth) bounds how deeply they nest.
	opts.Child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		if !childTypes[mime] {
			return nil, gofastcore.ErrUnsupported
		}
		return extractor.Extract(ctx, r, size, o)
	}

	var inputs []string
	status := 0
	for _, arg := range flags.Args() {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(stderr, "gofastodf: %v\n", err)
			status = 1
			continue
		}
		if !info.IsDir() {
			inputs = append(inputs, arg)
			continue
		}
		var found []string
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(stderr, "gofastodf: %v\n", err)
				status = 1
				return nil
			}
			// LibreOffice keeps a lock file named ".~lock.*#" beside an
			// open file.
			if !d.IsDir() && extensions[strings.ToLower(filepath.Ext(path))] && !strings.HasPrefix(d.Name(), ".~lock.") {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gofastodf: %v\n", err)
			status = 1
		}
		sort.Strings(found)
		inputs = append(inputs, found...)
	}

	out := stdout
	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			fmt.Fprintf(stderr, "gofastodf: %v\n", err)
			return 1
		}
		defer file.Close()
		out = file
	}
	w := bufio.NewWriter(out)
	defer w.Flush()
	render := &gofastcore.RenderOptions{}
	renderFlags.Apply(render)

	written := 0
	for _, path := range inputs {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "gofastodf: interrupted")
			return 1
		}
		o := opts
		o.Name = filepath.Base(path)
		doc, err := extractor.ExtractFile(ctx, path, &o)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			status = 1
			continue
		}
		if !*quiet {
			if note := remarks(doc); note != "" {
				fmt.Fprintf(stderr, "%s: %s\n", path, note)
			}
		}
		switch *format {
		case "json":
			enc := json.NewEncoder(w)
			enc.SetEscapeHTML(false)
			if len(inputs) == 1 {
				enc.SetIndent("", "  ")
			}
			err = enc.Encode(doc)
		case "markdown":
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "<!-- %s -->\n\n", strings.ReplaceAll(path, "--", "- -"))
			}
			if err = doc.WriteMarkdown(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		default:
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "==> %s <==\n", path)
			}
			if err = doc.WriteText(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		}
		if err == nil {
			err = w.Flush()
		}
		if err != nil {
			fmt.Fprintf(stderr, "gofastodf: writing %s: %v\n", path, err)
			return 1
		}
		written++
	}
	return status
}

// remarks sums up what a document's warnings say that a person running
// the command would want to know without reading the JSON: anything of
// severity error.
func remarks(doc *gofastcore.Document) string {
	counts := map[string]int{}
	for _, w := range doc.AllWarnings() {
		if w.Severity == gofastcore.SeverityError {
			counts[w.Code]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for i, code := range codes {
		if n := counts[code]; n > 1 {
			codes[i] = fmt.Sprintf("%s (%d)", code, n)
		}
	}
	return "warnings: " + strings.Join(codes, ", ")
}

// moduleVersion is the version this command was built from.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
