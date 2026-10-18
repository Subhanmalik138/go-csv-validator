# csvvalidate

A schema-driven CSV contract validator for API ingestion pipelines, written in Go.

Bad rows are the most expensive rows: they slip into an import, fail halfway,
and leave the pipeline in a half-written state. `csvvalidate` checks a CSV
file against a JSON schema contract *before* anything touches the API —
types, bounds, formats, enumerations, regex patterns, and uniqueness — and
reports every violation with its exact row and column.

```bash
go build -o csvvalidate ./cmd/csvvalidate

# check a file against a contract
csvvalidate -schema testdata/schema.json testdata/users_bad.csv

# machine-readable report for CI
csvvalidate -schema testdata/schema.json -format json -out report.json -in data.csv

# also emit a file containing only the rows that pass
csvvalidate -schema testdata/schema.json -clean valid_rows.csv data.csv
```

Exit code `0` = contract satisfied, `1` = violations found, `2` = usage or I/O error.

Note: flags must come before the positional CSV argument (Go stdlib flag parsing).

## Schema format

A schema is a JSON document listing the expected columns. Columns are matched
by **name**, so column order in the CSV does not matter; a missing *required*
column fails the whole file before any row is read.

```json
{
  "columns": [
    {"name": "id",    "type": "int",    "required": true, "unique": true, "min": 1},
    {"name": "name",  "type": "string", "required": true, "minLen": 2, "maxLen": 40},
    {"name": "email", "type": "email",  "required": true},
    {"name": "role",  "type": "string", "enum": ["admin", "user", "guest"]},
    {"name": "code",  "type": "string", "pattern": "^[A-Z]{3}-\\d{4}$"}
  ]
}
```

| Field     | Applies to          | Meaning                                              |
|-----------|---------------------|------------------------------------------------------|
| `type`    | all                 | `string`, `int`, `float`, `bool`, `date`, `email`, `url` |
| `required`| all                 | empty cell is a violation                            |
| `min/max` | `int`, `float`      | numeric bounds                                       |
| `minLen/maxLen` | all           | length bounds in runes                               |
| `pattern` | all                 | Go regexp the whole value must match                 |
| `enum`    | all                 | value must be one of the listed strings              |
| `unique`  | all                 | value must not repeat anywhere in the file           |
| `format`  | `date`              | Go time layout, default `2006-01-02`                 |

Empty cells in *optional* columns skip all other checks for that cell.

## Design decisions

1. **Contract-first, schema-as-JSON.** The rules live in a data file, not in
   code, so the same binary validates any feed — the contract travels with
   the pipeline config, not the release.
2. **Concurrent row validation with a worker pool.** Rows are independent, so
   validation scales with CPU count; the `unique` rule is coordinated through
   a mutex-guarded registry so duplicates are still detected exactly once.
   Output is re-sorted by (row, column), so reports are deterministic
   regardless of worker count (covered by a test that compares 1 vs 8 workers).
3. **Error budget instead of fail-fast.** A `-limit` caps the report so a
   million-row file with a broken column does not produce a million-line
   report; the file still gets fully scanned and row counts stay exact.
4. **Header check before row work.** Columns are matched by name, and a
   missing required column aborts immediately — there is no point validating
   a million rows against the wrong feed.

## Running the tests

```bash
go test ./...
go vet ./...
```

## Tech stack

- Go 1.24, standard library only (`encoding/csv`, `net/mail`, `regexp`, `sync`)
- No external dependencies
