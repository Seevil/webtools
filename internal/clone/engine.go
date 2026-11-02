package clone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type Event struct {
	Message                        string
	Pages, Assets, Failed, Pending int
	Active, Retries                int
	Done, Total                    int
	Bytes                          int64
	Elapsed                        time.Duration
}

type Result struct {
	Directory string   `json:"directory"`
	Entry     string   `json:"entry"`
	Pages     int      `json:"pages"`
	Assets    int      `json:"assets"`
	Failed    int      `json:"failed"`
	Retries   int      `json:"retries"`
	Converted int      `json:"converted"`
	Bytes     int64    `json:"bytes"`
	Cancelled bool     `json:"cancelled"`
	Resumable bool     `json:"resumable"`
	Warnings  []string `json:"warnings"`
}

type item struct {
	u     *url.URL
	depth int
	page  bool
}
type saved struct {
	u, base                      *url.URL
	local, kind, source, variant string
	hash, localHash              [32]byte
	page                         bool
	depth                        int
}

func safeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s = strings.Trim(b.String(), " .")
	if s == "" {
		s = "_"
	}
	upper := strings.ToUpper(strings.Split(s, ".")[0])
	reserved := upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL"
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
		reserved = true
	}
	if reserved {
		s = "_" + s
	}
	if len(s) > 120 {
		h := sha256.Sum256([]byte(s))
		ext := filepath.Ext(s)
		if len(ext) > 16 {
			ext = ""
		}
		stem := strings.TrimSuffix(s, filepath.Ext(s))
		if len(stem) > 80 {
			stem = stem[:80]
		}
		s = strings.ToValidUTF8(stem, "_") + "-" + hex.EncodeToString(h[:8]) + ext
	}
	return s
}

func pagePath(u *url.URL) string {
	name := safeSegment(path.Base(u.Path))
	if name == "_" || name == "." || path.Ext(name) == "" {
		if strings.HasSuffix(u.Path, "/") || u.Path == "" || u.Path == "/" {
			name = "index.html"
		} else {
			p := path.Base(u.Path)
			if p == "." || p == "/" || p == "" {
				name = "index.html"
			} else {
				name = safeSegment(p) + ".html"
			}
		}
	}
	ext := strings.ToLower(path.Ext(name))
	if ext != ".html" && ext != ".htm" {
		name = strings.TrimSuffix(name, path.Ext(name)) + ".html"
	}
	return name
}

func sourceCandidate(u *url.URL, kind string, page bool, paths FilePaths, start *url.URL) string {
	candidate := ""
	if page {
		candidate = pagePath(u)
	} else {
		candidate = assetPath(u, kind, paths)
	}
	if !sameOrigin(u, start) {
		candidate = filepath.Join("external", safeSegment(u.Host), candidate)
	}
	return candidate
}

func assetPath(u *url.URL, kind string, paths FilePaths) string {
	name := safeSegment(path.Base(u.Path))
	if name == "_" {
		name = "index"
	}
	ext := strings.ToLower(path.Ext(name))
	prefix := paths.File
	switch {
	case kind == "text/css" || ext == ".css":
		prefix = paths.CSS
	case kind == "text/javascript" || kind == "application/javascript" || ext == ".js" || ext == ".mjs":
		prefix = paths.JS
	case strings.HasPrefix(kind, "font/") || ext == ".woff" || ext == ".woff2" || ext == ".ttf" || ext == ".otf":
		prefix = paths.Font
	case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".avif":
		prefix = paths.Picture
	case strings.HasPrefix(kind, "image/"):
		prefix = paths.Image
	}
	return filepath.Join(filepath.FromSlash(strings.Trim(prefix, "/")), name)
}

type fileOwner struct {
	url     string
	hash    [32]byte
	variant string
}

type fileRegistry struct {
	used     map[string]fileOwner
	byHash   map[string]string
	occupied map[string]bool // Existing files are protected until a saved record claims them.
}

func newFileRegistry() *fileRegistry {
	return &fileRegistry{used: map[string]fileOwner{}, byHash: map[string]string{}, occupied: map[string]bool{}}
}

func (r *fileRegistry) protectExisting(root string) error {
	return filepath.Walk(root, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("输出目录树不能包含符号链接")
		}
		if info.IsDir() {
			if file != root && info.Name() == stateDir {
				return filepath.SkipDir
			}
			rel, e := filepath.Rel(root, file)
			if e != nil || (file != root && !filepath.IsLocal(rel)) {
				return errors.New("输出目录包含无效路径")
			}
			if file != root {
				r.occupied[pathKey(rel)] = true
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("输出目录包含非普通文件")
		}
		rel, e := filepath.Rel(root, file)
		if e != nil || !filepath.IsLocal(rel) {
			return errors.New("输出目录包含无效文件路径")
		}
		r.occupied[pathKey(rel)] = true
		return nil
	})
}

func pathKey(s string) string { return strings.ToLower(filepath.ToSlash(s)) }

func safeLocalPath(root, local string) (string, error) {
	if !filepath.IsLocal(local) || local == "." {
		return "", errors.New("输出路径无效")
	}
	clean := filepath.Clean(local)
	full := filepath.Join(root, clean)
	cur := root
	parts := strings.Split(clean, string(filepath.Separator))
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("输出路径不能经过符号链接")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", errors.New("输出路径父级不是目录")
		}
	}
	return full, nil
}

func contentKey(candidate string, hash [32]byte, variant string) string {
	// Keep the digest binary in the lookup key: this avoids a hex allocation on
	// every candidate while retaining candidate-path and CSS-base identity.
	return pathKey(candidate) + "\x00" + string(hash[:]) + "\x00" + variant
}

func cssBaseKey(base *url.URL, body []byte) string {
	var refs []string
	processCSS(string(body), func(raw string) string {
		if u := resolve(base, raw); u != nil {
			refs = append(refs, u.String())
		}
		return raw
	})
	return strings.Join(refs, "\x00")
}

// Equal bytes at the same original path reuse the first file, even if the first
// collision was already renamed. Different content keeps a separate file.
func (r *fileRegistry) reserve(candidate, local string, owner fileOwner) {
	r.used[pathKey(local)] = owner
	delete(r.occupied, pathKey(local))
	key := contentKey(candidate, owner.hash, owner.variant)
	if _, exists := r.byHash[key]; !exists {
		r.byHash[key] = local
	}
}

func (r *fileRegistry) choose(candidate string, u *url.URL, body []byte, variant ...string) (string, bool) {
	return r.chooseHash(candidate, u, body, sha256.Sum256(body), variant...)
}

func (r *fileRegistry) chooseHash(candidate string, u *url.URL, body []byte, currentHash [32]byte, variant ...string) (string, bool) {
	v := ""
	if len(variant) > 0 {
		v = variant[0]
	}
	if local, ok := r.byHash[contentKey(candidate, currentHash, v)]; ok {
		return local, true
	}
	owner := fileOwner{url: u.String(), hash: currentHash, variant: v}
	key := pathKey(candidate)
	if _, exists := r.used[key]; !exists && !r.occupied[key] {
		r.reserve(candidate, candidate, owner)
		return candidate, false
	}
	h := sha256.Sum256([]byte(u.String()))
	ext := filepath.Ext(candidate)
	stem := strings.TrimSuffix(candidate, ext)
	digest := hex.EncodeToString(h[:])
	for i := 10; i <= len(digest); i += 2 {
		proposed := stem + "-" + digest[:i] + ext
		if _, exists := r.used[pathKey(proposed)]; !exists && !r.occupied[pathKey(proposed)] {
			r.reserve(candidate, proposed, owner)
			return proposed, false
		}
	}
	for i := 2; ; i++ {
		proposed := fmt.Sprintf("%s-%s-%d%s", stem, digest, i, ext)
		if _, exists := r.used[pathKey(proposed)]; !exists && !r.occupied[pathKey(proposed)] {
			r.reserve(candidate, proposed, owner)
			return proposed, false
		}
	}
}

func displayURL(u *url.URL) string { return u.Scheme + "://" + u.Host + u.EscapedPath() }

var errRedirectPolicy = errors.New("重定向超出限定范围或次数超限")

type fetched struct {
	it       item
	body     []byte
	ctype    string
	final    *url.URL
	status   int
	err      error
	retries  int
	bytes    int64
	tooLarge bool
}

func retryDelay(attempt int) time.Duration {
	d := 500 * time.Millisecond << (attempt - 1)
	if d <= 0 || d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Run starts a new task in a fresh directory named after the site domain.
func Run(ctx context.Context, cfg Config, emit func(Event)) (Result, error) {
	start, err := cfg.Validate()
	if err != nil {
		return Result{}, err
	}
	if !cfg.pageAllowed(start, start) {
		return Result{}, errors.New("起始网址不在限定域名或路径范围内")
	}
	if _, err = cfg.EffectiveFilePaths(); err != nil {
		return Result{}, err
	}
	root, err := newOutput(cfg.Output, start.Hostname())
	if err != nil {
		return Result{}, fmt.Errorf("创建输出目录失败：%w", err)
	}
	return execute(ctx, cfg, start, root, nil, emit)
}

// Resume continues an unfinished task in its original directory, retrying failed and pending items.
// Cookie is never stored, so it must be supplied again when the site requires it.
func Resume(ctx context.Context, root, cookie string, emit func(Event)) (Result, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	st, err := loadState(root)
	if err != nil {
		return Result{Directory: root}, err
	}
	cfg := st.Config
	cfg.Cookie = cookie
	start, err := cfg.Validate()
	if err != nil {
		return Result{Directory: root}, err
	}
	return execute(ctx, cfg, start, root, &st, emit)
}

func execute(ctx context.Context, cfg Config, start *url.URL, root string, st *taskState, emit func(Event)) (Result, error) {
	paths, err := cfg.EffectiveFilePaths()
	if err != nil {
		return Result{Directory: root}, err
	}
	convert := strings.EqualFold(strings.TrimSpace(cfg.Encoding), "utf-8")
	result := Result{Directory: root, Warnings: []string{
		"仅克隆可通过 HTTP GET 下载的前端内容，不执行 JavaScript，不复制服务端业务。",
		"动态接口、ES 模块导入、Service Worker、CSS 转义 URL 及含 data: 的 srcset 未解析，可能仍需原站。",
		"已禁用 HTML 表单 action、自动跳转及 meta CSP；原有脚本仍可能联网。仅预览可信站点。",
		"同名但内容不同的文件会添加区分后缀，并同步重写页面引用。",
	}}
	if convert {
		result.Warnings = append(result.Warnings, "HTML/CSS 已按声明编码转换为 UTF-8；未声明编码的非 UTF-8 文件保持原样。")
	}
	registry := newFileRegistry()
	if err := registry.protectExisting(root); err != nil {
		return result, fmt.Errorf("检查输出目录失败：%w", err)
	}
	registry.reserve("result.json", "result.json", fileOwner{url: "manifest"})
	success := map[string]saved{}
	seen := map[string]bool{}
	fresh := map[string]bool{}
	localRedirect := map[string]string{}
	staleLocalByURL := map[string]string{}
	finalHashes := map[string][32]byte{}
	legacySourceByURL := map[string]string{}
	var queue, failed []item
	pageCount, assetCount, skipped := 1, 0, 0
	if st == nil {
		queue = []item{{start, 0, true}}
		seen[start.String()] = true
	} else {
		result.Entry, result.Bytes = st.Entry, st.Bytes
		pageCount, assetCount, skipped = st.PageCount, st.AssetCount, st.Skipped
		counted := map[string]bool{}
		for _, prior := range st.Records {
			if prior.URL != "" && prior.Source != "" && filepath.IsLocal(filepath.FromSlash(prior.Source)) {
				legacySourceByURL[prior.URL] = filepath.FromSlash(prior.Source)
			}
		}
		localHashes := map[string][32]byte{}
		localMissing := map[string]bool{}
		queued := map[string]bool{}
		for key, r := range st.Records {
			record, ok := r.restore()
			if !ok {
				if u, parseErr := url.Parse(r.URL); parseErr == nil && u.Scheme != "" && u.Host != "" {
					u = canonical(u)
					delete(seen, u.String())
					if !queued[u.String()] {
						queue = append(queue, item{u: u, depth: r.Depth, page: r.Page})
						queued[u.String()] = true
					}
				}
				continue
			}
			localHash, cached := localHashes[pathKey(record.local)]
			var readErr error
			if !cached && !localMissing[pathKey(record.local)] {
				var localBody []byte
				localBody, readErr = os.ReadFile(filepath.Join(root, record.local))
				if readErr == nil {
					localHash = sha256.Sum256(localBody)
					localHashes[pathKey(record.local)] = localHash
				} else {
					localMissing[pathKey(record.local)] = true
				}
			}
			if readErr != nil || localMissing[pathKey(record.local)] {
				delete(seen, record.u.String())
				staleLocalByURL[record.u.String()] = record.local
				if !queued[record.u.String()] {
					queue = append(queue, item{u: record.u, depth: record.depth, page: record.page})
					queued[record.u.String()] = true
				}
				continue
			}
			// LocalHash was added after the original state format. For old
			// records, only trust files whose bytes were not localized in-place.
			if r.LocalHash != "" {
				if localHash != record.localHash {
					delete(seen, record.u.String())
					if !queued[record.u.String()] {
						queue = append(queue, item{u: record.u, depth: record.depth, page: record.page})
						queued[record.u.String()] = true
					}
					staleLocalByURL[record.u.String()] = record.local
					continue
				}
			} else if localHash != record.hash {
				delete(seen, record.u.String())
				staleLocalByURL[record.u.String()] = record.local
				if !queued[record.u.String()] {
					queue = append(queue, item{u: record.u, depth: record.depth, page: record.page})
					queued[record.u.String()] = true
				}
				continue
			}
			record.localHash = localHash
			if record.source == "" {
				record.source = sourceCandidate(record.u, record.kind, record.page && (record.kind == "text/html" || record.kind == "application/xhtml+xml"), paths, start)
				if record.kind == "text/css" || strings.EqualFold(path.Ext(record.u.Path), ".css") {
					// Only unchanged old CSS is trusted here, so its current bytes still describe original references.
					if data, err := os.ReadFile(filepath.Join(root, record.local)); err == nil {
						record.variant = cssBaseKey(record.base, data)
					}
				}
			}
			success[key] = record
			if !counted[record.u.String()] {
				registry.reserve(record.source, record.local, fileOwner{url: record.u.String(), hash: record.hash, variant: record.variant})
				counted[record.u.String()] = true
				if record.page {
					result.Pages++
				} else {
					result.Assets++
				}
			}
		}
		for _, s := range st.Seen {
			seen[s] = true
		}
		for _, p := range st.Pending {
			u, e := url.Parse(p.URL)
			if e != nil {
				continue
			}
			u = canonical(u)
			if queued[u.String()] {
				continue
			}
			queue = append(queue, item{u, p.Depth, p.Page})
			seen[u.String()] = true
			queued[u.String()] = true
		}
	}

	began := time.Now()
	active := 0
	progress := func(message string) {
		if emit == nil {
			return
		}
		emit(Event{Message: message, Pages: result.Pages, Assets: result.Assets, Failed: result.Failed,
			Pending: len(queue), Active: active, Retries: result.Retries,
			Done: result.Pages + result.Assets + result.Failed + skipped, Total: len(seen),
			Bytes: result.Bytes, Elapsed: time.Since(began)})
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = cfg.Concurrency
	defer transport.CloseIdleConnections()
	workCtx, stopWork := context.WithCancel(ctx)
	defer stopWork()

	allowedAsset := func(u *url.URL) bool {
		for _, p := range fields(cfg.Exclude) {
			if strings.HasPrefix(u.Path, p) {
				return false
			}
		}
		if cfg.ExternalAssets {
			return true
		}
		return strings.EqualFold(u.Hostname(), start.Hostname())
	}
	enqueue := func(base *url.URL, raw string, page bool, depth int) string {
		u := resolve(base, raw)
		if u == nil || seen[u.String()] {
			return raw
		}
		if page {
			if depth > cfg.MaxDepth || pageCount >= cfg.MaxPages || !cfg.pageAllowed(u, start) {
				return raw
			}
			pageCount++
		} else {
			if assetCount >= cfg.MaxAssets || !allowedAsset(u) {
				return raw
			}
			assetCount++
		}
		seen[u.String()] = true
		queue = append(queue, item{u, depth, page})
		return raw
	}

	// fetchOnce performs one request; it reports whether the failure is worth retrying.
	fetchOnce := func(it item, limit int64, f *fetched) bool {
		client := &http.Client{Transport: transport, Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 8 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
					return errRedirectPolicy
				}
				if (it.page && !cfg.pageAllowed(req.URL, start)) || (!it.page && !allowedAsset(req.URL)) {
					return errRedirectPolicy
				}
				if !sameOrigin(req.URL, start) {
					req.Header.Del("Cookie")
					req.Header.Del("Referer")
				}
				return nil
			}}
		req, err := http.NewRequestWithContext(workCtx, http.MethodGet, it.u.String(), nil)
		if err != nil {
			f.err = err
			return false
		}
		req.Header.Set("User-Agent", cfg.UserAgent)
		if sameOrigin(it.u, start) {
			if cfg.Cookie != "" {
				req.Header.Set("Cookie", cfg.Cookie)
			}
			if cfg.Referer != "" {
				req.Header.Set("Referer", cfg.Referer)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			f.err = err
			return workCtx.Err() == nil && !errors.Is(err, errRedirectPolicy)
		}
		defer resp.Body.Close()
		f.status = resp.StatusCode
		f.ctype = resp.Header.Get("Content-Type")
		f.final = canonical(resp.Request.URL)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
			f.bytes += n
			s := resp.StatusCode
			return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || s >= 500
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		f.bytes += int64(len(body))
		if err != nil {
			f.err = err
			return workCtx.Err() == nil
		}
		if int64(len(body)) > limit {
			f.tooLarge = true
			return false
		}
		f.body = body
		return false
	}
	fetch := func(it item, limit int64) fetched {
		f := fetched{it: it}
		for attempt := 0; ; attempt++ {
			wait := time.Duration(cfg.DelayMillis) * time.Millisecond
			if attempt > 0 {
				wait = retryDelay(attempt)
			}
			if !sleepCtx(workCtx, wait) {
				f.err = context.Canceled
				return f
			}
			f = fetched{it: it, bytes: f.bytes, retries: f.retries}
			if !fetchOnce(it, limit, &f) || attempt >= cfg.Retries {
				return f
			}
			f.retries++
		}
	}

	// store runs only on the coordinator goroutine, which owns all crawl state.
	store := func(f fetched) error {
		it := f.it
		kind, _, _ := mime.ParseMediaType(f.ctype)
		if kind == "" {
			kind, _, _ = mime.ParseMediaType(http.DetectContentType(f.body))
		}
		isHTML := kind == "text/html" || kind == "application/xhtml+xml"
		isCSS := kind == "text/css" || strings.EqualFold(path.Ext(it.u.Path), ".css")
		if isHTML && !it.page {
			result.Failed++
			progress("资源请求返回 HTML（可能是登录或错误页），已跳过：" + displayURL(it.u))
			return nil
		}
		body := f.body
		if convert && (isHTML || isCSS) {
			if out, ok := toUTF8(body, f.ctype, isHTML); ok {
				body = out
				result.Converted++
			}
		}
		candidate := sourceCandidate(it.u, kind, isHTML && it.page, paths, start)
		if prior := legacySourceByURL[it.u.String()]; prior != "" {
			candidate = prior
		} else if st != nil {
			// Old state without Source is intentionally conservative: derive the
			// candidate from URL/type rather than treating a collision-suffixed
			// Local path as the source identity.
			candidate = sourceCandidate(it.u, kind, isHTML && it.page, paths, start)
		}
		variant := ""
		if isCSS {
			variant = cssBaseKey(f.final, body)
		}
		contentHash := sha256.Sum256(body)
		local, reused := registry.chooseHash(candidate, it.u, body, contentHash, variant)
		if old := staleLocalByURL[it.u.String()]; old != "" && local != old {
			localRedirect[pathKey(old)] = local
		}
		full, err := safeLocalPath(root, local)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			return err
		}
		if !reused {
			if err := os.WriteFile(full, body, 0600); err != nil {
				return err
			}
			fresh[local] = true
		}
		finalHashes[pathKey(local)] = contentHash
		base := f.final
		if isHTML {
			base = documentBase(string(body), base)
		}
		record := saved{u: it.u, base: base, local: local, kind: kind, source: candidate, variant: variant, hash: contentHash, localHash: contentHash, page: it.page, depth: it.depth}
		success[it.u.String()] = record
		if _, exists := success[f.final.String()]; !exists {
			success[f.final.String()] = record
		}
		if it.page {
			result.Pages++
		} else {
			result.Assets++
		}
		if it.u.String() == start.String() {
			result.Entry = filepath.ToSlash(local)
		}
		if isHTML {
			processHTML(string(body), func(raw string, page bool) string {
				d := it.depth
				if page {
					d++
				}
				return enqueue(base, raw, page, d)
			})
		} else if isCSS {
			processCSS(string(body), func(raw string) string { return enqueue(base, raw, false, it.depth) })
		}
		message := "已保存：" + displayURL(it.u)
		if reused {
			message = "已复用相同文件：" + displayURL(it.u)
		}
		if f.retries > 0 {
			message += fmt.Sprintf("（重试 %d 次）", f.retries)
		}
		progress(message)
		return nil
	}

	results := make(chan fetched, cfg.Concurrency)
	totalLimit := int64(cfg.MaxTotalMB) * 1024 * 1024
	fileLimit := int64(cfg.MaxFileMB) * 1024 * 1024
	capped := false
	if st == nil {
		progress(fmt.Sprintf("任务开始；并发 %d，失败重试 %d 次；Cookie 仅发送到起始同源站点", cfg.Concurrency, cfg.Retries))
	} else {
		progress(fmt.Sprintf("继续任务；待处理 %d 项（含上次失败项）", len(queue)))
	}
	for {
		for !capped && ctx.Err() == nil && active < cfg.Concurrency && len(queue) > 0 {
			it := queue[0]
			queue = queue[1:]
			if _, ok := success[it.u.String()]; ok {
				skipped++
				continue
			}
			remaining := totalLimit - result.Bytes
			if remaining <= 0 {
				capped = true
				queue = append([]item{it}, queue...)
				result.Warnings = append(result.Warnings, "已达到下载总量上限，剩余链接保留原地址，可在任务历史中续传")
				break
			}
			active++
			go func(it item, limit int64) { results <- fetch(it, limit) }(it, min(fileLimit, remaining))
		}
		if active == 0 {
			break
		}
		f := <-results
		active--
		result.Retries += f.retries
		result.Bytes += f.bytes
		switch {
		case f.err != nil && ctx.Err() != nil:
			queue = append(queue, f.it)
		case f.err != nil:
			result.Failed++
			failed = append(failed, f.it)
			progress("请求失败（网络、超时或重定向限制）：" + displayURL(f.it.u))
		case f.status < 200 || f.status >= 300 || f.tooLarge:
			result.Failed++
			failed = append(failed, f.it)
			progress(fmt.Sprintf("未保存（HTTP %d / 大小超限）：%s", f.status, displayURL(f.it.u)))
		default:
			if err := store(f); err != nil {
				return result, err
			}
		}
	}
	if ctx.Err() != nil {
		result.Cancelled = true
	}
	if result.Entry != "" {
		entryPath, entryErr := safeLocalPath(root, result.Entry)
		if entryErr != nil {
			result.Entry = ""
		} else if info, statErr := os.Stat(entryPath); statErr != nil || !info.Mode().IsRegular() {
			result.Entry = ""
		} else if entryRecord, ok := success[start.String()]; !ok || entryRecord.local != filepath.FromSlash(result.Entry) {
			result.Entry = ""
		} else if entryRecord.localHash != ([32]byte{}) {
			if data, readErr := os.ReadFile(entryPath); readErr != nil || sha256.Sum256(data) != entryRecord.localHash {
				result.Entry = ""
			}
		}
	}

	progress("正在本地化已下载资源；未下载链接保留远端地址")
	written := map[string]bool{}
	for _, record := range success {
		if written[record.local] {
			continue
		}
		written[record.local] = true
		isHTML := record.kind == "text/html" || record.kind == "application/xhtml+xml"
		if !isHTML && record.kind != "text/css" && !strings.EqualFold(path.Ext(record.u.Path), ".css") {
			continue
		}
		// Files from an earlier run were already localized; only their remaining absolute links may change.
		onlyAbsolute := !fresh[record.local]
		full, e := safeLocalPath(root, record.local)
		if e != nil {
			return result, e
		}
		b, e := os.ReadFile(full)
		if e != nil {
			if onlyAbsolute {
				continue
			}
			return result, e
		}
		rewrite := func(raw string) string {
			parsed, parseErr := url.Parse(strings.TrimSpace(raw))
			if parseErr != nil {
				return raw
			}
			if onlyAbsolute {
				if parsed.Scheme == "" && parsed.Host == "" && parsed.Path != "" {
					if strings.HasPrefix(parsed.Path, "/") || filepath.IsAbs(filepath.FromSlash(parsed.Path)) {
						return raw
					}
					joined := filepath.Clean(filepath.Join(filepath.Dir(record.local), filepath.FromSlash(parsed.Path)))
					if redirected := localRedirect[pathKey(joined)]; redirected != "" {
						rel, e := filepath.Rel(filepath.Dir(record.local), redirected)
						if e == nil {
							return (&url.URL{Path: filepath.ToSlash(rel), RawQuery: parsed.RawQuery, Fragment: parsed.Fragment}).String()
						}
					}
					return raw
				}
				if parsed.Scheme != "http" && parsed.Scheme != "https" {
					return raw
				}
			}
			u := resolve(record.base, raw)
			if u == nil {
				return raw
			}
			frag := ""
			if original, e := url.Parse(raw); e == nil && original.Fragment != "" {
				frag = "#" + original.EscapedFragment()
			}
			if target, ok := success[u.String()]; ok {
				if rel, e := filepath.Rel(filepath.Dir(record.local), target.local); e == nil {
					return (&url.URL{Path: filepath.ToSlash(rel)}).String() + frag
				}
			}

			if onlyAbsolute {
				return raw
			}
			return u.String() + frag
		}
		text := string(b)
		if isHTML {
			text = processHTML(text, func(raw string, _ bool) string { return rewrite(raw) })
		} else {
			text = processCSS(text, rewrite)
		}
		localHash := sha256.Sum256([]byte(text))
		if text != string(b) {
			if e = os.WriteFile(full, []byte(text), 0600); e != nil {
				return result, e
			}
		}
		finalHashes[pathKey(record.local)] = localHash
	}

	pending := append(append([]item{}, queue...), failed...)
	state := taskState{Config: cfg, Entry: result.Entry, Bytes: result.Bytes, PageCount: pageCount,
		AssetCount: assetCount, Skipped: skipped, Records: map[string]stateRecord{}}
	for key, r := range success {
		if h, ok := finalHashes[pathKey(r.local)]; ok {
			r.localHash = h
		}
		state.Records[key] = stateRecord{URL: r.u.String(), Base: r.base.String(), Local: filepath.ToSlash(r.local),
			Source: filepath.ToSlash(r.source), Variant: r.variant, Kind: r.kind, Hash: hex.EncodeToString(r.hash[:]),
			LocalHash: hex.EncodeToString(r.localHash[:]), Depth: r.depth, Page: r.page}
	}
	for _, it := range pending {
		state.Pending = append(state.Pending, stateItem{URL: it.u.String(), Depth: it.depth, Page: it.page})
	}
	for s := range seen {
		state.Seen = append(state.Seen, s)
	}
	result.Resumable = len(pending) > 0
	if err := saveState(root, state); err != nil {
		result.Resumable = false
		result.Warnings = append(result.Warnings, "续传状态保存失败："+err.Error())
	}

	manifest, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(root, "result.json"), manifest, 0600); err != nil {
		return result, err
	}
	if result.Cancelled {
		progress("任务已取消；已下载文件已保留，可在任务历史中续传")
		return result, context.Canceled
	}
	if result.Entry == "" {
		return result, errors.New("起始页面未成功下载；请检查网络、访问权限和限制设置")
	}
	progress("下载完成")
	return result, nil
}
