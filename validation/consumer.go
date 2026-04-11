package validation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
)

// consumerEndpoint represents one route registered in a consumer codebase.
type consumerEndpoint struct {
	Repo           string      // "meshery" or "meshery-cloud"
	Method         string      // "GET", "POST", etc., or "ANY"
	Path           string      // normalized: starts with /, params as {name}
	HandlerName    string      // "GetConnections", "(anonymous)", ""
	HandlerFile    string      // "server/handlers/user_handler.go" (repo-relative)
	RouterFile     string      // where registration lives
	RouterLine     int         // line number in the router file
	ImportsSchemas bool        // handler file imports github.com/meshery/schemas/models/*
	RequestType    *goTypeInfo // nil if not inferable
	ResponseType   *goTypeInfo // nil if not inferable
	Notes          []string    // parser-side notes (e.g. "anonymous handler")
}

// goTypeInfo describes a Go type used by a consumer handler.
type goTypeInfo struct {
	Package      string            // full import path
	TypeName     string            // struct name, e.g. "ConnectionPayload"
	Fields       map[string]string // JSON tag -> Go type string
	IsFromSchema bool              // package starts with "github.com/meshery/schemas/models/"
}

// handlerInfo summarizes what we learn from a single handler file walk.
type handlerInfo struct {
	File           string
	ImportsSchemas bool
	RequestType    *goTypeInfo
	ResponseType   *goTypeInfo
}

// indexHandlers walks the handler directories under a consumer source tree,
// builds a map keyed by handler function name, and joins it back into the
// supplied list of consumerEndpoints. Endpoints whose HandlerName cannot be
// resolved are returned unchanged with HandlerFile blank.
//
// The schemaIdx, when non-nil, is used to populate goTypeInfo.Fields for
// any handler-decoded type that maps to a schemas-generated struct. Local
// types defined in the same package as the handler file are added to the
// index on the fly so per-handler structs can also be verified.
func indexHandlers(tree sourceTree, endpoints []consumerEndpoint, schemaIdx *goTypeIndex) []consumerEndpoint {
	if tree == nil {
		return endpoints
	}

	type fileCtx struct {
		path    string
		dir     string
		file    *ast.File
		imports map[string]string
	}

	var ctxs []fileCtx

	walkDirs := []string{
		"server/handlers",
		"server/router",
	}
	for _, dir := range walkDirs {
		_ = tree.Walk(dir, func(p string) error {
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			data, err := tree.ReadFile(p)
			if err != nil {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, p, data, parser.ParseComments)
			if err != nil {
				return nil
			}
			ctxs = append(ctxs, fileCtx{
				path:    p,
				dir:     path.Dir(p),
				file:    file,
				imports: collectImportMap(file),
			})
			return nil
		})
	}

	// Per-package local type sets, indexed by directory of the handler
	// file. Handler-local types live in the same package as the handler,
	// so a per-dir map is the smallest unit that gives us correct lookups.
	localPkgTypes := make(map[string]map[string]map[string]string)
	for _, c := range ctxs {
		if localPkgTypes[c.dir] == nil {
			localPkgTypes[c.dir] = make(map[string]map[string]string)
		}
		for _, decl := range c.file.Decls {
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
					localPkgTypes[c.dir][ts.Name.Name] = fields
				}
			}
		}
	}

	handlers := make(map[string]handlerInfo)
	for _, c := range ctxs {
		importsSchemas := fileImportsSchemas(c.file)
		for _, decl := range c.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			name := fn.Name.Name
			if _, exists := handlers[name]; exists {
				// Keep the first occurrence — handlers in different
				// receivers with the same name are rare and the join
				// is best-effort.
				continue
			}
			req, resp := scanHandlerBody(fn, c.imports, localPkgTypes[c.dir], schemaIdx)
			handlers[name] = handlerInfo{
				File:           c.path,
				ImportsSchemas: importsSchemas,
				RequestType:    req,
				ResponseType:   resp,
			}
		}
	}

	for i := range endpoints {
		ep := &endpoints[i]
		if ep.HandlerName == "" {
			continue
		}
		info, ok := handlers[ep.HandlerName]
		if !ok {
			continue
		}
		ep.HandlerFile = info.File
		ep.ImportsSchemas = info.ImportsSchemas
		if ep.RequestType == nil {
			ep.RequestType = info.RequestType
		}
		if ep.ResponseType == nil {
			ep.ResponseType = info.ResponseType
		}
	}

	return endpoints
}

// collectImportMap returns a map of package alias → import path for an AST
// file. Files use either an explicit alias (`import foo "x/y/z"`) or fall
// back to the trailing path segment as the alias.
func collectImportMap(file *ast.File) map[string]string {
	out := make(map[string]string)
	if file == nil {
		return out
	}
	for _, imp := range file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		raw := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias == "" {
			alias = path.Base(raw)
		}
		if alias == "_" || alias == "." {
			continue
		}
		out[alias] = raw
	}
	return out
}

// fileImportsSchemas returns true if any import of the given file references
// a generated meshery/schemas type package.
func fileImportsSchemas(file *ast.File) bool {
	if file == nil {
		return false
	}
	for _, imp := range file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		path := strings.Trim(imp.Path.Value, `"`)
		if strings.HasPrefix(path, "github.com/meshery/schemas/models/") {
			return true
		}
	}
	return false
}

// extractHandlerName recursively unwraps a handler expression to find the
// innermost named handler function. Implements the algorithm described in
// the architecture doc, plus extra unwraps for:
//   - http.StripPrefix(prefix, inner)
//   - echo.WrapHandler(http.HandlerFunc(receiver.X))
//   - func literal handlers (scan body for h.X / s.h.X calls)
func extractHandlerName(expr ast.Expr) string {
	switch e := expr.(type) {
	case nil:
		return ""

	case *ast.Ident:
		return e.Name

	case *ast.SelectorExpr:
		// receiver.Method — the handler is the trailing selector name.
		if e.Sel != nil {
			return e.Sel.Name
		}

	case *ast.CallExpr:
		// Special-case http.StripPrefix(prefix, inner) → unwrap the
		// second argument.
		if isCalledFunc(e, "StripPrefix") && len(e.Args) >= 2 {
			if name := extractHandlerName(e.Args[1]); name != "" {
				return name
			}
		}
		// http.HandlerFunc(receiver.X) → unwrap.
		if isCalledFunc(e, "HandlerFunc") && len(e.Args) >= 1 {
			if name := extractHandlerName(e.Args[0]); name != "" {
				return name
			}
		}
		// echo.WrapHandler(...) → unwrap.
		if isCalledFunc(e, "WrapHandler") && len(e.Args) >= 1 {
			if name := extractHandlerName(e.Args[0]); name != "" {
				return name
			}
		}
		// Generic recursion: scan args, deepest non-empty result wins.
		for _, arg := range e.Args {
			if name := extractHandlerName(arg); name != "" && !isMiddlewareName(name) {
				return name
			}
		}
		// Fall back to args including middleware-like names — gives the
		// caller something rather than empty string when the chain is
		// entirely middlewares.
		for _, arg := range e.Args {
			if name := extractHandlerName(arg); name != "" {
				return name
			}
		}
		// Finally fall back to the function being invoked.
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel != nil {
			return sel.Sel.Name
		}
		if id, ok := e.Fun.(*ast.Ident); ok {
			return id.Name
		}

	case *ast.FuncLit:
		// Anonymous handler — scan its body for h.X(...) / s.h.X(...) calls
		// where the receiver looks like a handler container.
		if name := scanFuncLitForHandler(e); name != "" {
			return name
		}
		return "(anonymous)"
	}
	return ""
}

// isCalledFunc reports whether a CallExpr is calling a function whose
// trailing selector matches name (e.g. http.StripPrefix → "StripPrefix").
func isCalledFunc(call *ast.CallExpr, name string) bool {
	if call == nil {
		return false
	}
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel != nil && fn.Sel.Name == name
	case *ast.Ident:
		return fn.Name == name
	}
	return false
}

// middlewareNameFragments are common substrings of middleware function names
// that should be skipped while looking for the real handler.
var middlewareNameFragments = []string{
	"Middleware",
	"AuthGuard",
	"WithAuth",
	"WithSession",
	"Authorization",
}

func isMiddlewareName(name string) bool {
	for _, frag := range middlewareNameFragments {
		if strings.Contains(name, frag) {
			return true
		}
	}
	return false
}

// scanFuncLitForHandler walks a function literal body and returns the first
// CallExpr whose receiver looks like a handler container (h, s.h, hc, etc.).
func scanFuncLitForHandler(fn *ast.FuncLit) string {
	if fn == nil || fn.Body == nil {
		return ""
	}
	var found string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		if !receiverLooksLikeHandlerContainer(sel.X) {
			return true
		}
		// Skip the obvious middleware leak.
		if isMiddlewareName(sel.Sel.Name) {
			return true
		}
		found = sel.Sel.Name
		return false
	})
	return found
}

// receiverLooksLikeHandlerContainer returns true for expressions like h, s.h,
// or hc — common handler container receivers in Meshery.
func receiverLooksLikeHandlerContainer(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		switch e.Name {
		case "h", "hc", "handler":
			return true
		}
	case *ast.SelectorExpr:
		if e.Sel != nil {
			switch e.Sel.Name {
			case "h", "hc", "handler", "academyHandler", "invitationsHandler", "badgesHandler":
				return true
			}
		}
	}
	return false
}

// scanHandlerBody walks a function declaration looking for the request and
// response types it works with, then populates field sets from the supplied
// type contexts.
//
// localTypes is the per-package map of struct types defined in the same
// directory as the handler file (handler-local payload types). schemaIdx is
// the global index of meshery-schemas types loaded from the local models
// tree. imports maps each package alias used in the file to its full import
// path so SelectorExpr type references can be resolved against schemaIdx.
func scanHandlerBody(
	fn *ast.FuncDecl,
	imports map[string]string,
	localTypes map[string]map[string]string,
	schemaIdx *goTypeIndex,
) (*goTypeInfo, *goTypeInfo) {
	if fn == nil || fn.Body == nil {
		return nil, nil
	}

	locals := collectLocalVars(fn)

	resolve := func(arg ast.Expr) *goTypeInfo {
		info := identifyArgType(arg)
		if info == nil {
			info = lookupLocalVar(arg, locals)
		}
		populateFields(info, imports, localTypes, schemaIdx)
		return info
	}

	var req, resp *goTypeInfo

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		switch sel.Sel.Name {
		case "Decode":
			// json.NewDecoder(r.Body).Decode(&v)
			if req == nil && len(call.Args) == 1 {
				req = resolve(call.Args[0])
			}
		case "Bind":
			// echo: c.Bind(&v)
			if req == nil && len(call.Args) == 1 {
				req = resolve(call.Args[0])
			}
		case "Encode":
			// json.NewEncoder(w).Encode(v)
			if resp == nil && len(call.Args) == 1 {
				resp = resolve(call.Args[0])
			}
		case "JSON":
			// echo: c.JSON(code, v)
			if resp == nil && len(call.Args) >= 2 {
				resp = resolve(call.Args[1])
			}
		}
		return true
	})

	return req, resp
}

// collectLocalVars walks a function body and records the type of every
// short variable declaration (`x := T{...}`, `x := &T{}`, `x := new(T)`)
// and `var` declaration whose RHS or type expression is recoverable. It is
// the missing piece that lets scanHandlerBody resolve `Decode(&v)` calls
// where `v` is a bare identifier declared a few lines earlier.
func collectLocalVars(fn *ast.FuncDecl) map[string]*goTypeInfo {
	out := make(map[string]*goTypeInfo)
	if fn == nil || fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.DeclStmt:
			gen, ok := s.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				return true
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				var info *goTypeInfo
				if vs.Type != nil {
					info = typeFromExpr(vs.Type)
				}
				if info == nil && len(vs.Values) > 0 {
					info = identifyArgType(vs.Values[0])
				}
				if info == nil {
					continue
				}
				for _, name := range vs.Names {
					if name == nil || name.Name == "_" {
						continue
					}
					if _, exists := out[name.Name]; exists {
						continue
					}
					out[name.Name] = info
				}
			}
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range s.Lhs {
				if i >= len(s.Rhs) {
					break
				}
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name == "_" {
					continue
				}
				if _, exists := out[id.Name]; exists {
					continue
				}
				if info := identifyArgType(s.Rhs[i]); info != nil {
					out[id.Name] = info
				}
			}
		}
		return true
	})
	return out
}

// lookupLocalVar resolves a Decode/Encode argument to a previously declared
// local variable. It handles `&v` and bare `v` forms.
func lookupLocalVar(expr ast.Expr, locals map[string]*goTypeInfo) *goTypeInfo {
	if locals == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if e.Op.String() == "&" {
			return lookupLocalVar(e.X, locals)
		}
	case *ast.Ident:
		if info, ok := locals[e.Name]; ok && info != nil {
			// Return a shallow copy so populateFields cannot mutate
			// the version stored in the locals map.
			cp := *info
			return &cp
		}
	}
	return nil
}

// populateFields fills in info.Fields and info.IsFromSchema by looking the
// type up in the supplied contexts. If info has a Package qualifier the
// resolution prefers the schemas index (using imports to map alias → path);
// otherwise the local per-package type map is consulted. Fields that are
// already populated are left alone — verifyShape's contract is "non-empty
// Fields means we can compare", and once that holds we should not redo work.
func populateFields(
	info *goTypeInfo,
	imports map[string]string,
	localTypes map[string]map[string]string,
	schemaIdx *goTypeIndex,
) {
	if info == nil || len(info.Fields) > 0 {
		return
	}
	if info.Package != "" {
		importPath := imports[info.Package]
		if importPath != "" {
			if strings.HasPrefix(importPath, "github.com/meshery/schemas/models/") {
				info.IsFromSchema = true
			}
			if fields := schemaIdx.lookup(importPath, stripArrayPrefix(info.TypeName)); len(fields) > 0 {
				info.Fields = fields
				return
			}
		}
	}
	if fields := localTypes[stripArrayPrefix(info.TypeName)]; len(fields) > 0 {
		info.Fields = fields
	}
}

// stripArrayPrefix removes the leading "[]" added by typeFromExpr for slice
// arguments so type lookups land on the element type rather than missing.
func stripArrayPrefix(name string) string {
	return strings.TrimPrefix(name, "[]")
}

// identifyArgType inspects an expression that was passed into Decode/Encode/Bind/JSON
// and best-effort determines the underlying type name. We do not use go/types,
// so this only resolves cases where the literal type is visible at the call site.
func identifyArgType(expr ast.Expr) *goTypeInfo {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if e.Op.String() == "&" {
			return identifyArgType(e.X)
		}
	case *ast.CompositeLit:
		return typeFromExpr(e.Type)
	case *ast.CallExpr:
		// new(T) / make(T)
		if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "new" && len(e.Args) == 1 {
			return typeFromExpr(e.Args[0])
		}
	case *ast.Ident:
		// Bare identifier — caller falls back to the local-var map.
		return nil
	}
	return nil
}

func typeFromExpr(expr ast.Expr) *goTypeInfo {
	switch e := expr.(type) {
	case *ast.Ident:
		return &goTypeInfo{TypeName: e.Name}
	case *ast.SelectorExpr:
		if e.Sel == nil {
			return nil
		}
		pkg := ""
		if id, ok := e.X.(*ast.Ident); ok {
			pkg = id.Name
		}
		return &goTypeInfo{TypeName: e.Sel.Name, Package: pkg}
	case *ast.StarExpr:
		return typeFromExpr(e.X)
	case *ast.ArrayType:
		inner := typeFromExpr(e.Elt)
		if inner == nil {
			return nil
		}
		inner.TypeName = "[]" + inner.TypeName
		return inner
	}
	return nil
}

// sortConsumerEndpoints orders endpoints deterministically by path then method.
func sortConsumerEndpoints(eps []consumerEndpoint) {
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].Path != eps[j].Path {
			return eps[i].Path < eps[j].Path
		}
		return eps[i].Method < eps[j].Method
	})
}
