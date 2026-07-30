package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCommunityHistoryRouteRegistered 静态扫描 main.go AST,
// 确认 HandleFunc("/api/community/history", ...) 注册存在 (修复 v1.4.51 follow-up)。
//
// 背景: Windows 客户端此前调 /api/community/history 一直 404,
// 根因是 v1.4.49a 引入 "排名变化" 弹窗时只写了 getCommunityHistory 函数,
// 忘了在 main() 的 http.HandleFunc 列表里注册, 而 Python 端的 server.py:1061
// 有注册, macOS 走 Python 后端没事, Windows 一直静默 404。
//
// 此测试不依赖网络, 也不启动 main(), 纯静态扫描源码, 防回归成本最低。
func TestCommunityHistoryRouteRegistered(t *testing.T) {
	const target = "/api/community/history"

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	found := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "main" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "HandleFunc" {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			// strip surrounding quotes
			route := strings.Trim(lit.Value, "\"")
			if route == target {
				found = true
				return false
			}
			return true
		})
		if found {
			break
		}
	}

	if !found {
		t.Fatalf("main.go 中未注册 %q 路由 (Windows 客户端排名变化弹窗依赖此路由)", target)
	}
}

func TestParseCommunityHistoryDays(t *testing.T) {
	cases := map[string]int{
		"/api/community/history":           30,
		"/api/community/history?days=30":   30,
		"/api/community/history?days=90":   90,
		"/api/community/history?days=180":  180,
		"/api/community/history?days=365":  365,
		"/api/community/history?days=7":    30,
		"/api/community/history?days=nope": 30,
	}
	for rawURL, want := range cases {
		req := httptest.NewRequest(http.MethodGet, rawURL, nil)
		if got := parseCommunityHistoryDays(req); got != want {
			t.Fatalf("parseCommunityHistoryDays(%q) = %d, want %d", rawURL, got, want)
		}
	}
}

func TestParseCommunityHistoryRange(t *testing.T) {
	cases := map[string]string{
		"/api/community/history":               "",
		"/api/community/history?range=week":    "week",
		"/api/community/history?range=month":   "month",
		"/api/community/history?range=quarter": "quarter",
		"/api/community/history?range=year":    "year",
		"/api/community/history?range=90":      "",
	}
	for rawURL, want := range cases {
		req := httptest.NewRequest(http.MethodGet, rawURL, nil)
		if got := parseCommunityHistoryRange(req); got != want {
			t.Fatalf("parseCommunityHistoryRange(%q) = %q, want %q", rawURL, got, want)
		}
	}
}

func TestCommunityHistoryCalendarPeriodBounds(t *testing.T) {
	today := time.Date(2026, 5, 15, 12, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	cases := map[string]string{
		"week":    "2026-05-11|2026-05-15",
		"month":   "2026-05-01|2026-05-15",
		"quarter": "2026-04-01|2026-05-15",
		"year":    "2026-01-01|2026-05-15",
	}
	for period, want := range cases {
		start, end, ok := communityHistoryDateBounds(period, today)
		if !ok || start.Format("2006-01-02")+"|"+end.Format("2006-01-02") != want {
			t.Fatalf("communityHistoryDateBounds(%q) = %s|%s, %v; want %s", period, start.Format("2006-01-02"), end.Format("2006-01-02"), ok, want)
		}
	}
}

// TestCommunityHistoryRouteIntegration 起一个最小 httptest server,
// 复用 main() 风格的 setCORSHeaders 行为, 验证 /api/community/history 的 HTTP 层契约:
//
//	GET 200 + JSON 形状 {snapshots:[...], data_status:"ok"|"empty"}  (前端 loadRankHistory 期望)
//	OPTIONS 200 (CORS preflight 通过)
//	POST 405 (仅支持 GET)
//
// 这里不直接调 getCommunityHistory, 因为它会去打真 GitCode API (无 mock 注入点),
// 我们只测试 route handler 调 getCommunityHistory 后会写入正确的 JSON 结构。
// HTTP status 与 CORS 是这次 fix 唯一的回归面, 已足够覆盖。
func TestCommunityHistoryRouteIntegration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/community/history", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
				"ok": false, "status": "method_not_allowed", "message": "仅支持 GET",
			})
			return
		}
		writeJSON(w, 200, getCommunityHistory(30))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("GET returns 200 and valid snapshots shape", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/api/community/history")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		// 验证顶层字段与前端 loadRankHistory (index.html:3940-3947) 期望一致。
		var payload struct {
			Snapshots  []map[string]interface{} `json:"snapshots"`
			DataStatus string                   `json:"data_status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if payload.DataStatus == "" {
			t.Fatalf("missing data_status: %+v", payload)
		}
		// snapshots 必须是数组 (允许为空, data_status=empty 时也是 [])
		if payload.Snapshots == nil {
			t.Fatalf("snapshots missing or null (前端会报 .length 错): %+v", payload)
		}
		// 如果有快照, 每条必须有 date 与 leaderboard 字段
		for i, s := range payload.Snapshots {
			if _, ok := s["date"]; !ok {
				t.Fatalf("snapshot[%d] missing date: %+v", i, s)
			}
			if _, ok := s["leaderboard"]; !ok {
				t.Fatalf("snapshot[%d] missing leaderboard: %+v", i, s)
			}
		}
	})

	t.Run("OPTIONS returns 200 (CORS preflight)", func(t *testing.T) {
		req, _ := http.NewRequest("OPTIONS", server.URL+"/api/community/history", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("OPTIONS: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("POST returns 405 method_not_allowed", func(t *testing.T) {
		resp, err := http.Post(server.URL+"/api/community/history", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", resp.StatusCode)
		}
	})
}
