package validation

import (
	"sort"
	"testing"
)

func TestNormalizeMatchKey(t *testing.T) {
	cases := []struct {
		method, path string
		want         matchKey
	}{
		{"get", "/api/users/", matchKey{Method: "GET", Path: "/api/users"}},
		{"POST", "api/users", matchKey{Method: "POST", Path: "/api/users"}},
		{"put", "/api/users/{orgID}", matchKey{Method: "PUT", Path: "/api/users/{orgid}"}},
		{"delete", "/api/users/{orgId}", matchKey{Method: "DELETE", Path: "/api/users/{orgid}"}},
	}
	for _, tc := range cases {
		got := normalizeMatchKey(tc.method, tc.path)
		if got != tc.want {
			t.Errorf("normalizeMatchKey(%q,%q) = %+v, want %+v",
				tc.method, tc.path, got, tc.want)
		}
	}
}

func TestMatchEndpointsFullOuter(t *testing.T) {
	schema := &schemaIndex{
		Endpoints: []schemaEndpoint{
			{Method: "GET", Path: "/api/users", HasSuccessRef: true},
			{Method: "POST", Path: "/api/users"},
			{Method: "GET", Path: "/api/orphans"},
		},
	}
	meshery := []consumerEndpoint{
		{Method: "GET", Path: "/api/users", HandlerName: "GetUsers", HandlerFile: "x.go", ImportsSchemas: true},
		{Method: "GET", Path: "/api/extra", HandlerName: "Extra", HandlerFile: "x.go"},
	}
	cloud := []consumerEndpoint{
		{Method: "POST", Path: "/api/users", HandlerName: "CreateUser", HandlerFile: "y.go"},
	}

	res := matchEndpoints(schema, meshery, cloud)

	if len(res.Matched) != 2 {
		t.Fatalf("matched: want 2, got %d (%+v)", len(res.Matched), res.Matched)
	}
	if len(res.SchemaOnly) != 1 || res.SchemaOnly[0].Path != "/api/orphans" {
		t.Fatalf("schemaOnly: %+v", res.SchemaOnly)
	}
	if len(res.ConsumerOnly) != 1 || res.ConsumerOnly[0].Path != "/api/extra" {
		t.Fatalf("consumerOnly: %+v", res.ConsumerOnly)
	}
}

func TestMatchEndpointsXInternal(t *testing.T) {
	schema := &schemaIndex{
		Endpoints: []schemaEndpoint{
			{Method: "GET", Path: "/api/cloud-only", XInternal: []string{"cloud"}},
			{Method: "GET", Path: "/api/meshery-only", XInternal: []string{"meshery"}},
		},
	}
	meshery := []consumerEndpoint{
		{Method: "GET", Path: "/api/cloud-only", HandlerName: "ShouldNotMatch", HandlerFile: "x.go"},
		{Method: "GET", Path: "/api/meshery-only", HandlerName: "ShouldMatch", HandlerFile: "x.go"},
	}
	cloud := []consumerEndpoint{
		{Method: "GET", Path: "/api/cloud-only", HandlerName: "Match", HandlerFile: "y.go"},
		{Method: "GET", Path: "/api/meshery-only", HandlerName: "ShouldNotMatch", HandlerFile: "y.go"},
	}

	res := matchEndpoints(schema, meshery, cloud)

	// Both schema endpoints exist; meshery handler for cloud-only is unmatched
	// (consumer-only) and cloud handler for meshery-only is unmatched.
	if len(res.Matched) != 2 {
		t.Fatalf("matched: %d", len(res.Matched))
	}
	for _, m := range res.Matched {
		switch m.Schema.Path {
		case "/api/cloud-only":
			if len(m.Consumers) != 1 || m.Consumers[0].Repo != "" {
				// Repo isn't set in our fixtures; just verify HandlerName.
			}
			if m.Consumers[0].HandlerName != "Match" {
				t.Errorf("cloud-only matched %q", m.Consumers[0].HandlerName)
			}
		case "/api/meshery-only":
			if m.Consumers[0].HandlerName != "ShouldMatch" {
				t.Errorf("meshery-only matched %q", m.Consumers[0].HandlerName)
			}
		}
	}

	// Two consumers should be left over.
	consumerOnlyHandlers := make([]string, 0, len(res.ConsumerOnly))
	for _, c := range res.ConsumerOnly {
		consumerOnlyHandlers = append(consumerOnlyHandlers, c.HandlerName)
	}
	sort.Strings(consumerOnlyHandlers)
	if len(consumerOnlyHandlers) != 2 ||
		consumerOnlyHandlers[0] != "ShouldNotMatch" ||
		consumerOnlyHandlers[1] != "ShouldNotMatch" {
		t.Errorf("consumerOnly: %v", consumerOnlyHandlers)
	}
}

func TestClassifySchemaBacked(t *testing.T) {
	if got := classifySchemaBacked(false, schemaEndpoint{}); got != "FALSE" {
		t.Errorf("absent: got %q", got)
	}
	if got := classifySchemaBacked(true, schemaEndpoint{HasSuccessRef: true}); got != "TRUE" {
		t.Errorf("with ref: got %q", got)
	}
	if got := classifySchemaBacked(true, schemaEndpoint{HasSuccessRef: false}); got != "Partial" {
		t.Errorf("no ref: got %q", got)
	}
}

func TestClassifySchemaDriven(t *testing.T) {
	// No consumer repo provided.
	if got := classifySchemaDriven(false, nil, nil, nil); got != "N/A" {
		t.Errorf("no repo: got %q", got)
	}
	// Repo provided, no consumer endpoint.
	if got := classifySchemaDriven(true, nil, nil, nil); got != "Not Audited" {
		t.Errorf("missing handler: got %q", got)
	}
	// Anonymous handler.
	c := &consumerEndpoint{HandlerName: "(anonymous)"}
	if got := classifySchemaDriven(true, c, nil, nil); got != "Not Audited" {
		t.Errorf("anonymous: got %q", got)
	}
	// Handler found, no schema import.
	c = &consumerEndpoint{HandlerName: "X", HandlerFile: "f.go"}
	if got := classifySchemaDriven(true, c, nil, nil); got != "FALSE" {
		t.Errorf("no import: got %q", got)
	}
	// Handler found, imports schemas, no shape verifiable —
	// Partial, not TRUE.
	c = &consumerEndpoint{HandlerName: "X", HandlerFile: "f.go", ImportsSchemas: true}
	if got := classifySchemaDriven(true, c, nil, nil); got != "Partial" {
		t.Errorf("imports schemas no shape: want Partial, got %q", got)
	}
	// Handler imports schemas, schema shape exists but consumer type
	// info is missing — still Partial (we can't actually verify).
	shape := &schemaShape{
		Fields: map[string]fieldShape{
			"name": {Name: "name", Type: "string"},
		},
	}
	if got := classifySchemaDriven(true, c, shape, nil); got != "Partial" {
		t.Errorf("imports schemas, no consumer type: want Partial, got %q", got)
	}
	// Handler imports schemas and the request type was actually
	// inspected and matches → TRUE.
	c = &consumerEndpoint{
		HandlerName:    "X",
		HandlerFile:    "f.go",
		ImportsSchemas: true,
		RequestType: &goTypeInfo{
			TypeName: "T",
			Fields:   map[string]string{"name": "string"},
		},
	}
	if got := classifySchemaDriven(true, c, shape, nil); got != "TRUE" {
		t.Errorf("matching shape: got %q", got)
	}
	// Mismatched request shape ⇒ Partial.
	c.RequestType.Fields = map[string]string{"name": "int"}
	if got := classifySchemaDriven(true, c, shape, nil); got != "Partial" {
		t.Errorf("mismatched shape: got %q", got)
	}
	// Response shape verified successfully even though request side is
	// nil ⇒ TRUE (e.g. GET endpoint where the handler decoded the
	// response into a schema-typed struct).
	c = &consumerEndpoint{
		HandlerName:    "GetX",
		HandlerFile:    "f.go",
		ImportsSchemas: true,
		ResponseType: &goTypeInfo{
			TypeName: "T",
			Fields:   map[string]string{"name": "string"},
		},
	}
	if got := classifySchemaDriven(true, c, nil, shape); got != "TRUE" {
		t.Errorf("response-only verified: got %q", got)
	}
}

func TestDiffFieldsFiltersDBFields(t *testing.T) {
	shape := &schemaShape{
		Fields: map[string]fieldShape{
			"id":         {Name: "id", Type: "string"},
			"created_at": {Name: "created_at", Type: "string"},
			"name":       {Name: "name", Type: "string"},
		},
	}
	info := &goTypeInfo{
		Fields: map[string]string{"name": "string"},
	}
	diffs := diffFields(shape, info, true)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs (id/created_at filtered), got %+v", diffs)
	}
}

func TestDiffFieldsConsumerExtra(t *testing.T) {
	shape := &schemaShape{
		Fields: map[string]fieldShape{
			"name": {Name: "name", Type: "string"},
		},
	}
	info := &goTypeInfo{
		Fields: map[string]string{
			"name":  "string",
			"extra": "int",
		},
	}
	diffs := diffFields(shape, info, true)
	if len(diffs) != 1 || diffs[0].FieldName != "extra" || diffs[0].InSchema {
		t.Errorf("expected one consumer-only diff, got %+v", diffs)
	}
}

func TestMatchAnyVerbConsumer(t *testing.T) {
	schema := &schemaIndex{Endpoints: []schemaEndpoint{
		{Method: "GET", Path: "/api/p"},
	}}
	meshery := []consumerEndpoint{
		{Method: "ANY", Path: "/api/p", HandlerName: "P", HandlerFile: "f.go"},
	}
	res := matchEndpoints(schema, meshery, nil)
	if len(res.Matched) != 1 {
		t.Fatalf("expected ANY consumer to match GET schema endpoint")
	}
}
