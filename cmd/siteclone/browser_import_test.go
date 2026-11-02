package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestBrowserImportReadsOnlyTargetURLCookies(t *testing.T) {
	var cookieMethod string
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var message struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(data, &message) != nil {
				continue
			}
			result := any(map[string]any{})
			switch message.Method {
			case "Runtime.evaluate":
				result = map[string]any{"result": map[string]any{"value": "Browser UA"}}
			case "Network.getCookies":
				cookieMethod = message.Method
				urls, _ := message.Params["urls"].([]any)
				if len(urls) != 1 || urls[0] != "https://www.example.com/account" {
					t.Errorf("cookie URL scope: %v", urls)
				}
				result = map[string]any{"cookies": []map[string]any{{"name": "path", "value": "one", "domain": "www.example.com", "path": "/account"}, {"name": "root", "value": "two", "domain": ".example.com", "path": "/"}, {"name": "bad", "value": "x;y", "domain": "example.com", "path": "/"}}}
			}
			payload, _ := json.Marshal(map[string]any{"id": message.ID, "result": result})
			_ = conn.Write(r.Context(), websocket.MessageText, payload)
		}
	}))
	defer ws.Close()
	wsURL := "ws" + strings.TrimPrefix(ws.URL, "http")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]cdpTarget{{Type: "page", URL: "https://evil.invalid/", WebSocketDebuggerURL: wsURL}, {Type: "page", URL: "https://login.example.com/", WebSocketDebuggerURL: wsURL}})
	}))
	defer api.Close()
	s := &browserImport{baseURL: api.URL, target: "https://www.example.com/account"}
	cookie, ua, count, err := s.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "path=one; root=two" || ua != "Browser UA" || count != 2 || cookieMethod != "Network.getCookies" {
		t.Fatalf("%q %q %d %q", cookie, ua, count, cookieMethod)
	}
}

func TestBrowserImportRejectsUnrelatedOpenPages(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]cdpTarget{{Type: "page", URL: "https://unrelated.invalid/", WebSocketDebuggerURL: "ws://127.0.0.1:1"}})
	}))
	defer api.Close()
	s := &browserImport{baseURL: api.URL, target: "https://www.example.com/"}
	if _, _, _, err := s.read(context.Background()); err == nil {
		t.Fatal("unrelated page accepted")
	}
}
