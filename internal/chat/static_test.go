package chat

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadStaticBuildIDIsDeterministicAndContentAddressed(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><html><head><title>t</title></head><body>hi</body></html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}

	first, err := LoadStatic(fsys)
	if err != nil {
		t.Fatalf("LoadStatic: %v", err)
	}

	second, err := LoadStatic(fsys)
	if err != nil {
		t.Fatalf("LoadStatic again: %v", err)
	}

	if first.BuildID == "" {
		t.Fatal("build id is empty")
	}

	if first.BuildID != second.BuildID {
		t.Fatalf("build id changed without a content change: %q != %q", first.BuildID, second.BuildID)
	}

	wantMeta := []byte(`<meta name="app-build" content="` + first.BuildID + `" />`)
	if !bytes.Contains(first.IndexHTML, wantMeta) {
		t.Fatalf("index.html does not contain %q", wantMeta)
	}

	fsys["assets/app.js"] = &fstest.MapFile{Data: []byte("console.log(2)")}

	changed, err := LoadStatic(fsys)
	if err != nil {
		t.Fatalf("LoadStatic after change: %v", err)
	}

	if changed.BuildID == first.BuildID {
		t.Fatal("build id did not change when an asset changed")
	}
}

func TestLoadStaticWithoutIndex(t *testing.T) {
	bundle, err := LoadStatic(fstest.MapFS{
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	})
	if err != nil {
		t.Fatalf("LoadStatic: %v", err)
	}

	if bundle.IndexHTML != nil {
		t.Fatal("index.html should be nil when the file is absent")
	}

	if bundle.BuildID == "" {
		t.Fatal("build id should still be computed from the remaining files")
	}
}

func TestStaticRoutesCacheHeadersAndFallback(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("<!doctype html><html><head><title>t</title></head><body>hi</body></html>")},
		"assets/app-abc.js": &fstest.MapFile{Data: []byte("console.log(1)")},
		"favicon.svg":       &fstest.MapFile{Data: []byte("<svg/>")},
	}

	bundle, err := LoadStatic(fsys)
	if err != nil {
		t.Fatalf("LoadStatic: %v", err)
	}

	srv := httptest.NewServer(Routes(Deps{
		Hub:       NewHub(),
		Sessions:  newTestStore(t),
		StaticFS:  fsys,
		IndexHTML: bundle.IndexHTML,
	}))
	t.Cleanup(srv.Close)

	get := func(path string) (*http.Response, string) {
		t.Helper()

		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		return resp, string(body)
	}

	resp, body := get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}

	if cache := resp.Header.Get("Cache-Control"); !strings.Contains(cache, "no-store") {
		t.Fatalf("index cache-control = %q, want no-store", cache)
	}

	if !strings.Contains(body, `<meta name="app-build" content="`+bundle.BuildID+`" />`) {
		t.Fatalf("index body is missing the build meta tag: %s", body)
	}

	resp, body = get("/room/abc")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "app-build") {
		t.Fatalf("SPA fallback: status=%d body=%q", resp.StatusCode, body)
	}

	resp, body = get("/assets/app-abc.js")
	if resp.StatusCode != http.StatusOK || body != "console.log(1)" {
		t.Fatalf("asset: status=%d body=%q", resp.StatusCode, body)
	}

	if cache := resp.Header.Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("asset cache-control = %q, want immutable", cache)
	}

	resp, _ = get("/assets/missing.js")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", resp.StatusCode)
	}

	resp, _ = get("/api/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown API status = %d, want 404", resp.StatusCode)
	}

	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("unknown API content-type = %q, want JSON", contentType)
	}
}
