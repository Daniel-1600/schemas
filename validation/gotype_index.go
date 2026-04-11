package validation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// goTypeIndex stores struct field sets discovered from a Go source tree,
// keyed by import path and type name. The audit uses this to populate
// goTypeInfo.Fields once a handler scan has identified the type backing a
// Decode/Encode/Bind/JSON call.
//
// The index is intentionally read-mostly: it is built once per audit run
// from the local meshery-schemas models tree, then merged with per-package
// type maps gathered while walking each consumer repo.
type goTypeIndex struct {
	// types[importPath][TypeName] -> json field name -> Go type string
	types map[string]map[string]map[string]string
}

func newGoTypeIndex() *goTypeIndex {
	return &goTypeIndex{types: make(map[string]map[string]map[string]string)}
}

// addType registers a type's field set under the given import path.
// If a previous entry exists, it is replaced — schemas type definitions are
// expected to be unique within an import path.
func (idx *goTypeIndex) addType(importPath, typeName string, fields map[string]string) {
	if idx == nil || len(fields) == 0 {
		return
	}
	pkg, ok := idx.types[importPath]
	if !ok {
		pkg = make(map[string]map[string]string)
		idx.types[importPath] = pkg
	}
	pkg[typeName] = fields
}

// lookup returns the field set for a (importPath, typeName) tuple, or nil
// if no such type was indexed.
func (idx *goTypeIndex) lookup(importPath, typeName string) map[string]string {
	if idx == nil {
		return nil
	}
	if pkg, ok := idx.types[importPath]; ok {
		if f, ok := pkg[typeName]; ok {
			return f
		}
	}
	return nil
}

// loadSchemasGoTypes walks the meshery-schemas models tree under rootDir and
// builds an index of every struct discovered. Each type is keyed under the
// import path inferred from its directory layout (i.e.
// github.com/meshery/schemas/models/<rel>).
//
// Errors during the walk are silently ignored: an unavailable models tree
// produces an empty index, which then degrades the audit to the same state
// it would have been in without the index — every Schema-Driven row stays
// "Partial" rather than failing the run.
func loadSchemasGoTypes(rootDir string) *goTypeIndex {
	idx := newGoTypeIndex()
	if rootDir == "" {
		return idx
	}
	modelsDir := filepath.Join(rootDir, "models")
	info, err := os.Stat(modelsDir)
	if err != nil || !info.IsDir() {
		return idx
	}
	_ = filepath.WalkDir(modelsDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(modelsDir, p)
		if err != nil {
			return nil
		}
		importPath := "github.com/meshery/schemas/models/" + filepath.ToSlash(filepath.Dir(rel))
		// Strip the trailing "/." that filepath.Dir leaves for top-level files.
		importPath = strings.TrimSuffix(importPath, "/.")
		addGoFileToIndex(idx, importPath, p)
		return nil
	})
	return idx
}

// addGoFileToIndex parses a single .go file and registers every top-level
// struct type definition under the supplied import path.
func addGoFileToIndex(idx *goTypeIndex, importPath, filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	addGoSourceToIndex(idx, importPath, filePath, data)
}

// addGoSourceToIndex is the in-memory equivalent of addGoFileToIndex; it
// exists so the same logic can be reused for files that are loaded out of a
// sourceTree rather than the OS filesystem.
func addGoSourceToIndex(idx *goTypeIndex, importPath, filePath string, data []byte) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, data, parser.ParseComments)
	if err != nil {
		return
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			fields := extractStructFields(st)
			if len(fields) > 0 {
				idx.addType(importPath, ts.Name.Name, fields)
			}
		}
	}
}

// extractStructFields walks a struct type definition and returns a JSON-tag
// → Go-type-string map. Fields tagged `json:"-"` are skipped; embedded
// fields are skipped (the audit only verifies the top-level shape, which
// matches what diffFields compares).
func extractStructFields(st *ast.StructType) map[string]string {
	if st == nil || st.Fields == nil {
		return nil
	}
	out := make(map[string]string)
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		jsonName := ""
		skip := false
		if field.Tag != nil {
			raw, err := strconv.Unquote(field.Tag.Value)
			if err == nil {
				tag := reflect.StructTag(raw)
				if jt := tag.Get("json"); jt != "" {
					parts := strings.Split(jt, ",")
					if parts[0] == "-" {
						skip = true
					} else if parts[0] != "" {
						jsonName = parts[0]
					}
				}
			}
		}
		if skip {
			continue
		}
		typeStr := exprToString(field.Type)
		for _, name := range field.Names {
			n := jsonName
			if n == "" {
				n = name.Name
			}
			out[n] = typeStr
		}
	}
	return out
}

// exprToString returns a textual rendering of a Go type expression suitable
// for the relaxed comparisons performed by typesCompatible. It deliberately
// flattens pointer/array/map wrappers to a single Go-style string and does
// not preserve qualifier whitespace.
func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.ArrayType:
		return "[]" + exprToString(e.Elt)
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok && e.Sel != nil {
			return id.Name + "." + e.Sel.Name
		}
		if e.Sel != nil {
			return e.Sel.Name
		}
	case *ast.MapType:
		return "map[" + exprToString(e.Key) + "]" + exprToString(e.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.StructType:
		return "struct{}"
	}
	return ""
}
