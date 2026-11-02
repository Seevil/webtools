package clone

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLanguageDirectoryFiltering(t *testing.T) {
	for _, tc := range []struct {
		start, target string
		allow         bool
	}{
		{"/", "/en/about", false},
		{"/", "/fr/", false},
		{"/", "/english/about", true},
		{"/", "/business/", true},
		{"/", "/us/products", true},
		{"/", "/assets/en/logo.svg", true},
		{"/en/", "/en/about", true},
		{"/en/", "/EN/products", true},
		{"/en/", "/fr/about", false},
		{"/en/", "/about", true},
		{"/zh-cn/", "/zh-cn/about", true},
		{"/zh-cn/", "/zh-tw/about", false},
	} {
		c := DefaultConfig()
		c.URL = "https://example.invalid" + tc.start
		c.FilterLanguages = true
		start, err := c.Validate()
		if err != nil {
			t.Fatal(err)
		}
		got := c.pageAllowed(parseURL(t, "https://example.invalid"+tc.target), start)
		if got != tc.allow {
			t.Errorf("%s → %s: got %t, want %t", tc.start, tc.target, got, tc.allow)
		}
		c.FilterLanguages = false
		if !c.pageAllowed(parseURL(t, "https://example.invalid"+tc.target), start) {
			t.Errorf("disabling filter still excludes %s", tc.target)
		}
	}
}

func TestLanguageDirectoryFilteringDownloadAndResume(t *testing.T) {
	var mu sync.Mutex
	requested := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requested[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/en/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<a href="/en/next">next</a><a href="/fr/about">fr</a><a href="/docs/about">docs</a><img src="/static/image.png">`)
		case "/en/next":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<h1>next</h1>")
		case "/docs/about":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<h1>docs</h1>")
		case "/static/image.png":
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "asset")
		default:
			http.Error(w, "other language", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL+"/en/")
	cfg.FilterLanguages = true
	result, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pages != 3 || result.Assets != 1 || result.Failed != 0 {
		t.Fatalf("unexpected filtered crawl: %+v", result)
	}
	mu.Lock()
	if requested["/fr/about"] != 0 || requested["/en/next"] != 1 || requested["/docs/about"] != 1 {
		t.Errorf("unexpected requests: %v", requested)
	}
	mu.Unlock()
	content := readText(t, filepath.Join(result.Directory, result.Entry))
	if !strings.Contains(content, srv.URL+"/fr/about") {
		t.Fatal("excluded language link should remain remote")
	}
	if _, err := os.Stat(filepath.Join(result.Directory, "static", "picture", "image.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(context.Background(), result.Directory, "", nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requested["/fr/about"] != 0 {
		t.Fatal("resume downloaded excluded language page")
	}
}
