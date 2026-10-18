package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeTemp(t, `{"columns":[
		{"name":"id","type":"int","required":true,"unique":true},
		{"name":"email","type":"email","required":true},
		{"name":"role","type":"string","enum":["admin","user"],"required":true}
	]}`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(s.Columns))
	}
}

func TestLoadBadJSON(t *testing.T) {
	if _, err := Load(writeTemp(t, `{"columns":[`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestCompileRejects(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"no columns", `{"columns":[]}`},
		{"unknown type", `{"columns":[{"name":"a","type":"money"}]}`},
		{"duplicate name", `{"columns":[{"name":"a","type":"int"},{"name":"a","type":"int"}]}`},
		{"bad regex", `{"columns":[{"name":"a","type":"string","pattern":"([a"}]}`},
		{"min over max", `{"columns":[{"name":"a","type":"int","min":5,"max":1}]}`},
		{"minlen over maxlen", `{"columns":[{"name":"a","type":"string","minLen":5,"maxLen":1}]}`},
		{"bounds on string", `{"columns":[{"name":"a","type":"string","min":1}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, tc.json)); err == nil {
				t.Fatalf("expected compile error for %s", tc.name)
			}
		})
	}
}

func TestIndex(t *testing.T) {
	s := &Schema{Columns: []Column{{Name: "a"}, {Name: "b"}}}
	c, i, ok := s.Index("b")
	if !ok || i != 1 || c.Name != "b" {
		t.Fatalf("bad index result: %+v %d %v", c, i, ok)
	}
	if _, _, ok := s.Index("z"); ok {
		t.Fatal("expected miss for unknown column")
	}
}
