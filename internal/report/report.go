// Package report renders validation results as human-readable text or JSON.
package report

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subhanmalik138/go-csv-validator/internal/validate"
)

// Text renders a compact human-readable report.
func Text(file string, res validate.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "csvvalidate report — %s\n", file)
	fmt.Fprintf(&b, "rows: %d (valid: %d, invalid: %d)\n", res.TotalRows, res.ValidRows, res.TotalRows-res.ValidRows)
	if len(res.Errors) == 0 {
		b.WriteString("no errors — file satisfies the contract\n")
		return b.String()
	}
	fmt.Fprintf(&b, "errors: %d", len(res.Errors))
	if res.Truncated {
		b.WriteString(" (truncated to the error limit)")
	}
	b.WriteString("\n\n")
	for _, e := range res.Errors {
		loc := fmt.Sprintf("row %d", e.Row)
		if e.Row == 0 {
			loc = "header"
		}
		fmt.Fprintf(&b, "%s, column %q: [%s] %s\n", loc, e.Column, e.Rule, e.Message)
	}
	return b.String()
}

// SummaryJSON is the JSON report document.
type SummaryJSON struct {
	File      string              `json:"file"`
	TotalRows int                 `json:"totalRows"`
	ValidRows int                 `json:"validRows"`
	Errors    []validate.RowError `json:"errors"`
	Truncated bool                `json:"truncated"`
}

// JSON renders the report as indented JSON.
func JSON(file string, res validate.Result) (string, error) {
	if res.Errors == nil {
		res.Errors = []validate.RowError{}
	}
	doc := SummaryJSON{
		File:      file,
		TotalRows: res.TotalRows,
		ValidRows: res.ValidRows,
		Errors:    res.Errors,
		Truncated: res.Truncated,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
