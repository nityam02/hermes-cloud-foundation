package uiserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	static := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/a.js": {Data: []byte("x")}}
	h, err := New(Documents{Inventory: []byte(`{"kind":"Inventory"}`)}, static)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, target, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServesDocumentsAndStatus(t *testing.T) {
	h := testHandler(t)
	if w := do(h, "GET", "/api/inventory.json", "127.0.0.1:8787"); w.Code != 200 || !strings.Contains(w.Body.String(), "Inventory") {
		t.Fatalf("inventory: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/plan.json", "127.0.0.1:8787"); w.Code != 404 {
		t.Fatalf("missing document should be 404, got %d", w.Code)
	}
	w := do(h, "GET", "/api/status", "localhost:8787")
	if !strings.Contains(w.Body.String(), `"inventory":true`) || !strings.Contains(w.Body.String(), `"plan":false`) || !strings.Contains(w.Body.String(), `"read_only":true`) {
		t.Fatalf("status: %s", w.Body)
	}
}

func TestReadOnlyAndHostGuard(t *testing.T) {
	h := testHandler(t)
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if w := do(h, m, "/api/inventory.json", "127.0.0.1:8787"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s should be refused, got %d", m, w.Code)
		}
	}
	for _, host := range []string{"evil.example.com", "evil.example.com:8787", "10.0.0.5:8787"} {
		if w := do(h, "GET", "/", host); w.Code != http.StatusForbidden {
			t.Errorf("Host %q should be refused (DNS rebinding), got %d", host, w.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:1", "localhost", "[::1]:9"} {
		if w := do(h, "GET", "/", host); w.Code != 200 {
			t.Errorf("Host %q should be allowed, got %d", host, w.Code)
		}
	}
}

func TestSecurityHeadersAndSPAFallback(t *testing.T) {
	h := testHandler(t)
	w := do(h, "GET", "/inventory/some/deep/link", "127.0.0.1:8787")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "app") {
		t.Fatalf("unknown path should load the app shell: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'self'") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing security headers: %v", w.Header())
	}
	if w := do(h, "GET", "/api/unknown", "127.0.0.1:8787"); w.Code != 404 {
		t.Fatalf("unknown api path: %d", w.Code)
	}
}

func TestValidateAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:8787", "localhost:8787", "[::1]:8787"} {
		if err := ValidateAddr(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:8787", ":8787", "192.168.1.5:8787", "example.com:80", "nonsense"} {
		if err := ValidateAddr(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestNotBuiltPage(t *testing.T) {
	h, err := New(Documents{}, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	w := do(h, "GET", "/", "127.0.0.1:8787")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "make ui-build") {
		t.Fatalf("expected the not-built page, got %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/status", "127.0.0.1:8787"); w.Code != 200 {
		t.Fatalf("the API must work without the web app: %d", w.Code)
	}
}
