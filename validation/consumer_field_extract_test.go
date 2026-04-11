package validation

import (
	"testing"
)

// TestIndexHandlersExtractsFieldsFromLocalType verifies that scanHandlerBody
// resolves the type behind a Decode call to a struct defined in the same
// package and populates the Fields map. This is the regression test for the
// gap that the maintainer review called out: "goTypeInfo.Fields is not
// being populated from real handler code".
func TestIndexHandlersExtractsFieldsFromLocalType(t *testing.T) {
	files := map[string][]byte{
		"server/handlers/user.go": []byte(`package handlers

import "encoding/json"
import _ "github.com/meshery/schemas/models/v1beta1/user"

type CreateUserPayload struct {
	Name  string ` + "`json:\"name\"`" + `
	Email string ` + "`json:\"email\"`" + `
}

type httpResp struct{}
func (httpResp) Body() []byte { return nil }

func CreateUser() {
	var payload CreateUserPayload
	_ = json.NewDecoder(httpResp{}).Decode(&payload)
	_ = payload
}
`),
	}
	tree := mapTree{files: files, label: "field-extract-local"}
	endpoints := []consumerEndpoint{
		{Method: "POST", Path: "/api/users", HandlerName: "CreateUser"},
	}
	got := indexHandlers(tree, endpoints, newGoTypeIndex())
	if len(got) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(got))
	}
	ep := got[0]
	if ep.HandlerFile == "" {
		t.Fatalf("handler file not resolved")
	}
	if !ep.ImportsSchemas {
		t.Errorf("expected ImportsSchemas=true (file imports a schemas/models/* path)")
	}
	if ep.RequestType == nil {
		t.Fatalf("RequestType not resolved")
	}
	if ep.RequestType.TypeName != "CreateUserPayload" {
		t.Errorf("RequestType.TypeName: got %q", ep.RequestType.TypeName)
	}
	if len(ep.RequestType.Fields) == 0 {
		t.Fatalf("RequestType.Fields was not populated; this is the field-level audit gap")
	}
	if _, ok := ep.RequestType.Fields["name"]; !ok {
		t.Errorf("expected name field in extracted type, got %+v", ep.RequestType.Fields)
	}
	if _, ok := ep.RequestType.Fields["email"]; !ok {
		t.Errorf("expected email field in extracted type, got %+v", ep.RequestType.Fields)
	}
}

// TestIndexHandlersExtractsFieldsFromSchemaType wires a synthetic schemas
// package into a goTypeIndex and verifies that scanHandlerBody resolves a
// `pkg.TypeName` reference through the import alias map.
func TestIndexHandlersExtractsFieldsFromSchemaType(t *testing.T) {
	idx := newGoTypeIndex()
	addGoSourceToIndex(idx, "github.com/meshery/schemas/models/v1beta1/connection",
		"connection.go",
		[]byte(`package connection
type ConnectionPayload struct {
	Name string ` + "`json:\"name\"`" + `
	Kind string ` + "`json:\"kind\"`" + `
}
`))

	files := map[string][]byte{
		"server/handlers/connection.go": []byte(`package handlers

import "encoding/json"
import "github.com/meshery/schemas/models/v1beta1/connection"

func CreateConnection() {
	var c connection.ConnectionPayload
	_ = json.NewDecoder(nil).Decode(&c)
	_ = c
}
`),
	}
	tree := mapTree{files: files, label: "field-extract-schema"}
	endpoints := []consumerEndpoint{
		{Method: "POST", Path: "/api/connections", HandlerName: "CreateConnection"},
	}
	got := indexHandlers(tree, endpoints, idx)
	if len(got) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(got))
	}
	ep := got[0]
	if ep.RequestType == nil {
		t.Fatalf("RequestType not resolved")
	}
	if !ep.RequestType.IsFromSchema {
		t.Errorf("expected IsFromSchema=true for schemas-package type")
	}
	if len(ep.RequestType.Fields) == 0 {
		t.Fatalf("RequestType.Fields not populated through schema index")
	}
	if _, ok := ep.RequestType.Fields["name"]; !ok {
		t.Errorf("expected name field, got %+v", ep.RequestType.Fields)
	}
}

// TestIndexHandlersExtractsResponseFromEchoJSON covers Echo's c.JSON(code, v)
// pattern: response shape extraction goes through the second arg, not the
// first, and the local var holds a slice of structs.
func TestIndexHandlersExtractsResponseFromEchoJSON(t *testing.T) {
	files := map[string][]byte{
		"server/handlers/list.go": []byte(`package handlers

type Item struct {
	ID   string ` + "`json:\"id\"`" + `
	Name string ` + "`json:\"name\"`" + `
}

type echoCtx struct{}
func (echoCtx) JSON(code int, v interface{}) error { return nil }

func ListItems(c echoCtx) error {
	items := []Item{{ID: "1", Name: "n"}}
	return c.JSON(200, items)
}
`),
	}
	tree := mapTree{files: files, label: "echo-json-response"}
	endpoints := []consumerEndpoint{
		{Method: "GET", Path: "/api/items", HandlerName: "ListItems"},
	}
	got := indexHandlers(tree, endpoints, newGoTypeIndex())
	if len(got) != 1 {
		t.Fatalf("expected 1 endpoint")
	}
	ep := got[0]
	if ep.ResponseType == nil {
		t.Fatalf("ResponseType not resolved")
	}
	if len(ep.ResponseType.Fields) == 0 {
		t.Fatalf("ResponseType.Fields not populated for slice payload")
	}
	if _, ok := ep.ResponseType.Fields["id"]; !ok {
		t.Errorf("expected id field, got %+v", ep.ResponseType.Fields)
	}
}

// TestIndexHandlersAnonymousHandlerLeavesFieldsEmpty ensures we don't
// invent type information when the handler is anonymous and the local var
// resolution can't find anything to anchor to.
func TestIndexHandlersBareCallNoLocalVar(t *testing.T) {
	files := map[string][]byte{
		"server/handlers/anon.go": []byte(`package handlers

import "encoding/json"

func DecodeUnknown() {
	_ = json.NewDecoder(nil).Decode(nil)
}
`),
	}
	tree := mapTree{files: files, label: "anon-decode"}
	endpoints := []consumerEndpoint{
		{Method: "POST", Path: "/api/x", HandlerName: "DecodeUnknown"},
	}
	got := indexHandlers(tree, endpoints, newGoTypeIndex())
	if got[0].RequestType != nil && len(got[0].RequestType.Fields) > 0 {
		t.Errorf("expected no field info when arg is nil, got %+v", got[0].RequestType)
	}
}
