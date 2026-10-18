// Package schema defines the JSON contract a CSV file must satisfy.
//
// A contract lists the expected columns and the rule each column's values
// must follow: type, required-ness, numeric/length bounds, regex patterns,
// enumerations, and uniqueness.
package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// Column describes the validation rules for a single CSV column.
type Column struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"` // string, int, float, bool, date, email, url
	Required bool     `json:"required,omitempty"`
	Min      *float64 `json:"min,omitempty"`     // numeric bounds (int/float)
	Max      *float64 `json:"max,omitempty"`     // numeric bounds (int/float)
	MinLen   *int     `json:"minLen,omitempty"`  // rune-length bounds (any type)
	MaxLen   *int     `json:"maxLen,omitempty"`  // rune-length bounds (any type)
	Pattern  string   `json:"pattern,omitempty"` // Go regexp, matched against the whole value
	Enum     []string `json:"enum,omitempty"`    // allowed values
	Unique   bool     `json:"unique,omitempty"`  // value must not repeat in the file
	Format   string   `json:"format,omitempty"`  // time layout for Type "date" (default "2006-01-02")
}

// Schema is the full CSV contract.
type Schema struct {
	Columns []Column `json:"columns"`
}

var validTypes = map[string]bool{
	"string": true, "int": true, "float": true, "bool": true,
	"date": true, "email": true, "url": true,
}

// Load reads and validates a JSON schema file.
func Load(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	if err := s.Compile(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Compile checks the schema itself for contradictions: unknown types,
// duplicate column names, invalid regexes, and inconsistent bounds.
func (s *Schema) Compile() error {
	if len(s.Columns) == 0 {
		return fmt.Errorf("schema must declare at least one column")
	}
	seen := make(map[string]bool, len(s.Columns))
	for i, c := range s.Columns {
		if c.Name == "" {
			return fmt.Errorf("column %d: name is empty", i)
		}
		if seen[c.Name] {
			return fmt.Errorf("column %q: duplicate name", c.Name)
		}
		seen[c.Name] = true
		if !validTypes[c.Type] {
			return fmt.Errorf("column %q: unknown type %q", c.Name, c.Type)
		}
		if c.Pattern != "" {
			if _, err := regexp.Compile(c.Pattern); err != nil {
				return fmt.Errorf("column %q: invalid pattern: %w", c.Name, err)
			}
		}
		if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
			return fmt.Errorf("column %q: min %v > max %v", c.Name, *c.Min, *c.Max)
		}
		if c.MinLen != nil && c.MaxLen != nil && *c.MinLen > *c.MaxLen {
			return fmt.Errorf("column %q: minLen %d > maxLen %d", c.Name, *c.MinLen, *c.MaxLen)
		}
		if (c.Min != nil || c.Max != nil) && c.Type != "int" && c.Type != "float" {
			return fmt.Errorf("column %q: min/max only apply to int and float columns", c.Name)
		}
		if c.Type == "email" || c.Type == "url" {
			if len(c.Enum) > 0 {
				return fmt.Errorf("column %q: enum is redundant for type %q", c.Name, c.Type)
			}
		}
	}
	return nil
}

// Index returns the schema column and its position for a name.
func (s *Schema) Index(name string) (Column, int, bool) {
	for i, c := range s.Columns {
		if c.Name == name {
			return c, i, true
		}
	}
	return Column{}, -1, false
}
