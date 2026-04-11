package validation

import (
	"sort"
	"strings"
	"testing"
)

func runGorilla(t *testing.T, src string) []consumerEndpoint {
	t.Helper()
	tree := mapTree{
		files: map[string][]byte{
			gorillaRouterFile: []byte(src),
		},
		label: "gorilla-test",
	}
	eps, err := parseGorillaRoutes(tree)
	if err != nil {
		t.Fatalf("parseGorillaRoutes: %v", err)
	}
	return eps
}

func gorillaWrap(body string) string {
	return `package router

type muxRouter struct{}

func (m *muxRouter) Handle(string, interface{}) *muxRouter   { return m }
func (m *muxRouter) HandleFunc(string, interface{}) *muxRouter { return m }
func (m *muxRouter) Methods(...string) *muxRouter             { return m }
func (m *muxRouter) PathPrefix(string) *muxRouter             { return m }
func (m *muxRouter) Handler(interface{}) *muxRouter           { return m }

type handlerSet struct{}

func (h *handlerSet) ProviderMiddleware(interface{}) interface{} { return nil }
func (h *handlerSet) AuthMiddleware(interface{}, ...interface{}) interface{} { return nil }
func (h *handlerSet) SessionInjectorMiddleware(interface{}) interface{} { return nil }
func (h *handlerSet) GetSystemDatabase()                                 {}
func (h *handlerSet) ServerVersionHandler()                              {}
func (h *handlerSet) ProviderHandler()                                   {}
func (h *handlerSet) ProviderUIHandler()                                 {}
func (h *handlerSet) StaticHandler()                                     {}
func (h *handlerSet) LogoutHandler()                                     {}

func register() {
	gMux := &muxRouter{}
	h := &handlerSet{}
	_ = gMux
	_ = h
` + body + `
}
`
}

func TestGorillaPattern1HandleWithMiddleware(t *testing.T) {
	body := `gMux.Handle("/api/system/database",
		h.ProviderMiddleware(h.AuthMiddleware(
			h.SessionInjectorMiddleware(h.GetSystemDatabase),
			"providerAuth"))).
		Methods("GET")`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d: %+v", len(eps), eps)
	}
	got := eps[0]
	if got.Path != "/api/system/database" {
		t.Errorf("path: got %q", got.Path)
	}
	if got.Method != "GET" {
		t.Errorf("method: got %q", got.Method)
	}
	if got.HandlerName != "GetSystemDatabase" {
		t.Errorf("handler: got %q (want GetSystemDatabase)", got.HandlerName)
	}
}

func TestGorillaPattern2HandleFunc(t *testing.T) {
	body := `gMux.HandleFunc("/api/system/version", h.ServerVersionHandler).
		Methods("POST")`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].HandlerName != "ServerVersionHandler" {
		t.Errorf("handler: got %q", eps[0].HandlerName)
	}
	if eps[0].Method != "POST" {
		t.Errorf("method: got %q", eps[0].Method)
	}
}

func TestGorillaPattern3HandleFuncNoMethods(t *testing.T) {
	body := `gMux.HandleFunc("/api/provider", h.ProviderHandler)`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].Method != "ANY" {
		t.Errorf("method: want ANY, got %q", eps[0].Method)
	}
}

func TestGorillaPattern4PathPrefix(t *testing.T) {
	body := `gMux.PathPrefix("/api/provider/extension").
		Handler(h.ProviderMiddleware(h.AuthMiddleware(h.ProviderHandler, "providerAuth"))).
		Methods("GET", "POST")`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 2 {
		t.Fatalf("expected 2 endpoints, got %d: %+v", len(eps), eps)
	}
	verbs := []string{eps[0].Method, eps[1].Method}
	sort.Strings(verbs)
	if verbs[0] != "GET" || verbs[1] != "POST" {
		t.Errorf("methods: got %v", verbs)
	}
	for _, ep := range eps {
		if ep.Path != "/api/provider/extension" {
			t.Errorf("path: got %q", ep.Path)
		}
		if ep.HandlerName != "ProviderHandler" {
			t.Errorf("handler: got %q", ep.HandlerName)
		}
		hasPrefixNote := false
		for _, n := range ep.Notes {
			if strings.Contains(n, "prefix match") {
				hasPrefixNote = true
			}
		}
		if !hasPrefixNote {
			t.Errorf("expected prefix-match note, got %v", ep.Notes)
		}
	}
}

func TestGorillaPattern5AnonymousFuncLit(t *testing.T) {
	body := `gMux.HandleFunc("/api/anon", func(w interface{}, r interface{}) {
		_ = w
		_ = r
	}).Methods("GET")`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].HandlerName != "(anonymous)" {
		t.Errorf("handler: got %q (want (anonymous))", eps[0].HandlerName)
	}
}

func TestGorillaAnonymousWithBodyCall(t *testing.T) {
	// Anonymous func wrapper around h.LogoutHandler.
	body := `gMux.HandleFunc("/user/logout", func(w interface{}, r interface{}) {
		h.LogoutHandler()
	}).Methods("GET")`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].HandlerName != "LogoutHandler" {
		t.Errorf("handler: got %q (want LogoutHandler)", eps[0].HandlerName)
	}
}

func TestGorillaMultipleMethods(t *testing.T) {
	body := `gMux.HandleFunc("/api/multi", h.ServerVersionHandler).
		Methods("GET", "POST", "PUT")`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(eps))
	}
}

func TestGorillaSorted(t *testing.T) {
	body := `gMux.HandleFunc("/zeta", h.ProviderHandler).Methods("GET")
gMux.HandleFunc("/alpha", h.ProviderHandler).Methods("GET")
gMux.HandleFunc("/alpha", h.ProviderHandler).Methods("POST")`
	eps := runGorilla(t, gorillaWrap(body))
	if len(eps) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(eps))
	}
	if eps[0].Path != "/alpha" || eps[0].Method != "GET" {
		t.Errorf("first: %+v", eps[0])
	}
	if eps[1].Path != "/alpha" || eps[1].Method != "POST" {
		t.Errorf("second: %+v", eps[1])
	}
	if eps[2].Path != "/zeta" {
		t.Errorf("third: %+v", eps[2])
	}
}

func TestGorillaPathPrefixHandlerNoMethods(t *testing.T) {
	// PathPrefix(...).Handler(...) with no trailing .Methods() must be
	// captured as ANY.
	body := `gMux.PathPrefix("/provider/_next").
		Handler(http.HandlerFunc(func(w interface{}, r interface{}) {
			h.ProviderUIHandler()
		}))`
	src := `package router
type muxRouter struct{}
func (m *muxRouter) PathPrefix(string) *muxRouter { return m }
func (m *muxRouter) Handler(interface{}) *muxRouter { return m }
type handlerSet struct{}
func (h *handlerSet) ProviderUIHandler() {}
type httpPkg struct{}
func (httpPkg) HandlerFunc(interface{}) interface{} { return nil }
var http httpPkg
func register() {
	gMux := &muxRouter{}
	h := &handlerSet{}
	_ = gMux
	_ = h
` + body + `
}
`
	eps := runGorilla(t, src)
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d: %+v", len(eps), eps)
	}
	if eps[0].Path != "/provider/_next" {
		t.Errorf("path: got %q", eps[0].Path)
	}
	if eps[0].Method != "ANY" {
		t.Errorf("method: want ANY, got %q", eps[0].Method)
	}
	if eps[0].HandlerName != "ProviderUIHandler" {
		t.Errorf("handler: got %q (want ProviderUIHandler)", eps[0].HandlerName)
	}
}

func TestGorillaStripPrefix(t *testing.T) {
	body := `gMux.PathPrefix("/provider/").
		Handler(http.StripPrefix("/provider/", h.ProviderUIHandler)).
		Methods("GET")`
	// Add http.StripPrefix shim to the synthetic source.
	src := `package router
type muxRouter struct{}
func (m *muxRouter) PathPrefix(string) *muxRouter { return m }
func (m *muxRouter) Handler(interface{}) *muxRouter { return m }
func (m *muxRouter) Methods(...string) *muxRouter { return m }
type handlerSet struct{}
func (h *handlerSet) ProviderUIHandler() {}
type httpPkg struct{}
func (httpPkg) StripPrefix(string, interface{}) interface{} { return nil }
var http httpPkg
func register() {
	gMux := &muxRouter{}
	h := &handlerSet{}
	_ = gMux
	_ = h
` + body + `
}
`
	eps := runGorilla(t, src)
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].HandlerName != "ProviderUIHandler" {
		t.Errorf("handler: got %q", eps[0].HandlerName)
	}
}
