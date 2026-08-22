package staticweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedPlaceholderKeepsCleanCheckoutBuildable(t *testing.T) {
	handler, err := NewEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
	if response.Code != http.StatusOK || !contains(response.Body.String(), "AISummoner WebUI assets") {
		t.Fatalf("embedded placeholder response = %d %q", response.Code, response.Body.String())
	}
}

func TestStaticHandlerCacheAndSPAFallback(t *testing.T) {
	handler := testHandler(t)
	tests := []struct {
		name      string
		path      string
		wantCode  int
		wantBody  string
		wantCache string
	}{
		{name: "index", path: "/", wantCode: 200, wantBody: "app-shell", wantCache: noCache},
		{name: "SPA route", path: "/devices/dev_one/agent", wantCode: 200, wantBody: "app-shell", wantCache: noCache},
		{name: "hashed asset", path: "/assets/index-ABCDEFGH.js", wantCode: 200, wantBody: "asset-body", wantCache: immutableAssets},
		{name: "unhashed asset", path: "/assets/logo.svg", wantCode: 200, wantBody: "svg-body", wantCache: noCache},
		{name: "asset directory", path: "/assets", wantCode: 404},
		{name: "missing asset", path: "/assets/missing-ABCDEFGH.js", wantCode: 404},
		{name: "missing file", path: "/favicon.ico", wantCode: 404},
		{name: "API root", path: "/api", wantCode: 404},
		{name: "API child", path: "/api/v1/missing", wantCode: 404},
		{name: "internal bridge", path: "/internal/opencode/remote-exec", wantCode: 404},
		{name: "health child", path: "/healthz/details", wantCode: 404},
		{name: "dot traversal", path: "/../index.html", wantCode: 404},
		{name: "backslash traversal", path: `/assets\..\index.html`, wantCode: 404},
		{name: "double slash", path: "/assets//index-ABCDEFGH.js", wantCode: 404},
		{name: "leading double slash", path: "//assets/index-ABCDEFGH.js", wantCode: 404},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
			request.URL.Path = test.path
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantCode {
				t.Fatalf("status = %d, want %d; body=%q", response.Code, test.wantCode, response.Body.String())
			}
			if test.wantBody != "" && !contains(response.Body.String(), test.wantBody) {
				t.Fatalf("body = %q, want marker %q", response.Body.String(), test.wantBody)
			}
			if test.wantCache != "" && response.Header().Get("Cache-Control") != test.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", response.Header().Get("Cache-Control"), test.wantCache)
			}
		})
	}
}

func TestStaticHandlerHEADAndMethodBoundary(t *testing.T) {
	handler := testHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "http://example.test/assets/index-ABCDEFGH.js", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d body=%q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://example.test/devices", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST response = %d Allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestStaticHandlerRejectsNUL(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	request.URL.Path = "/assets/\x00index.js"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("NUL path status = %d", response.Code)
	}
}

func TestNewRequiresIndex(t *testing.T) {
	if _, err := New(fstest.MapFS{}); err == nil {
		t.Fatal("filesystem without index.html was accepted")
	}
}

func testHandler(t *testing.T) *Handler {
	t.Helper()
	handler, err := New(fstest.MapFS{
		"index.html":               {Data: []byte("<html>app-shell</html>")},
		"assets/index-ABCDEFGH.js": {Data: []byte("asset-body")},
		"assets/logo.svg":          {Data: []byte("svg-body")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func contains(value, fragment string) bool {
	return strings.Contains(value, fragment)
}
