package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"siteclone/internal/clone"
)

type browserCookie struct {
	Name, Value, Domain, Path string
	Expires                   float64
	HTTPOnly, Secure          bool
}

type browserImport struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	profile string
	baseURL string
	browser string
	target  string
	closed  bool
}

func browserExecutable(name string) (string, error) {
	var paths []string
	programFiles, programFilesX86, local := os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")
	if name == "Microsoft Edge" {
		paths = []string{filepath.Join(programFiles, "Microsoft", "Edge", "Application", "msedge.exe"), filepath.Join(programFilesX86, "Microsoft", "Edge", "Application", "msedge.exe")}
	} else {
		paths = []string{filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe"), filepath.Join(programFilesX86, "Google", "Chrome", "Application", "chrome.exe"), filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe")}
	}
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 %s，请先安装浏览器", name)
}

func startBrowserImport(ctx context.Context, browser, target string) (*browserImport, error) {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, errors.New("请先填写有效的 HTTP(S) 下载网址")
	}
	exe, err := browserExecutable(browser)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	profile, err := os.MkdirTemp("", "siteclone-browser-")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe,
		fmt.Sprintf("--remote-debugging-port=%d", port), "--remote-debugging-address=127.0.0.1",
		"--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check", "--disable-sync", "--new-window", u.String())
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return nil, err
	}
	s := &browserImport{cmd: cmd, profile: profile, baseURL: fmt.Sprintf("http://127.0.0.1:%d", port), browser: browser, target: u.String()}
	go func() { _ = cmd.Wait(); s.mu.Lock(); s.closed = true; s.mu.Unlock() }()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			s.Close()
			return nil, ctx.Err()
		default:
		}
		if _, err := s.version(ctx); err == nil {
			return s, nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	s.Close()
	return nil, errors.New("浏览器调试会话启动超时")
}

func (s *browserImport) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.closed && s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	s.closed = true
	profile := s.profile
	s.mu.Unlock()
	if profile != "" {
		_ = os.RemoveAll(profile)
	}
}

func (s *browserImport) version(ctx context.Context) (map[string]any, error) {
	return getJSON[map[string]any](ctx, s.baseURL+"/json/version")
}

func getJSON[T any](ctx context.Context, raw string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return zero, err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("浏览器调试接口返回 HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, 2<<20)
	var value T
	if err := json.NewDecoder(limited).Decode(&value); err != nil {
		return zero, err
	}
	return value, nil
}

type cdpTarget struct{ Type, URL, WebSocketDebuggerURL string }
type cdpMessage struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (s *browserImport) read(ctx context.Context) (string, string, int, error) {
	targets, err := getJSON[[]cdpTarget](ctx, s.baseURL+"/json/list")
	if err != nil {
		return "", "", 0, err
	}
	base := strings.TrimPrefix(clone.RootDomainWildcard(s.target), "*.")
	if base == "" {
		return "", "", 0, errors.New("无法识别目标根域名")
	}
	var endpoint string
	for _, target := range targets {
		u, e := url.Parse(target.URL)
		host := strings.ToLower(u.Hostname())
		if e == nil && target.Type == "page" && (u.Scheme == "http" || u.Scheme == "https") && (host == base || strings.HasSuffix(host, "."+base)) {
			endpoint = target.WebSocketDebuggerURL
			break
		}
	}
	if endpoint == "" {
		return "", "", 0, errors.New("浏览器中没有目标根域页面，请在独立会话中完成登录并保持目标站点页面打开")
	}
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return "", "", 0, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	var id atomic.Int32
	call := func(method string, params any, out any) error {
		n := int(id.Add(1))
		payload, _ := json.Marshal(map[string]any{"id": n, "method": method, "params": params})
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return err
		}
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return err
			}
			var message cdpMessage
			if json.Unmarshal(data, &message) != nil || message.ID != n {
				continue
			}
			if message.Error != nil {
				return errors.New(message.Error.Message)
			}
			return json.Unmarshal(message.Result, out)
		}
	}
	var eval struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := call("Runtime.evaluate", map[string]any{"expression": "navigator.userAgent", "returnByValue": true}, &eval); err != nil {
		return "", "", 0, err
	}
	var cookieResult struct {
		Cookies []browserCookie `json:"cookies"`
	}
	if err := call("Network.getCookies", map[string]any{"urls": []string{s.target}}, &cookieResult); err != nil {
		return "", "", 0, err
	}
	cookies := append([]browserCookie(nil), cookieResult.Cookies...)
	sort.SliceStable(cookies, func(i, j int) bool {
		if len(cookies[i].Path) != len(cookies[j].Path) {
			return len(cookies[i].Path) > len(cookies[j].Path)
		}
		return cookies[i].Name < cookies[j].Name
	})
	pairs := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" || strings.ContainsAny(cookie.Name+cookie.Value, "\r\n;") {
			continue
		}
		pairs = append(pairs, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(pairs, "; "), eval.Result.Value, len(pairs), nil
}
