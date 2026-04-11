package validation

import (
	"testing"
)

func runEcho(t *testing.T, files map[string][]byte) []consumerEndpoint {
	t.Helper()
	tree := mapTree{files: files, label: "echo-test"}
	eps, err := parseEchoRoutes(tree)
	if err != nil {
		t.Fatalf("parseEchoRoutes: %v", err)
	}
	return eps
}

func echoMain(body string) []byte {
	return []byte(`package router

type echoGroup struct{}

func (g *echoGroup) GET(string, ...interface{}) {}
func (g *echoGroup) POST(string, ...interface{}) {}
func (g *echoGroup) PUT(string, ...interface{}) {}
func (g *echoGroup) PATCH(string, ...interface{}) {}
func (g *echoGroup) DELETE(string, ...interface{}) {}

type echoServer struct {
	e *echoGroup
}

type academy struct{}
func (academy) RegisterToAcademyContent() {}
func (academy) GetAllAcademyContent() {}

type handlerContainer struct{}
func (handlerContainer) UpdateUserPreference() {}
func (handlerContainer) DeleteUserAccountById(...interface{}) {}
func (handlerContainer) AuthorizationMiddlewareForAdmin() {}

type echoServerExtras struct {
	academyHandler academy
	h              handlerContainer
}

type echoPkg struct{}
func (echoPkg) WrapHandler(interface{}) interface{} { return nil }
var echo echoPkg
type httpPkg struct{}
func (httpPkg) HandlerFunc(interface{}) interface{} { return nil }
var http httpPkg

func register() {
	authedAPI := &echoGroup{}
	s := &echoServerExtras{}
	srv := &echoServer{e: &echoGroup{}}
	_ = authedAPI
	_ = s
	_ = srv
` + body + `
}
`)
}

func TestEchoGroupMethod(t *testing.T) {
	body := `authedAPI.GET("/identity/users", s.h.UpdateUserPreference)`
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": echoMain(body),
	})
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d: %+v", len(eps), eps)
	}
	if eps[0].Path != "/api/identity/users" {
		t.Errorf("path: got %q", eps[0].Path)
	}
	if eps[0].Method != "GET" {
		t.Errorf("method: got %q", eps[0].Method)
	}
	if eps[0].HandlerName != "UpdateUserPreference" {
		t.Errorf("handler: got %q", eps[0].HandlerName)
	}
}

func TestEchoDirectCall(t *testing.T) {
	body := `srv.e.GET("/api/system/health", s.academyHandler.GetAllAcademyContent)`
	// srv.e flattens to "srv.e" — not in the prefix table — so the
	// parser leaves the absolute path alone. To test the s.e prefix
	// flow we use a struct field literally named "s.e".
	body = `s.e.GET("/api/system/health", s.academyHandler.GetAllAcademyContent)`
	src := []byte(`package router
type echoGroup struct{}
func (g *echoGroup) GET(string, ...interface{}) {}
type echoSrv struct { e *echoGroup }
type academy struct{}
func (academy) GetAllAcademyContent() {}
type extras struct{ academyHandler academy }
func register() {
	s := &echoSrv{e: &echoGroup{}}
	_ = s
	_ = &extras{}
` + body + `
}
`)
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": src,
	})
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d: %+v", len(eps), eps)
	}
	if eps[0].Path != "/api/system/health" {
		t.Errorf("path: got %q (no double prefix)", eps[0].Path)
	}
}

func TestEchoWrapHandler(t *testing.T) {
	body := `authedAPI.PUT("/identity/users/profile", echo.WrapHandler(http.HandlerFunc(s.h.UpdateUserPreference)))`
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": echoMain(body),
	})
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].HandlerName != "UpdateUserPreference" {
		t.Errorf("handler: got %q (want UpdateUserPreference)", eps[0].HandlerName)
	}
}

func TestEchoInlineHandler(t *testing.T) {
	body := `authedAPI.DELETE("/identity/users/:userId", func(c interface{}) error {
		s.h.DeleteUserAccountById(c, c, c)
		return nil
	}, s.h.AuthorizationMiddlewareForAdmin)`
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": echoMain(body),
	})
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].Path != "/api/identity/users/{userId}" {
		t.Errorf("path: got %q", eps[0].Path)
	}
	if eps[0].HandlerName != "DeleteUserAccountById" {
		t.Errorf("handler: got %q (want DeleteUserAccountById)", eps[0].HandlerName)
	}
}

func TestEchoParamNormalization(t *testing.T) {
	body := `authedAPI.GET("/identity/users/:userId/keys/:keyId", s.h.UpdateUserPreference)`
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": echoMain(body),
	})
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].Path != "/api/identity/users/{userId}/keys/{keyId}" {
		t.Errorf("path: got %q", eps[0].Path)
	}
}

func TestEchoMultipleFiles(t *testing.T) {
	main := echoMain(`authedAPI.GET("/identity/users", s.h.UpdateUserPreference)`)
	delegated := []byte(`package invitations
type echoGroup struct{}
func (g *echoGroup) POST(string, ...interface{}) {}
type handlers struct{}
func (h *handlers) AcceptInvite() {}
func RegisterRoutes() {
	authedAPI := &echoGroup{}
	h := &handlers{}
	_ = authedAPI
	_ = h
	authedAPI.POST("/invitations/accept", h.AcceptInvite)
}
`)
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go":                 main,
		"server/handlers/invitations/handlers.go": delegated,
	})
	if len(eps) != 2 {
		t.Fatalf("expected 2 endpoints, got %d: %+v", len(eps), eps)
	}
	wantPaths := map[string]bool{
		"/api/identity/users":     false,
		"/api/invitations/accept": false,
	}
	for _, ep := range eps {
		if _, ok := wantPaths[ep.Path]; ok {
			wantPaths[ep.Path] = true
		}
	}
	for p, seen := range wantPaths {
		if !seen {
			t.Errorf("missing path %q", p)
		}
	}
}

func TestEchoFmtSprintfPath(t *testing.T) {
	// meshery-cloud/server/router/router.go:824 — fmt.Sprintf path with
	// models.KUBERNETES substitution.
	body := `authedAPI.GET(fmt.Sprintf("/integrations/connections/%s/:connectionID/context", models.KUBERNETES), s.h.UpdateUserPreference)`
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": echoMain(body),
	})
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d: %+v", len(eps), eps)
	}
	want := "/api/integrations/connections/kubernetes/{connectionID}/context"
	if eps[0].Path != want {
		t.Errorf("path: got %q, want %q", eps[0].Path, want)
	}
}

func TestEchoFmtSprintfPathUnresolved(t *testing.T) {
	// fmt.Sprintf with an unknown identifier — must drop the route, not
	// emit a placeholder path.
	body := `authedAPI.GET(fmt.Sprintf("/x/%s", models.UNKNOWN_THING), s.h.UpdateUserPreference)`
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": echoMain(body),
	})
	if len(eps) != 0 {
		t.Fatalf("expected route to be skipped, got %d endpoints: %+v", len(eps), eps)
	}
}

func TestEchoSorted(t *testing.T) {
	body := `authedAPI.GET("/zeta", s.h.UpdateUserPreference)
authedAPI.POST("/alpha", s.h.UpdateUserPreference)
authedAPI.GET("/alpha", s.h.UpdateUserPreference)`
	eps := runEcho(t, map[string][]byte{
		"server/router/router.go": echoMain(body),
	})
	if len(eps) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(eps))
	}
	if eps[0].Path != "/api/alpha" || eps[0].Method != "GET" {
		t.Errorf("first: %+v", eps[0])
	}
	if eps[1].Path != "/api/alpha" || eps[1].Method != "POST" {
		t.Errorf("second: %+v", eps[1])
	}
	if eps[2].Path != "/api/zeta" {
		t.Errorf("third: %+v", eps[2])
	}
}
