package clone

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCorruptEntryFailedRepairDoesNotReportSuccess(t *testing.T) {
	var broken atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<h1>entry</h1>")
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.Retries = 0
	first, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Directory, first.Entry), []byte("user change"), 0600); err != nil {
		t.Fatal(err)
	}
	broken.Store(true)
	second, err := Resume(context.Background(), first.Directory, "", nil)
	if err == nil || second.Entry != "" || !second.Resumable {
		t.Fatalf("bad repair result: %+v %v", second, err)
	}
	if readText(t, filepath.Join(first.Directory, first.Entry)) != "user change" {
		t.Fatal("user change overwritten")
	}
}

func TestExistingFilesAndDirectoriesCannotBeOverwritten(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "static", "js", "app.js"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "static", "js", "other.js")
	if err := os.WriteFile(file, []byte("user content"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := newFileRegistry()
	if err := registry.protectExisting(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.js", "other.js"} {
		candidate := filepath.Join("static", "js", name)
		chosen, reused := registry.choose(candidate, parseURL(t, "https://example.invalid/"+name), []byte("download"))
		if chosen == candidate || reused {
			t.Fatalf("untracked path claimed: %s", chosen)
		}
	}
	if readText(t, file) != "user content" {
		t.Fatal("file overwritten")
	}
}

func TestResumeLegacyStateRestoresOriginalCollisionKey(t *testing.T) {
	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script src="/a/app.js"></script><script src="/b/app.js"></script><script src="/c/app.js"></script>`)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		if strings.HasPrefix(r.URL.Path, "/c/") && !up.Load() {
			w.WriteHeader(503)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/a/") {
			fmt.Fprint(w, "first")
			return
		}
		fmt.Fprint(w, "same")
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.Concurrency, cfg.Retries = 1, 0
	r, err := Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := loadState(r.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for key, record := range st.Records {
		record.Source = ""
		st.Records[key] = record
	}
	if err := saveState(r.Directory, st); err != nil {
		t.Fatal(err)
	}
	up.Store(true)
	if _, err := Resume(context.Background(), r.Directory, "", nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(r.Directory, "static", "js"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("legacy collision not reused: %v %v", entries, err)
	}
}
