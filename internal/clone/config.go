package clone

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type FilePaths struct {
	JS      string `json:"js"`
	CSS     string `json:"css"`
	Image   string `json:"image"`
	Picture string `json:"picture"`
	Font    string `json:"font"`
	File    string `json:"file"`
}

func DefaultFilePaths() FilePaths {
	return FilePaths{JS: "/static/js/", CSS: "/static/css/", Image: "/static/image/", Picture: "/static/picture/", Font: "/static/font/", File: "/static/file/"}
}

type Config struct {
	URL             string    `json:"url"`
	Output          string    `json:"output"`
	Domains         string    `json:"domains"`
	FilePaths       FilePaths `json:"filePaths"`
	Include         string    `json:"include"`
	Exclude         string    `json:"exclude"`
	MaxDepth        int       `json:"maxDepth"`
	MaxPages        int       `json:"maxPages"`
	MaxAssets       int       `json:"maxAssets"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	DelayMillis     int       `json:"delayMillis"`
	MaxFileMB       int       `json:"maxFileMB"`
	MaxTotalMB      int       `json:"maxTotalMB"`
	UserAgent       string    `json:"userAgent"`
	Referer         string    `json:"referer"`
	Cookie          string    `json:"-"`
	ExternalAssets  bool      `json:"externalAssets"`
	FilterLanguages bool      `json:"filterLanguages"`
	Concurrency     int       `json:"concurrency"`
	Retries         int       `json:"retries"`
	// Encoding is "" to keep the original bytes, or "utf-8" to convert HTML/CSS text.
	Encoding string `json:"encoding"`
}

const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

func defaultOutputDir() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		return filepath.Join(filepath.Dir(exe), "downloads")
	}
	return "downloads"
}

func DefaultConfig() Config {
	return Config{Output: defaultOutputDir(), FilePaths: DefaultFilePaths(), MaxDepth: 3, MaxPages: 500,
		MaxAssets: 1000, TimeoutSeconds: 30, DelayMillis: 200,
		MaxFileMB: 25, MaxTotalMB: 512, UserAgent: DefaultUserAgent, ExternalAssets: false,
		Concurrency: 4, Retries: 2}
}

func RootDomainWildcard(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	// Keep IPv4 / IPv6 addresses as-is without adding wildcard prefix
	if net.ParseIP(host) != nil {
		return host
	}
	parts := strings.Split(host, ".")
	if len(parts) <= 1 {
		return host
	}
	// Common multi-part ccTLDs
	twoPartTLDs := map[string]bool{
		"com.cn": true, "net.cn": true, "org.cn": true, "gov.cn": true, "edu.cn": true,
		"co.uk": true, "org.uk": true, "gov.uk": true, "ac.uk": true,
		"co.jp": true, "ne.jp": true, "or.jp": true,
		"com.hk": true, "org.hk": true, "edu.hk": true,
		"com.tw": true, "org.tw": true, "edu.tw": true,
		"com.au": true, "net.au": true, "org.au": true,
	}
	if len(parts) >= 3 {
		lastTwo := parts[len(parts)-2] + "." + parts[len(parts)-1]
		if twoPartTLDs[lastTwo] {
			root := strings.Join(parts[len(parts)-3:], ".")
			return "*." + root
		}
	}
	root := strings.Join(parts[len(parts)-2:], ".")
	return "*." + root
}

func SiteDomain(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func (p FilePaths) normalized() (FilePaths, error) {
	values := []*string{&p.JS, &p.CSS, &p.Image, &p.Picture, &p.Font, &p.File}
	for _, value := range values {
		v := strings.TrimSpace(*value)
		if v == "" {
			return p, errors.New("文件结构路径不能为空")
		}
		if strings.ContainsAny(v, "\\:?#%\x00\r\n") || !strings.HasPrefix(v, "/") {
			return p, fmt.Errorf("文件结构路径必须是以 / 开头的普通路径：%q", v)
		}
		if strings.Trim(v, "/") == "" {
			return p, errors.New("文件结构路径至少需要一个目录名")
		}
		parts := strings.Split(strings.Trim(v, "/"), "/")
		for _, segment := range parts {
			if segment == "" || segment == "." || segment == ".." || safeSegment(segment) != segment {
				return p, fmt.Errorf("文件结构路径包含无效目录名：%q", v)
			}
		}
		*value = "/" + path.Join(parts...) + "/"
	}
	return p, nil
}

func (c Config) EffectiveFilePaths() (FilePaths, error) {
	if c.FilePaths == (FilePaths{}) {
		return DefaultFilePaths(), nil
	}
	return c.FilePaths.normalized()
}

func (c Config) Validate() (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("请输入完整的 HTTP(S) 网址，且不得包含用户名或密码")
	}
	if strings.ContainsRune(c.URL+c.Output+c.Domains+c.Include+c.Exclude+c.Cookie+c.UserAgent+c.Referer, 0) {
		return nil, errors.New("输入不能包含 NUL 字符")
	}
	if strings.TrimSpace(c.Output) == "" {
		return nil, errors.New("请选择保存目录")
	}
	if _, err := c.EffectiveFilePaths(); err != nil {
		return nil, err
	}
	if c.MaxDepth < 0 || c.MaxDepth > 20 {
		return nil, errors.New("最大深度须在 0 到 20 之间")
	}
	if c.MaxPages < 1 || c.MaxPages > 10000 {
		return nil, errors.New("最大页数须在 1 到 10000 之间")
	}
	if c.MaxAssets < 1 || c.MaxAssets > 20000 {
		return nil, errors.New("资源上限须在 1 到 20000 之间")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 300 {
		return nil, errors.New("超时须在 1 到 300 秒之间")
	}
	if c.DelayMillis < 0 || c.DelayMillis > 60000 {
		return nil, errors.New("请求间隔须在 0 到 60000 毫秒之间")
	}
	if c.MaxFileMB < 1 || c.MaxFileMB > 256 || c.MaxTotalMB < 1 || c.MaxTotalMB > 4096 {
		return nil, errors.New("单文件上限须为 1–256 MB，总量须为 1–4096 MB")
	}
	if c.Concurrency < 1 || c.Concurrency > 32 {
		return nil, errors.New("并发数须在 1 到 32 之间")
	}
	if c.Retries < 0 || c.Retries > 10 {
		return nil, errors.New("失败重试须在 0 到 10 次之间")
	}
	if e := strings.ToLower(strings.TrimSpace(c.Encoding)); e != "" && e != "utf-8" {
		return nil, errors.New("编码转换仅支持保持原编码或转换为 UTF-8")
	}
	for _, value := range []string{c.Cookie, c.UserAgent, c.Referer} {
		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("请求头不能包含换行符")
		}
	}
	if c.Referer != "" {
		r, e := url.Parse(c.Referer)
		if e != nil || r.Host == "" || (r.Scheme != "http" && r.Scheme != "https") || r.User != nil {
			return nil, errors.New("来源网址必须是有效的 HTTP(S) 地址")
		}
	}
	for _, d := range fields(c.Domains) {
		rule := strings.ToLower(d)
		if strings.HasPrefix(rule, "*.") {
			rule = strings.TrimPrefix(rule, "*.")
		}
		if rule == "" || strings.ContainsAny(rule, "/:?#@*\\") || strings.HasPrefix(rule, ".") || strings.HasSuffix(rule, ".") || strings.Contains(rule, "..") || (net.ParseIP(rule) == nil && !validDomainName(rule)) {
			return nil, fmt.Errorf("域名规则无效：%s", d)
		}
	}
	for _, p := range append(fields(c.Include), fields(c.Exclude)...) {
		if !strings.HasPrefix(p, "/") {
			return nil, errors.New("限定/排除路径必须以 / 开头，多项用分号分隔")
		}
	}
	u.Fragment = ""
	return canonical(u), nil
}

func fields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' ' })
}

func validDomainName(s string) bool {
	if len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func canonical(u *url.URL) *url.URL {
	v := *u
	v.Fragment = ""
	v.Scheme = strings.ToLower(v.Scheme)
	v.Host = strings.ToLower(v.Host)
	if v.Path == "" {
		v.Path = "/"
	}
	return &v
}

func sameOrigin(a, b *url.URL) bool { return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host) }

func (c Config) pageAllowed(u, start *url.URL) bool {
	ok := strings.EqualFold(u.Hostname(), start.Hostname())
	if ds := fields(c.Domains); len(ds) > 0 {
		ok = false
		for _, d := range ds {
			d = strings.ToLower(d)
			h := strings.ToLower(u.Hostname())
			if strings.HasPrefix(d, "*.") {
				base := strings.TrimPrefix(d, "*.")
				ok = ok || h == base || strings.HasSuffix(h, "."+base)
			} else {
				ok = ok || h == d
			}
		}
	}
	if !ok || !c.languageAllowed(u, start) {
		return false
	}
	for _, p := range fields(c.Exclude) {
		if strings.HasPrefix(u.Path, p) {
			return false
		}
	}
	if ps := fields(c.Include); len(ps) > 0 {
		for _, p := range ps {
			if strings.HasPrefix(u.Path, p) {
				return true
			}
		}
		return false
	}
	return true
}

func SaveConfig(path string, c Config) error {
	if _, err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	info, err := os.Stat(path)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return c, errors.New("配置文件必须是小于 1 MB 的普通文件")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Domains == "" {
		c.Domains = RootDomainWildcard(c.URL)
	}
	if c.FilePaths == (FilePaths{}) {
		c.FilePaths = DefaultFilePaths()
	}
	_, err = c.Validate()
	return c, err
}

func newOutput(root, domain string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return "", err
	}
	name := safeSegment(domain)
	if name == "_" {
		return "", errors.New("输出文件夹域名无效")
	}
	for i := 0; i < 10000; i++ {
		folder := name
		if i > 0 {
			folder = fmt.Sprintf("%s-%d", name, i+1)
		}
		candidate := filepath.Join(abs, folder)
		err = os.Mkdir(candidate, 0700)
		if err == nil {
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", errors.New("无法创建不重复的输出文件夹")
}
