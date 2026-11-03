package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func serveWeb(t *testing.T, web fstest.MapFS, path string) *http.Response {
	t.Helper()
	s := New(nil, "/repo")
	s.web = web
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var builtWeb = fstest.MapFS{
	"index.html":        {Data: []byte("<!doctype html><title>depgraph</title>")},
	"assets/index-1.js": {Data: []byte("console.log(1)")},
}

func TestRootServesBuiltUI(t *testing.T) {
	r := serveWeb(t, builtWeb, "/")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", r.StatusCode)
	}
	if got := body(t, r); !strings.Contains(got, "<title>depgraph</title>") {
		t.Fatalf("body = %q", got)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestRootServesAssets(t *testing.T) {
	r := serveWeb(t, builtWeb, "/assets/index-1.js")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", r.StatusCode)
	}
	if got := body(t, r); got != "console.log(1)" {
		t.Fatalf("body = %q", got)
	}
}

func TestUnknownAssetIs404(t *testing.T) {
	if r := serveWeb(t, builtWeb, "/assets/missing.js"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", r.StatusCode)
	}
}

func TestRootNotBuilt(t *testing.T) {
	for _, p := range []string{"/", "/assets/index-1.js"} {
		r := serveWeb(t, fstest.MapFS{".gitkeep": {}}, p)
		if r.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d", p, r.StatusCode)
		}
		// same guidance as the README: a release binary, or a source build with mise
		got := body(t, r)
		for _, want := range []string{"https://github.com/nonchan7720/depgraph/releases", "mise run build"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: body = %q, want it to mention %q", p, got, want)
			}
		}
	}
}

func TestLegacyUIIsGone(t *testing.T) {
	// the CDN-loading single-file UI was removed: only the built web/ app is served
	if r := serveWeb(t, builtWeb, "/legacy"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", r.StatusCode)
	}
}

func TestAPIRoutesAreNotShadowedByUI(t *testing.T) {
	// /api/package without key is answered by the API handler, not the file server
	r := serveWeb(t, builtWeb, "/api/package")
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", r.StatusCode)
	}
}

func TestEmbeddedWebHasPlaceholder(t *testing.T) {
	// dist/.gitkeep keeps //go:embed all:dist compiling before the first web build
	if _, err := embeddedWeb().Open(".gitkeep"); err != nil {
		t.Fatalf("dist/.gitkeep missing from embed: %v", err)
	}
}
