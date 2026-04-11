package validation

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// findRepoRootForTest walks up from the test working directory until it finds
// a go.mod file. Mirrors cmd/validate-schemas/main.go:findRepoRoot.
func findRepoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

func TestEndpointIndexBuilds(t *testing.T) {
	root := findRepoRootForTest(t)
	idx, err := buildEndpointIndex(root)
	if err != nil {
		t.Fatalf("buildEndpointIndex: %v", err)
	}
	if idx == nil || len(idx.Endpoints) == 0 {
		t.Fatalf("expected at least one schema endpoint")
	}

	// Sanity: every endpoint must carry method, path, version, construct, source.
	for _, ep := range idx.Endpoints {
		if ep.Method == "" || ep.Path == "" {
			t.Errorf("endpoint missing method/path: %+v", ep)
		}
		if ep.Version == "" || ep.Construct == "" || ep.SourceFile == "" {
			t.Errorf("endpoint missing metadata: %+v", ep)
		}
		if !shouldValidateVersion(ep.Version) {
			t.Errorf("endpoint emitted from unvalidated version %q: %+v", ep.Version, ep)
		}
	}
}

func TestEndpointIndexSorted(t *testing.T) {
	root := findRepoRootForTest(t)
	idx, err := buildEndpointIndex(root)
	if err != nil {
		t.Fatalf("buildEndpointIndex: %v", err)
	}

	for i := 1; i < len(idx.Endpoints); i++ {
		a, b := idx.Endpoints[i-1], idx.Endpoints[i]
		if a.Path > b.Path || (a.Path == b.Path && a.Method > b.Method) {
			t.Fatalf("index not sorted at %d: (%s %s) > (%s %s)",
				i, a.Path, a.Method, b.Path, b.Method)
		}
	}
}

func TestEndpointIndexKnownEndpoints(t *testing.T) {
	root := findRepoRootForTest(t)
	idx, err := buildEndpointIndex(root)
	if err != nil {
		t.Fatalf("buildEndpointIndex: %v", err)
	}

	// /api/auth/keys is known to be defined in the key construct
	// with x-internal: ["cloud"] on at least one operation. Use this
	// as a representative endpoint without hardcoding too much.
	var sawKeyAuth bool
	var sawCloudInternal bool
	for _, ep := range idx.Endpoints {
		if ep.Path == "/api/auth/keys" && ep.Construct == "key" {
			sawKeyAuth = true
			for _, target := range ep.XInternal {
				if target == "cloud" {
					sawCloudInternal = true
				}
			}
		}
	}
	if !sawKeyAuth {
		t.Fatalf("expected to find /api/auth/keys in key construct")
	}
	if !sawCloudInternal {
		t.Fatalf("expected at least one /api/auth/keys op to be x-internal=[cloud]")
	}
}

func TestEndpointIndexSkipsLegacyVersions(t *testing.T) {
	root := findRepoRootForTest(t)
	idx, err := buildEndpointIndex(root)
	if err != nil {
		t.Fatalf("buildEndpointIndex: %v", err)
	}
	for _, ep := range idx.Endpoints {
		if strings.HasPrefix(ep.Version, "v1alpha") {
			t.Fatalf("legacy version leaked into index: %+v", ep)
		}
	}
}

func TestParseXInternal(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"nil extensions", nil, nil},
		{"missing key", map[string]any{"x-other": "x"}, nil},
		{"slice of any", map[string]any{"x-internal": []any{"cloud", "meshery"}}, []string{"cloud", "meshery"}},
		{"slice of string", map[string]any{"x-internal": []string{"cloud"}}, []string{"cloud"}},
		{"single string", map[string]any{"x-internal": "meshery"}, []string{"meshery"}},
		{"empty slice", map[string]any{"x-internal": []any{}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseXInternal(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("got %v want %v", got, want)
				}
			}
		})
	}
}

func TestBuildSchemaShape_FromKeyAPI(t *testing.T) {
	root := findRepoRootForTest(t)
	apiPath := filepath.Join(root, "schemas", "constructs", "v1beta1", "key", "api.yml")
	doc, err := loadAPISpec(apiPath)
	if err != nil {
		t.Fatalf("loadAPISpec: %v", err)
	}
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		t.Fatalf("expected loaded key api.yml with components")
	}
	keyRef, ok := doc.Components.Schemas["Key"]
	if !ok {
		t.Skip("Key schema component not present; skipping shape extraction")
	}
	shape := buildSchemaShape(keyRef)
	if shape == nil || len(shape.Fields) == 0 {
		t.Fatalf("expected Key schema to produce non-empty shape, got %+v", shape)
	}
}
