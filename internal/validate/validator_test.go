package validate

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/Subhanmalik138/go-csv-validator/internal/schema"
)

func fptr(f float64) *float64 { return &f }
func iptr(i int) *int         { return &i }

func testSchema() *schema.Schema {
	return &schema.Schema{Columns: []schema.Column{
		{Name: "id", Type: "int", Required: true, Unique: true, Min: fptr(1)},
		{Name: "name", Type: "string", Required: true, MinLen: iptr(2), MaxLen: iptr(40)},
		{Name: "email", Type: "email", Required: true},
		{Name: "age", Type: "int", Min: fptr(0), Max: fptr(150)},
		{Name: "role", Type: "string", Enum: []string{"admin", "user", "guest"}},
		{Name: "joined", Type: "date"},
		{Name: "code", Type: "string", Pattern: `^[A-Z]{3}-\d{4}$`},
		{Name: "opt", Type: "float"},
	}}
}

func run(t *testing.T, s *schema.Schema, csvText string, workers, limit int) Result {
	t.Helper()
	v, err := New(s, workers, limit)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := v.Validate(context.Background(), csv.NewReader(strings.NewReader(csvText)))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return res
}

func TestValidFile(t *testing.T) {
	res := run(t, testSchema(), `id,name,email,age,role,joined,code,opt
1,Ada,ada@example.com,36,admin,2020-01-15,ABC-1234,1.5
2,Grace,grace@example.com,85,user,2019-06-30,XYZ-0001,
`, 4, 100)
	if res.TotalRows != 2 || res.ValidRows != 2 || len(res.Errors) != 0 {
		t.Fatalf("expected clean result, got %+v", res)
	}
}

func TestViolations(t *testing.T) {
	res := run(t, testSchema(), `id,name,email,age,role,joined,code,opt
abc,X,not-an-email,200,superadmin,15/01/2020,abc123,zzz
1,,dup@example.com,30,user,2021-03-03,ABC-1234,2
1,Duplicate,dup2@example.com,30,user,2021-03-03,DEF-5678,3
`, 1, 100)

	rules := map[string]bool{}
	for _, e := range res.Errors {
		rules[e.Rule] = true
	}
	// Row 1: type (id), type (email), max (age), enum (role), type (joined),
	// pattern (code), type (opt), minLen (name "X")
	// Row 2: required (name)
	// Row 3: unique (id)
	for _, want := range []string{"type", "max", "enum", "pattern", "minLen", "required", "unique"} {
		if !rules[want] {
			t.Errorf("expected a %q violation, got %v", want, rules)
		}
	}
	if res.TotalRows != 3 || res.ValidRows != 0 {
		t.Errorf("expected 3 total / 0 valid, got %+v", res)
	}
	// Sorted by (row, column).
	for i := 1; i < len(res.Errors); i++ {
		a, b := res.Errors[i-1], res.Errors[i]
		if a.Row > b.Row || (a.Row == b.Row && a.Column > b.Column) {
			t.Fatalf("errors not sorted: %+v before %+v", a, b)
		}
	}
}

func TestMissingRequiredColumn(t *testing.T) {
	res := run(t, testSchema(), `id,name,age
1,Ada,36
`, 2, 100)
	if len(res.Errors) != 1 || res.Errors[0].Rule != "missing_column" {
		t.Fatalf("expected one missing_column error, got %+v", res.Errors)
	}
}

func TestErrorLimit(t *testing.T) {
	res := run(t, testSchema(), `id,name,email
x,a,b
y,c,d
`, 2, 2)
	if !res.Truncated {
		t.Fatal("expected truncation flag")
	}
	if len(res.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(res.Errors))
	}
}

func TestUniqueDetection(t *testing.T) {
	s := &schema.Schema{Columns: []schema.Column{
		{Name: "id", Type: "int", Required: true, Unique: true},
	}}
	var sb strings.Builder
	sb.WriteString("id\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("1\n") // same value in every row
	}
	res := run(t, s, sb.String(), 8, 0)
	unique := 0
	for _, e := range res.Errors {
		if e.Rule == "unique" {
			unique++
		}
	}
	if unique != 199 {
		t.Fatalf("expected 199 unique violations, got %d", unique)
	}
}

func TestConcurrentMatchesSerial(t *testing.T) {
	// Same input through 1 and 8 workers must agree on row counts and error rows.
	csvText := `id,name,email,age,role,joined,code,opt
1,Ada,ada@example.com,36,admin,2020-01-15,ABC-1234,1.5
nope,Bo,bad,999,vip,bogus,nope,zzz
3,Cy,cy@example.com,25,user,2022-11-01,QRS-2222,
`
	a := run(t, testSchema(), csvText, 1, 100)
	b := run(t, testSchema(), csvText, 8, 100)
	if a.TotalRows != b.TotalRows || a.ValidRows != b.ValidRows || len(a.Errors) != len(b.Errors) {
		t.Fatalf("worker count changed the outcome: %+v vs %+v", a, b)
	}
}
