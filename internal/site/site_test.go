package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two wasm programs and three images")
	}
	out := t.TempDir()
	files, err := Build(context.Background(), Options{Root: "../..", Out: out, Version: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"index.html", "app.js", "sw.js", "engine-worker.js", "container-worker.js", "wasm_exec.js",
		"dashboard.wasm", "engine.wasm", ".nojekyll", "v2/library/hello/manifests/latest", "v2/library/clock/manifests/latest"} {
		if !slices.Contains(files, want) {
			t.Errorf("no %s in %v", want, files)
		}
	}
	page, _ := os.ReadFile(filepath.Join(out, "index.html"))
	if !strings.Contains(string(page), "app.js?v=t1") || strings.Contains(string(page), "{{BUILD}}") {
		t.Errorf("index.html not stamped:\n%s", page)
	}

	// Served: the wasm type, and the isolation headers when asked.
	h := Handler(out, "/dashboard/", true)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/engine.wasm", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/wasm" || rec.Header().Get("Cross-Origin-Embedder-Policy") != "require-corp" {
		t.Errorf("engine.wasm: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	Handler(out, "/dashboard/", false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if rec.Code != 200 || rec.Header().Get("Cross-Origin-Opener-Policy") != "" {
		t.Errorf("index: %d %v", rec.Code, rec.Header())
	}
}

func TestBuildRefusals(t *testing.T) {
	if _, err := Build(context.Background(), Options{Out: t.TempDir(), Version: `x"><script>`}); err == nil {
		t.Error("a version that could inject markup")
	}
	if _, err := Build(context.Background(), Options{Out: t.TempDir(), Version: "v", Go: "/nonexistent/go"}); err == nil {
		t.Error("no go command, no error")
	}
	file := filepath.Join(t.TempDir(), "f")
	os.WriteFile(file, nil, 0o644)
	if _, err := Build(context.Background(), Options{Out: filepath.Join(file, "sub"), Version: "v"}); err == nil {
		t.Error("an output under a file")
	}
}
