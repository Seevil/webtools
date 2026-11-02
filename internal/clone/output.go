package clone

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Preview struct {
	URL    string
	server *http.Server
}

func (p *Preview) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return p.server.Shutdown(ctx)
}

// Preview has no control API, binds only to loopback, and uses a random URL prefix.
func StartPreview(root, entry string) (*Preview, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if entry == "" {
		return nil, errors.New("没有可预览的起始页面")
	}
	key := make([]byte, 24)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	prefix := "/" + hex.EncodeToString(key) + "/"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	host := listener.Addr().String()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rel := filepath.FromSlash(strings.TrimPrefix(r.URL.Path, prefix))
		if !filepath.IsLocal(rel) || strings.HasPrefix(filepath.ToSlash(rel), stateDir+"/") {
			http.NotFound(w, r)
			return
		}
		file := filepath.Join(root, rel)
		resolved, e := filepath.EvalSymlinks(file)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		realRoot, e := filepath.EvalSymlinks(root)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		check, e := filepath.Rel(realRoot, resolved)
		if e != nil || !filepath.IsLocal(check) {
			http.NotFound(w, r)
			return
		}
		info, e := os.Stat(resolved)
		if e != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "form-action 'none'; frame-ancestors 'self'")
		f, e := os.Open(resolved)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 20 * time.Second}
	go server.Serve(listener)
	return &Preview{URL: "http://" + host + prefix + (&url.URL{Path: filepath.ToSlash(entry)}).String(), server: server}, nil
}

func ExportZIP(root, destination string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if rel, e := filepath.Rel(root, destination); e == nil && filepath.IsLocal(rel) {
		return errors.New("ZIP 必须保存到任务目录之外")
	}
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	walkErr := filepath.WalkDir(root, func(file string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if file != root && d.Name() == stateDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("导出目录包含符号链接，已停止")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		header, e := zip.FileInfoHeader(info)
		if e != nil {
			return e
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate
		w, e := z.CreateHeader(header)
		if e != nil {
			return e
		}
		in, e := os.Open(file)
		if e != nil {
			return e
		}
		_, copyErr := io.Copy(w, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	zipErr := z.Close()
	closeErr := f.Close()
	if walkErr != nil {
		return walkErr
	}
	if zipErr != nil {
		return zipErr
	}
	return closeErr
}
