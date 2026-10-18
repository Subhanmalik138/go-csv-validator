// Package validate checks CSV records against a schema contract.
//
// Rows are validated concurrently with a worker pool; the unique-column rule
// is coordinated through a mutex so duplicates are still detected exactly.
// Error output is truncated at a configurable limit so huge files do not
// produce huge reports.
package validate

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Subhanmalik138/go-csv-validator/internal/schema"
)

// RowError describes a single contract violation.
type RowError struct {
	Row     int    `json:"row"`     // 1-based data row (header excluded)
	Column  string `json:"column"`  // schema column name
	Rule    string `json:"rule"`    // required, type, min, max, minLen, maxLen, pattern, enum, unique, missing_column
	Value   string `json:"value"`   // offending value ("" for missing_column)
	Message string `json:"message"` // human-readable explanation
}

// Result is the outcome of validating one CSV file.
type Result struct {
	TotalRows int        `json:"totalRows"`
	ValidRows int        `json:"validRows"`
	Errors    []RowError `json:"errors"`
	Truncated bool       `json:"truncated"` // true when Errors hit the configured limit
}

// Validator checks rows against a compiled schema.
type Validator struct {
	sch      *schema.Schema
	workers  int
	errLimit int
	patterns map[string]*regexp.Regexp
	enums    map[string]map[string]struct{}

	mu   sync.Mutex
	seen map[string]map[string]int // column -> value -> first row
}

// New builds a Validator. workers<=0 defaults to NumCPU; errLimit<=0 is unlimited.
func New(s *schema.Schema, workers, errLimit int) (*Validator, error) {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	v := &Validator{
		sch:      s,
		workers:  workers,
		errLimit: errLimit,
		patterns: make(map[string]*regexp.Regexp, len(s.Columns)),
		enums:    make(map[string]map[string]struct{}),
		seen:     make(map[string]map[string]int),
	}
	for _, c := range s.Columns {
		if c.Pattern != "" {
			re, err := regexp.Compile(c.Pattern)
			if err != nil {
				return nil, fmt.Errorf("column %q: %w", c.Name, err)
			}
			v.patterns[c.Name] = re
		}
		if len(c.Enum) > 0 {
			set := make(map[string]struct{}, len(c.Enum))
			for _, e := range c.Enum {
				set[e] = struct{}{}
			}
			v.enums[c.Name] = set
		}
		if c.Unique {
			v.seen[c.Name] = make(map[string]int)
		}
	}
	return v, nil
}

type job struct {
	row    int // 1-based data row number
	record []string
}

type outcome struct {
	row    int
	errors []RowError
}

// Validate reads the CSV header and all records, then validates every row.
// The returned Result is sorted by (row, column) for stable output.
func (v *Validator) Validate(ctx context.Context, r *csv.Reader) (Result, error) {
	header, err := r.Read()
	if err != nil {
		return Result{}, fmt.Errorf("read header: %w", err)
	}

	// Map header names to positions; schema matches columns by name.
	pos := make(map[string]int, len(header))
	for i, h := range header {
		pos[strings.TrimSpace(h)] = i
	}
	var missing []RowError
	for _, c := range v.sch.Columns {
		if c.Required {
			if _, ok := pos[c.Name]; !ok {
				missing = append(missing, RowError{
					Row: 0, Column: c.Name, Rule: "missing_column",
					Message: fmt.Sprintf("required column %q not present in CSV header", c.Name),
				})
			}
		}
	}
	if len(missing) > 0 {
		return Result{Errors: missing}, nil
	}

	jobs := make(chan job)
	results := make(chan outcome)
	var wg sync.WaitGroup
	for w := 0; w < v.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results <- outcome{row: j.row, errors: v.validateRow(j.row, j.record, pos)}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	go func() {
		defer close(jobs)
		row := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			rec, err := r.Read()
			if err != nil {
				return // io.EOF ends the stream
			}
			row++
			jobs <- job{row: row, record: rec}
		}
	}()

	res := Result{}
	byRow := make(map[int][]RowError)
	for o := range results {
		res.TotalRows++
		if len(o.errors) == 0 {
			res.ValidRows++
		} else {
			byRow[o.row] = o.errors
		}
	}
	res.Errors = flattenErrors(byRow)
	if v.errLimit > 0 && len(res.Errors) > v.errLimit {
		res.Errors = res.Errors[:v.errLimit]
		res.Truncated = true
	}
	return res, nil
}

func flattenErrors(byRow map[int][]RowError) []RowError {
	rows := make([]int, 0, len(byRow))
	for r := range byRow {
		rows = append(rows, r)
	}
	sort.Ints(rows)
	var out []RowError
	for _, r := range rows {
		es := byRow[r]
		sort.SliceStable(es, func(i, j int) bool { return es[i].Column < es[j].Column })
		out = append(out, es...)
	}
	return out
}

func (v *Validator) validateRow(row int, rec []string, pos map[string]int) []RowError {
	var errs []RowError
	for _, c := range v.sch.Columns {
		idx, ok := pos[c.Name]
		var val string
		if ok && idx < len(rec) {
			val = strings.TrimSpace(rec[idx])
		}
		for _, e := range v.validateCell(row, c, val) {
			errs = append(errs, e)
		}
	}
	return errs
}

func (v *Validator) validateCell(row int, c schema.Column, val string) []RowError {
	mk := func(rule, msg string) RowError {
		return RowError{Row: row, Column: c.Name, Rule: rule, Value: val, Message: msg}
	}

	if val == "" {
		if c.Required {
			return []RowError{mk("required", fmt.Sprintf("column %q is required but empty", c.Name))}
		}
		return nil // empty optional cell: nothing else to check
	}

	var errs []RowError

	// Type check.
	switch c.Type {
	case "int":
		if _, err := strconv.ParseInt(val, 10, 64); err != nil {
			errs = append(errs, mk("type", fmt.Sprintf("%q is not a valid integer", val)))
		}
	case "float":
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			errs = append(errs, mk("type", fmt.Sprintf("%q is not a valid number", val)))
		}
	case "bool":
		if !isBool(val) {
			errs = append(errs, mk("type", fmt.Sprintf("%q is not a valid boolean (true/false)", val)))
		}
	case "date":
		layout := c.Format
		if layout == "" {
			layout = "2006-01-02"
		}
		if _, err := time.Parse(layout, val); err != nil {
			errs = append(errs, mk("type", fmt.Sprintf("%q does not match date layout %q", val, layout)))
		}
	case "email":
		if _, err := mail.ParseAddress(val); err != nil {
			errs = append(errs, mk("type", fmt.Sprintf("%q is not a valid email address", val)))
		}
	case "url":
		if _, err := url.ParseRequestURI(val); err != nil {
			errs = append(errs, mk("type", fmt.Sprintf("%q is not a valid URL", val)))
		}
	}

	// Numeric bounds (int/float only; enforced by schema.Compile).
	if c.Min != nil || c.Max != nil {
		if n, err := strconv.ParseFloat(val, 64); err == nil {
			if c.Min != nil && n < *c.Min {
				errs = append(errs, mk("min", fmt.Sprintf("%q is below minimum %v", val, *c.Min)))
			}
			if c.Max != nil && n > *c.Max {
				errs = append(errs, mk("max", fmt.Sprintf("%q is above maximum %v", val, *c.Max)))
			}
		}
	}

	// Length bounds.
	if c.MinLen != nil || c.MaxLen != nil {
		n := len([]rune(val))
		if c.MinLen != nil && n < *c.MinLen {
			errs = append(errs, mk("minLen", fmt.Sprintf("%q is shorter than %d characters", val, *c.MinLen)))
		}
		if c.MaxLen != nil && n > *c.MaxLen {
			errs = append(errs, mk("maxLen", fmt.Sprintf("%q is longer than %d characters", val, *c.MaxLen)))
		}
	}

	// Regex pattern.
	if re, ok := v.patterns[c.Name]; ok && !re.MatchString(val) {
		errs = append(errs, mk("pattern", fmt.Sprintf("%q does not match pattern %q", val, c.Pattern)))
	}

	// Enumeration.
	if set, ok := v.enums[c.Name]; ok {
		if _, ok := set[val]; !ok {
			errs = append(errs, mk("enum", fmt.Sprintf("%q is not one of the allowed values", val)))
		}
	}

	// Uniqueness across the file.
	if c.Unique {
		v.mu.Lock()
		first, dup := v.seen[c.Name][val]
		if dup {
			errs = append(errs, mk("unique",
				fmt.Sprintf("%q already appeared in row %d", val, first)))
		} else {
			v.seen[c.Name][val] = row
		}
		v.mu.Unlock()
	}

	return errs
}

func isBool(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "1", "0", "yes", "no", "y", "n":
		return true
	}
	return false
}
