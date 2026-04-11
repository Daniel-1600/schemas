package validation

import (
	"testing"
)

func TestExtractStructFieldsHonorsJSONTags(t *testing.T) {
	src := []byte(`package x

type Sample struct {
	Name      string  ` + "`json:\"name\"`" + `
	Renamed   int     ` + "`json:\"display_count,omitempty\"`" + `
	Skipped   string  ` + "`json:\"-\"`" + `
	NoTag     bool
	Multi, Pi int
}
`)
	idx := newGoTypeIndex()
	addGoSourceToIndex(idx, "example.com/x", "x.go", src)
	got := idx.lookup("example.com/x", "Sample")
	if got == nil {
		t.Fatalf("type not indexed")
	}
	if _, ok := got["name"]; !ok {
		t.Errorf("missing json:name field: %+v", got)
	}
	if _, ok := got["display_count"]; !ok {
		t.Errorf("missing json:display_count field: %+v", got)
	}
	if _, ok := got["-"]; ok {
		t.Errorf("json:- field should be skipped")
	}
	if _, ok := got["NoTag"]; !ok {
		t.Errorf("untagged field should fall back to its Go name")
	}
	if _, ok := got["Multi"]; !ok || got["Multi"] != "int" {
		t.Errorf("multi-decl field Multi missing or wrong type: %+v", got)
	}
	if _, ok := got["Pi"]; !ok || got["Pi"] != "int" {
		t.Errorf("multi-decl field Pi missing or wrong type: %+v", got)
	}
	if got["display_count"] != "int" {
		t.Errorf("display_count: want int, got %q", got["display_count"])
	}
}

func TestExtractStructFieldsArrayAndPointer(t *testing.T) {
	src := []byte(`package x

type Sample struct {
	Items []string ` + "`json:\"items\"`" + `
	Owner *string  ` + "`json:\"owner\"`" + `
	Bag   map[string]int ` + "`json:\"bag\"`" + `
}
`)
	idx := newGoTypeIndex()
	addGoSourceToIndex(idx, "example.com/x", "x.go", src)
	got := idx.lookup("example.com/x", "Sample")
	if got["items"] != "[]string" {
		t.Errorf("items: got %q", got["items"])
	}
	if got["owner"] != "*string" {
		t.Errorf("owner: got %q", got["owner"])
	}
	if got["bag"] != "map[string]int" {
		t.Errorf("bag: got %q", got["bag"])
	}
}

func TestExtractStructFieldsSelectorTypes(t *testing.T) {
	src := []byte(`package x

type Sample struct {
	ID core.Uuid ` + "`json:\"id\"`" + `
}
`)
	idx := newGoTypeIndex()
	addGoSourceToIndex(idx, "example.com/x", "x.go", src)
	got := idx.lookup("example.com/x", "Sample")
	if got["id"] != "core.Uuid" {
		t.Errorf("id: got %q", got["id"])
	}
}

func TestGoTypeIndexLookupMissReturnsNil(t *testing.T) {
	idx := newGoTypeIndex()
	if idx.lookup("nope", "nope") != nil {
		t.Errorf("expected nil for missing entry")
	}
	var nilIdx *goTypeIndex
	if nilIdx.lookup("a", "b") != nil {
		t.Errorf("nil receiver should return nil, not panic")
	}
}
