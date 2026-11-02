package clone

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestDefaultUserAgent(t *testing.T) {
	const want = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
	if got := DefaultConfig().UserAgent; got != want {
		t.Fatalf("default User-Agent = %q, want %q", got, want)
	}
}

func testConfig(t *testing.T, target string) Config {
	t.Helper()
	c := DefaultConfig()
	c.URL = target
	c.Output = t.TempDir()
	c.DelayMillis = 0
	return c
}

func readText(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCloneAndRewrite(t *testing.T) {
	var forbidden atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><link rel="stylesheet" integrity="test" href="/style.css"><script src="/app.js"></script></head><body><button onclick="this.textContent='ok'">Click</button><img src="/image.png"><a href="/next#part">Next</a><a href="/skip/private">No</a><img src="/missing.png"><form action="/submit"></form></body></html>`)
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<h1 id="part">Next</h1><a href="/">Home</a>`)
	})
	mux.HandleFunc("/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		fmt.Fprint(w, `@import "/theme.css";body{background:url('/image.png')}`)
	})
	mux.HandleFunc("/theme.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		fmt.Fprint(w, `body{color:navy}`)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, `window.cloned = true;`)
	})
	mux.HandleFunc("/image.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{137, 80, 78, 71})
	})
	mux.HandleFunc("/skip/private", func(w http.ResponseWriter, r *http.Request) { forbidden.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := testConfig(t, srv.URL)
	c.Exclude = "/skip"
	r, err := Run(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pages != 2 || r.Assets != 4 || r.Failed != 1 {
		t.Fatalf("unexpected counts: %+v", r)
	}
	if forbidden.Load() != 0 {
		t.Fatal("excluded path fetched")
	}
	page := readText(t, filepath.Join(r.Directory, r.Entry))
	for _, bad := range []string{`href="/style.css"`, `src="/app.js"`, `action="/submit"`, `integrity="test"`} {
		if strings.Contains(page, bad) {
			t.Fatalf("unrewritten content: %s", bad)
		}
	}
	if !strings.Contains(page, `onclick="this.textContent='ok'"`) {
		t.Fatal("interaction modified")
	}
	if !strings.Contains(page, srv.URL+"/missing.png") {
		t.Fatal("failed asset must keep remote fallback")
	}
	if !strings.Contains(page, "#part") {
		t.Fatal("fragment lost")
	}
	css := readText(t, filepath.Join(r.Directory, "static", "css", "style.css"))
	if strings.Contains(css, "url('/image.png')") || strings.Contains(css, `@import "/theme.css"`) {
		t.Fatal("CSS not localized")
	}
	js := readText(t, filepath.Join(r.Directory, "static", "js", "app.js"))
	if js != `window.cloned = true;` {
		t.Fatal("script body changed")
	}
}

func TestMarkupBaseUnicodeAndScript(t *testing.T) {
	origin := parseURL(t, "https://example.invalid/root/")
	input := `<title>İ中文</title><base href="/assets/"><script>const text = '<img src="fake.png">';</script><style>/* İ */p{background:URL('bg.png')}</style><img src="one.png" srcset="two.png 2x, three.png 3x">`
	base := documentBase(input, origin)
	if base.Path != "/assets/" {
		t.Fatal(base)
	}
	var refs []string
	out := processHTML(input, func(s string, _ bool) string { refs = append(refs, s); return "local/" + s })
	if strings.Contains(out, "<base") || !strings.Contains(out, `src="local/one.png"`) {
		t.Fatal(out)
	}
	if !strings.Contains(out, `const text = '<img src="fake.png">';`) {
		t.Fatal("script was parsed as markup")
	}
	if !strings.Contains(out, `URL('local/bg.png')`) || !strings.Contains(out, "İ中文") {
		t.Fatal("Unicode offsets corrupted")
	}
	if len(refs) != 4 {
		t.Fatalf("references: %v", refs)
	}
}

func TestCSSDataCommentsAndEscapes(t *testing.T) {
	s := `/* url(fake) */a{content:"url(fake2)";background:url("a.png");mask:url(data:image/svg+xml;base64,AAAA)}@import 'b.css';c{background:url(escaped\ name.png)}`
	var refs []string
	out := processCSS(s, func(s string) string {
		refs = append(refs, s)
		if strings.HasPrefix(s, "data:") {
			return s
		}
		return "local/" + s
	})
	if len(refs) != 3 || !strings.Contains(out, "local/a.png") || !strings.Contains(out, "local/b.css") {
		t.Fatalf("%v: %s", refs, out)
	}
	if !strings.Contains(out, `url(escaped\ name.png)`) {
		t.Fatal("escaped URL corrupted")
	}
	if got := srcset("data:image/png;base64,AAAA 1x, b.png 2x", func(s string) string { return "wrong" }); !strings.Contains(got, "data:") {
		t.Fatal(got)
	}
}

func TestScopeAndValidation(t *testing.T) {
	c := DefaultConfig()
	c.URL = "https://example.invalid/allowed/"
	c.Domains = "*.example.invalid"
	c.Include = "/allowed"
	c.Exclude = "/allowed/private"
	start, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"https://sub.example.invalid/allowed/a", true},
		{"https://badexample.invalid/allowed/a", false},
		{"https://example.invalid/allowed/private/a", false},
		{"https://example.invalid/outside", false},
	} {
		if got := c.pageAllowed(parseURL(t, tc.raw), start); got != tc.want {
			t.Fatalf("%s: %v", tc.raw, got)
		}
	}
	c.Cookie = "x=y\r\nInjected: true"
	if _, err = c.Validate(); err == nil {
		t.Fatal("header injection accepted")
	}
	c.Cookie = ""
	c.MaxPages = 0
	if _, err = c.Validate(); err == nil {
		t.Fatal("invalid page cap accepted")
	}
}

func TestMultipleDomainRules(t *testing.T) {
	c := DefaultConfig()
	c.URL = "https://www.example.com/"
	c.Domains = "*.example.com; static.example.net,cdn.example.org\tassets.example.cn"
	start, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://a.example.com/", "https://static.example.net/", "https://cdn.example.org/", "https://assets.example.cn/"} {
		if !c.pageAllowed(parseURL(t, raw), start) {
			t.Fatalf("domain not allowed: %s", raw)
		}
	}
	if c.pageAllowed(parseURL(t, "https://notexample.com/"), start) {
		t.Fatal("unrelated domain allowed")
	}
	for _, bad := range []string{"bad..example.com", "-bad.example.com", "bad_.example.com", "*.example.com*"} {
		c.Domains = bad
		if _, err := c.Validate(); err == nil {
			t.Fatalf("invalid domain accepted: %s", bad)
		}
	}
}

func TestConfigNeverPersistsCookie(t *testing.T) {
	c := testConfig(t, "https://example.invalid/")
	c.Cookie = "private-test-cookie"
	file := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(file, c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readText(t, file), c.Cookie) {
		t.Fatal("cookie persisted")
	}
	loaded, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Cookie != "" {
		t.Fatal("cookie loaded")
	}
}

func TestCredentialOriginBoundary(t *testing.T) {
	var leak atomic.Bool
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			leak.Store(true)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("image"))
	}))
	defer foreign.Close()
	var ownCookie atomic.Bool
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "session=private" {
			ownCookie.Store(true)
		}
		if r.URL.Path == "/redirect.png" {
			http.Redirect(w, r, foreign.URL+"/redirected.png", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<img src="%s/direct.png"><img src="/redirect.png">`, foreign.URL)
	}))
	defer origin.Close()
	c := testConfig(t, origin.URL)
	c.Cookie = "session=private"
	c.Referer = origin.URL + "/private"
	c.ExternalAssets = true
	if _, err := Run(context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	if leak.Load() {
		t.Fatal("credentials leaked across origins")
	}
	if !ownCookie.Load() {
		t.Fatal("same-origin cookie missing")
	}
}

func TestDepthZeroAndCancellation(t *testing.T) {
	var child atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/child" {
			child.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<a href="/child">Child</a>`)
	}))
	defer srv.Close()
	c := testConfig(t, srv.URL)
	c.MaxDepth = 0
	if _, err := Run(context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	if child.Load() != 0 {
		t.Fatal("depth zero fetched child")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := Run(ctx, c, nil)
	if !errors.Is(err, context.Canceled) || !r.Cancelled {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestInFlightCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer srv.Close()
	c := testConfig(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Run(ctx, c, nil); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not interrupt request")
	}
}

func TestSafePathsAndQueryIdentity(t *testing.T) {
	u1 := parseURL(t, "https://example.invalid/a/CON?a=1")
	u2 := parseURL(t, "https://example.invalid/a/CON?a=2")
	registry := newFileRegistry()
	one, _ := registry.choose(pagePath(u1), u1, []byte("content1"))
	two, _ := registry.choose(pagePath(u2), u2, []byte("content2"))
	if one == two || !filepath.IsLocal(one) || strings.Contains(one, "..") {
		t.Fatalf("%q %q", one, two)
	}
	// Identical content at the same path should not rename, should reuse path
	three, reused := registry.choose(pagePath(u1), parseURL(t, "https://example.invalid/b/CON?a=1"), []byte("content1"))
	if three != one || !reused {
		t.Fatalf("identical file was renamed: got %q, want %q", three, one)
	}
	if strings.ContainsAny(safeSegment(`a:b?c*`), `:?*`) {
		t.Fatal("unsafe filename")
	}
}

func TestFilePathsAndDomainDirectory(t *testing.T) {
	paths := DefaultFilePaths()
	if got := assetPath(parseURL(t, "https://example.invalid/assets/photo.jpeg"), "image/jpeg", paths); got != filepath.Join("static", "picture", "photo.jpeg") {
		t.Fatal(got)
	}
	if got := assetPath(parseURL(t, "https://example.invalid/assets/logo.svg"), "image/svg+xml", paths); got != filepath.Join("static", "image", "logo.svg") {
		t.Fatal(got)
	}
	if got := pagePath(parseURL(t, "https://example.invalid/docs/about.html")); got != "about.html" {
		t.Fatal(got)
	}
	if got := RootDomainWildcard("https://Sub.Example.com.cn:8443/a"); got != "*.example.com.cn" {
		t.Fatal(got)
	}
	root := t.TempDir()
	first, err := newOutput(root, "example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newOutput(root, "example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "example.invalid" || filepath.Base(second) != "example.invalid-2" {
		t.Fatalf("unexpected output folders %q %q", first, second)
	}
}

func TestOldConfigDefaultsFilePaths(t *testing.T) {
	c := testConfig(t, "https://example.invalid/")
	c.FilePaths = FilePaths{}
	file := filepath.Join(t.TempDir(), "old.json")
	if err := SaveConfig(file, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.FilePaths != DefaultFilePaths() || loaded.Domains != "*.example.invalid" {
		t.Fatalf("defaults missing: %+v", loaded)
	}
}

func TestFilePathValidation(t *testing.T) {
	c := testConfig(t, "https://example.invalid/")
	for _, bad := range []string{"../escape", "/static/../escape/", "C:/absolute/", "/static//css/", "/static/con/"} {
		c.FilePaths.CSS = bad
		if _, err := c.Validate(); err == nil {
			t.Errorf("accepted unsafe path %q", bad)
		}
	}
	c.FilePaths = DefaultFilePaths()
	c.FilePaths.CSS = "/assets/css/"
	file := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(file, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.FilePaths.CSS != c.FilePaths.CSS {
		t.Fatalf("custom path lost: %+v", loaded.FilePaths)
	}
}

func TestCustomAssetDirectoryAndOriginalFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<link rel="stylesheet" href="/original-name.css"><img src="/品牌图.png">`)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css")
		}
		fmt.Fprint(w, "asset")
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.FilePaths.CSS = "/assets/styles/"
	cfg.FilePaths.Picture = "/assets/photos/"
	r, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join("assets", "styles", "original-name.css"), filepath.Join("assets", "photos", "品牌图.png")} {
		if _, err := os.Stat(filepath.Join(r.Directory, file)); err != nil {
			t.Fatalf("missing original name %q: %v", file, err)
		}
	}
	page := readText(t, filepath.Join(r.Directory, r.Entry))
	if strings.Contains(page, `href="/original-name.css"`) || strings.Contains(page, `src="/品牌图.png"`) || !strings.Contains(page, "assets/styles/original-name.css") {
		t.Fatal("custom paths not localized: " + page)
	}
}

func TestNameCollisionAndLocalLinks(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<link rel="stylesheet" href="/site.css"><script src="/bundle.js"></script><img src="/a/logo.png"><img src="/b/logo.png"><a href="/story.html?q=1">one</a><a href="/story.html?q=2">two</a>`)
		case "/story.html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<p>story</p>`)
		case "/a/logo.png":
			hits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "logo-content-A")
		case "/b/logo.png":
			hits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "logo-content-B-different")
		default:
			hits.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "asset")
		}
	}))
	defer server.Close()
	c := testConfig(t, server.URL)
	r, err := Run(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(r.Directory) != "127.0.0.1" {
		t.Fatalf("output folder %q", r.Directory)
	}
	if r.Pages != 3 || r.Assets != 4 || hits.Load() != 4 {
		t.Fatalf("unexpected result: %+v, hits %d", r, hits.Load())
	}
	for _, file := range []string{filepath.Join("static", "css", "site.css"), filepath.Join("static", "js", "bundle.js"), filepath.Join("static", "picture", "logo.png"), "story.html"} {
		if _, err := os.Stat(filepath.Join(r.Directory, file)); err != nil {
			t.Fatal(err)
		}
	}
	page := readText(t, filepath.Join(r.Directory, r.Entry))
	if strings.Contains(page, `src="/a/logo.png"`) || strings.Contains(page, `src="/b/logo.png"`) || strings.Contains(page, "story.html?q=") {
		t.Fatal("links were not localized: " + page)
	}
	entries, err := os.ReadDir(filepath.Join(r.Directory, "static", "picture"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() == entries[1].Name() {
		t.Fatalf("collision not distinguished: %v", entries)
	}
}

func TestRepeatedCollisionReusesMatchingJSAndCSS(t *testing.T) {
	const page = `<script src="/first/bundle.js"></script><script src="/second/bundle.js"></script><script src="/third/bundle.js"></script><link rel="stylesheet" href="/first/theme.css"><link rel="stylesheet" href="/second/theme.css"><link rel="stylesheet" href="/third/theme.css">`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".js") {
			w.Header().Set("Content-Type", "application/javascript")
		} else {
			w.Header().Set("Content-Type", "text/css")
		}
		if strings.Contains(r.URL.Path, "/first/") {
			fmt.Fprint(w, "/* first */")
		} else {
			fmt.Fprint(w, "/* same */")
		}
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.Concurrency = 1
	r, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join("static", "js"), filepath.Join("static", "css")} {
		entries, err := os.ReadDir(filepath.Join(r.Directory, dir))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("%s: same content saved in %d files: %v", dir, len(entries), entries)
		}
	}
	body := readText(t, filepath.Join(r.Directory, r.Entry))
	for _, name := range []string{"bundle.js", "theme.css"} {
		var refs []string
		for _, tag := range tags(body) {
			for _, a := range tag.attrs {
				if (a.name == "src" || a.name == "href") && strings.HasSuffix(a.value, "."+strings.TrimPrefix(filepath.Ext(name), ".")) {
					refs = append(refs, a.value)
				}
			}
		}
		if len(refs) != 3 || refs[1] != refs[2] || refs[0] == refs[1] {
			t.Fatalf("%s links did not share matching content: %v", name, refs)
		}
	}
}

func TestSameNamedStaticAssetsAcrossOriginAndCDN(t *testing.T) {
	var cdn *httptest.Server
	cdn = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, "window.shared = true")
	}))
	defer cdn.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<script src="/app.js"></script><script src="%s/app.js"></script>`, cdn.URL)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, "window.shared = true")
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.ExternalAssets = true
	r, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(r.Directory, "static", "js", "app.js")
	if _, err := os.Stat(own); err != nil {
		t.Fatal(err)
	}
	cdnFile := filepath.Join(r.Directory, "external", safeSegment(strings.TrimPrefix(cdn.URL, "http://")), "static", "js", "app.js")
	if _, err := os.Stat(cdnFile); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, tag := range tags(readText(t, filepath.Join(r.Directory, r.Entry))) {
		for _, a := range tag.attrs {
			if a.name == "src" {
				refs = append(refs, a.value)
			}
		}
	}
	if len(refs) != 2 || refs[0] == refs[1] {
		t.Fatalf("cross-origin assets must retain their separate paths: %v", refs)
	}
}

func TestSameCSSBytesWithDifferentRelativeReferencesRemainSeparate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<link rel="stylesheet" href="/a/main.css"><link rel="stylesheet" href="/b/main.css">`)
		case strings.HasSuffix(r.URL.Path, ".css"):
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, `body{background:url(logo.png)}`)
		default:
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, r.URL.Path)
		}
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	r, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(r.Directory, "static", "css"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("distinct relative CSS targets were merged: %v, %v", entries, err)
	}
	for _, entry := range entries {
		css := readText(t, filepath.Join(r.Directory, "static", "css", entry.Name()))
		if strings.Contains(css, "url(logo.png)") {
			t.Fatalf("CSS reference not localized: %s", css)
		}
	}
}

func TestResumeMissingOrCorruptRecordIsRedownloaded(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<img src="/asset.png">`)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "original")
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	first, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(first.Directory, "static", "picture", "asset.png")
	if err := os.WriteFile(asset, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	before := hits.Load()
	if _, err = Resume(context.Background(), first.Directory, "", nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() <= before {
		t.Fatalf("corrupt asset was reused: hits %d -> %d", before, hits.Load())
	}
	foundOriginal := false
	entries, err := os.ReadDir(filepath.Dir(asset))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if readText(t, filepath.Join(filepath.Dir(asset), entry.Name())) == "original" {
			foundOriginal = true
		}
	}
	if !foundOriginal {
		t.Fatal("redownloaded asset not found")
	}
	before = hits.Load()
	if _, err = Resume(context.Background(), first.Directory, "", nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != before {
		t.Fatal("valid resumed asset was downloaded again")
	}
}

func TestResumeCorruptCSSUpdatesReferencesAcrossResumes(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<link rel="stylesheet" href="/css/main.css">`)
		case "/css/main.css":
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, `body{background:url("/img/bg.png")}`)
		case "/img/bg.png":
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "image")
		}
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	first, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(first.Directory, "static", "css", "main.css")
	if err := os.WriteFile(css, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	before := hits.Load()
	second, err := Resume(context.Background(), first.Directory, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() <= before {
		t.Fatal("corrupt CSS was not downloaded")
	}
	page := readText(t, filepath.Join(second.Directory, second.Entry))
	if !strings.Contains(page, "static/css/main-") || strings.Contains(page, `href="static/css/main.css"`) {
		t.Fatalf("stale CSS reference: %s", page)
	}
	fixed := readText(t, filepath.Join(second.Directory, "static", "css", "main-"+strings.Split(strings.Split(page, "static/css/main-")[1], `"`)[0]))
	if !strings.Contains(fixed, "../picture/bg.png") {
		t.Fatalf("new CSS was not localized: %s", fixed)
	}
	before = hits.Load()
	third, err := Resume(context.Background(), first.Directory, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != before {
		t.Fatalf("third resume redownloaded repaired CSS: %d -> %d", before, hits.Load())
	}
	if !strings.Contains(readText(t, filepath.Join(third.Directory, third.Entry)), "static/css/main-") {
		t.Fatal("third resume lost repaired reference")
	}
}

func TestResumeCorruptImageUpdatesCSSRelativeFragment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<link rel="stylesheet" href="/css/main.css">`)
		case "/css/main.css":
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, `body{background:url("../img/bg.png#x")}`)
		case "/img/bg.png":
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "image")
		}
	}))
	defer server.Close()
	cfg := testConfig(t, server.URL)
	first, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(first.Directory, "static", "picture", "bg.png")
	if err := os.WriteFile(image, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := Resume(context.Background(), first.Directory, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	page := readText(t, filepath.Join(second.Directory, second.Entry))
	if !strings.Contains(page, `href="static/css/main.css"`) {
		t.Fatalf("page lost CSS reference: %s", page)
	}
	cssName := "main.css"
	css := readText(t, filepath.Join(second.Directory, "static", "css", cssName))
	if !strings.Contains(css, "../picture/bg-") || !strings.Contains(css, "#x") || strings.Contains(css, "../picture/bg.png#x") {
		t.Fatalf("CSS did not preserve redirected fragment: %s", css)
	}
	third, err := Resume(context.Background(), first.Directory, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	thirdPage := readText(t, filepath.Join(third.Directory, third.Entry))
	if !strings.Contains(thirdPage, `href="static/css/`+cssName+`"`) {
		t.Fatalf("third resume changed repaired CSS: %s", thirdPage)
	}
}

func BenchmarkRegistryChooseHash(b *testing.B) {
	registry := newFileRegistry()
	u, _ := url.Parse("https://example.invalid/assets/app.js")
	body := []byte("same static content")
	candidate := filepath.Join("static", "js", "app.js")
	for i := 0; i < b.N; i++ {
		registry.choose(candidate, u, body)
	}
}

func TestResumeReusesEarlierRenamedAsset(t *testing.T) {
	var available atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script src="/one/app.js"></script><script src="/two/app.js"></script><script src="/three/app.js"></script>`)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		switch r.URL.Path {
		case "/one/app.js":
			fmt.Fprint(w, "one")
		case "/two/app.js":
			fmt.Fprint(w, "two")
		case "/three/app.js":
			if !available.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, "two")
		}
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.Concurrency = 1
	cfg.Retries = 0
	first, err := Run(context.Background(), cfg, nil)
	if err != nil || first.Failed != 1 || !first.Resumable {
		t.Fatalf("first run: %+v %v", first, err)
	}
	available.Store(true)
	second, err := Resume(context.Background(), first.Directory, "", nil)
	if err != nil || second.Failed != 0 {
		t.Fatalf("resume: %+v %v", second, err)
	}
	entries, err := os.ReadDir(filepath.Join(second.Directory, "static", "js"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected JS files after resume: %v, %v", entries, err)
	}
	var refs []string
	for _, tag := range tags(readText(t, filepath.Join(second.Directory, second.Entry))) {
		for _, a := range tag.attrs {
			if a.name == "src" && strings.HasSuffix(a.value, ".js") {
				refs = append(refs, a.value)
			}
		}
	}
	if len(refs) != 3 || refs[0] == refs[1] || refs[1] != refs[2] {
		t.Fatalf("resumed JS references: %v", refs)
	}
}

func TestPreviewAndZIP(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>Local</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := StartPreview(root, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	resp, err := http.Get(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Local") {
		t.Fatalf("preview: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'none'") {
		t.Fatal("form guard missing")
	}
	req, _ := http.NewRequest(http.MethodGet, p.URL, nil)
	req.Host = "wrong.invalid"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("host check missing")
	}
	u := parseURL(t, p.URL)
	u.Path = "/wrong/index.html"
	resp, err = http.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("prefix check missing")
	}
	archive := filepath.Join(t.TempDir(), "clone.zip")
	if err = ExportZIP(root, archive); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 1 || z.File[0].Name != "index.html" {
		t.Fatal("invalid archive")
	}
	if err = ExportZIP(root, archive); err == nil {
		t.Fatal("existing archive overwritten")
	}
	if err = ExportZIP(root, filepath.Join(root, "inside.zip")); err == nil {
		t.Fatal("archive allowed inside source")
	}
}

func TestFileLimitKeepsRemoteFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large.bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, strings.Repeat("x", 1024*1024+1))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<img src="/large.bin">`)
	}))
	defer srv.Close()
	c := testConfig(t, srv.URL)
	c.MaxFileMB = 1
	r, err := Run(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Failed != 1 || r.Assets != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(readText(t, filepath.Join(r.Directory, r.Entry)), srv.URL+"/large.bin") {
		t.Fatal("missing fallback")
	}
}

func TestConcurrentDownloadAndProgress(t *testing.T) {
	var peak, current atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			for i := 0; i < 12; i++ {
				fmt.Fprintf(w, `<img src="/img%d.png">`, i)
			}
			return
		}
		n := current.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		current.Add(-1)
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, r.URL.Path)
	}))
	defer srv.Close()
	c := testConfig(t, srv.URL)
	c.Concurrency = 6
	var last Event
	r, err := Run(context.Background(), c, func(e Event) { last = e })
	if err != nil {
		t.Fatal(err)
	}
	if r.Pages != 1 || r.Assets != 12 || r.Resumable {
		t.Fatalf("unexpected result %+v", r)
	}
	if peak.Load() < 2 {
		t.Fatalf("downloads were not concurrent, peak %d", peak.Load())
	}
	if last.Total != 13 || last.Done != 13 {
		t.Fatalf("progress incomplete: %+v", last)
	}
}

func TestRetryTransientFailure(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flaky.png" {
			if attempts.Add(1) <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "png")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<img src="/flaky.png">`)
	}))
	defer srv.Close()
	c := testConfig(t, srv.URL)
	c.Retries = 2
	r, err := Run(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Assets != 1 || r.Failed != 0 || r.Retries != 2 {
		t.Fatalf("retry not applied: %+v", r)
	}
}

func TestResumeRetriesFailedItems(t *testing.T) {
	var broken atomic.Bool
	broken.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a.png" {
			if broken.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "png")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<img src="/a.png">`)
	}))
	defer srv.Close()
	c := testConfig(t, srv.URL)
	c.Retries = 0
	first, err := Run(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Failed != 1 || !first.Resumable || !CanResume(first.Directory) {
		t.Fatalf("expected resumable failure: %+v", first)
	}
	broken.Store(false)
	second, err := Resume(context.Background(), first.Directory, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Directory != first.Directory || second.Failed != 0 || second.Assets != 1 || second.Pages != 1 || second.Resumable {
		t.Fatalf("resume result: %+v", second)
	}
	page := readText(t, filepath.Join(second.Directory, second.Entry))
	if strings.Contains(page, srv.URL+"/a.png") || !strings.Contains(page, "static/picture/a.png") {
		t.Fatal("resumed asset not localized: " + page)
	}
	archive := filepath.Join(t.TempDir(), "resume.zip")
	if err = ExportZIP(second.Directory, archive); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, stateDir) {
			t.Fatal("state directory exported")
		}
	}
}

func TestEncodingConversionToUTF8(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().String(`<html><head><meta charset="gbk"><title>中文标题</title></head><body>你好</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, gbk)
	}))
	defer srv.Close()
	c := testConfig(t, srv.URL)
	keep, err := Run(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readText(t, filepath.Join(keep.Directory, keep.Entry)), "中文标题") {
		t.Fatal("original encoding should be kept by default")
	}
	c.Encoding = "utf-8"
	r, err := Run(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	page := readText(t, filepath.Join(r.Directory, r.Entry))
	if !strings.Contains(page, "中文标题") || !strings.Contains(page, `charset="utf-8"`) || r.Converted != 1 {
		t.Fatalf("not converted (%d): %s", r.Converted, page)
	}
}

func FuzzMarkupDoesNotPanic(f *testing.F) {
	for _, s := range []string{"<", `<img src="x">`, `<script>'<img>'</script>`, `İ<style>a{background:url(x)}</style>`, `<a href=x/y/>`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 65536 {
			t.Skip()
		}
		processHTML(s, func(s string, _ bool) string { return s })
		processCSS(s, func(s string) string { return s })
	})
}
