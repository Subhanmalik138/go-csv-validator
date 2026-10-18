// csvvalidate checks a CSV file against a JSON schema contract before the
// data is pushed into an API or ETL pipeline.
//
// Usage:
//
//	csvvalidate -schema schema.json data.csv
//	csvvalidate -schema schema.json -in data.csv -format json -out report.json
//	csvvalidate -schema schema.json data.csv -clean valid_rows.csv
//
// Exit code 0 means the file satisfies the contract, 1 means violations were
// found, 2 means a usage or I/O error.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Subhanmalik138/go-csv-validator/internal/report"
	"github.com/Subhanmalik138/go-csv-validator/internal/schema"
	"github.com/Subhanmalik138/go-csv-validator/internal/validate"
)

const (
	exitOK = iota
	exitViolations
	exitError
)

func main() {
	var (
		schemaPath = flag.String("schema", "", "path to the JSON schema contract (required)")
		inPath     = flag.String("in", "", "path to the CSV file (default: first positional argument)")
		format     = flag.String("format", "text", "report format: text or json")
		outPath    = flag.String("out", "", "write the report to this file (default: stdout)")
		cleanPath  = flag.String("clean", "", "write rows that pass validation to this CSV file")
		workers    = flag.Int("workers", 0, "validation workers (default: number of CPUs)")
		errLimit   = flag.Int("limit", 100, "max errors in the report (0 = unlimited)")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: csvvalidate -schema schema.json [flags] data.csv\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *schemaPath == "" {
		fatal(exitError, "missing required flag: -schema")
	}
	in := *inPath
	if in == "" {
		if flag.NArg() == 0 {
			fatal(exitError, "missing input CSV file")
		}
		in = flag.Arg(0)
	}
	if *format != "text" && *format != "json" {
		fatal(exitError, "format must be text or json")
	}

	sch, err := schema.Load(*schemaPath)
	if err != nil {
		fatal(exitError, "schema: %v", err)
	}

	f, err := os.Open(in)
	if err != nil {
		fatal(exitError, "open input: %v", err)
	}
	defer f.Close()

	v, err := validate.New(sch, *workers, *errLimit)
	if err != nil {
		fatal(exitError, "validator: %v", err)
	}

	r := csv.NewReader(f)
	r.ReuseRecord = false
	res, err := v.Validate(context.Background(), r)
	if err != nil {
		fatal(exitError, "validate: %v", err)
	}

	if *cleanPath != "" {
		if err := writeClean(in, *cleanPath, sch, res); err != nil {
			fatal(exitError, "write clean file: %v", err)
		}
	}

	var rendered string
	if *format == "json" {
		rendered, err = report.JSON(in, res)
	} else {
		rendered = report.Text(in, res)
	}
	if err != nil {
		fatal(exitError, "render report: %v", err)
	}

	out := io.Writer(os.Stdout)
	if *outPath != "" {
		of, err := os.Create(*outPath)
		if err != nil {
			fatal(exitError, "create report file: %v", err)
		}
		defer of.Close()
		out = of
	}
	fmt.Fprint(out, rendered)

	if len(res.Errors) > 0 {
		os.Exit(exitViolations)
	}
}

// writeClean re-reads the input and copies every fully valid row to outPath.
// Rows are identified by comparing against the validated error set.
func writeClean(inPath, outPath string, sch *schema.Schema, res validate.Result) error {
	badRows := make(map[int]bool, len(res.Errors))
	for _, e := range res.Errors {
		if e.Row > 0 {
			badRows[e.Row] = true
		}
	}

	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	r := csv.NewReader(in)
	w := csv.NewWriter(out)
	defer w.Flush()

	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if err := w.Write(header); err != nil {
		return err
	}

	row := 0
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		row++
		if badRows[row] {
			continue
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	return nil
}

func fatal(code int, format string, args ...any) {
	msg := strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")
	fmt.Fprintf(os.Stderr, "csvvalidate: %s\n", msg)
	os.Exit(code)
}
