package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCommunityInt64(t *testing.T) {
	cases := []struct {
		name  string
		value interface{}
		want  int64
	}{
		{name: "int64", value: int64(42_000_000), want: 42_000_000},
		{name: "int", value: 123, want: 123},
		{name: "float64", value: float64(456), want: 456},
		{name: "json number", value: json.Number("789"), want: 789},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := communityInt64(tc.value); got != tc.want {
				t.Fatalf("communityInt64(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

func testCommunityUsage() *UsageResponse {
	return &UsageResponse{
		Summary: map[string]interface{}{"date": "2026-07-12", "total_tokens": int64(123)},
		ByTool:  map[string]*ToolStats{"Codex": {TotalTokens: 123}},
	}
}

func TestCommunityReportUsesRelayWithoutGit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"status":"synced","message":"匿名统计已同步","reported_at":"2026-07-12T08:00:00Z"}`))
	}))
	defer server.Close()
	t.Setenv("TOKEN_MONITOR_COMMUNITY_RELAY_URL", server.URL)

	result := reportCommunityStats(testCommunityUsage())
	if !result.OK || result.Status != "synced" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if received["id"] == "" || received["device_secret"] == "" || communityInt64(received["today_tokens"]) != 123 {
		t.Fatalf("unexpected relay payload: %#v", received)
	}
	if _, err := os.Stat(filepath.Join(home, ".token_monitor", "community_credential.json")); err != nil {
		t.Fatalf("credential was not persisted: %v", err)
	}
}

func TestCommunityLegacyIdentityRotatesAndRetries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	requests := []map[string]interface{}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		requests = append(requests, payload)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"ok":false,"status":"identity_upgrade_required","message":"升级"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"status":"synced","message":"已同步"}`))
	}))
	defer server.Close()
	t.Setenv("TOKEN_MONITOR_COMMUNITY_RELAY_URL", server.URL)

	result := reportCommunityStats(testCommunityUsage())
	if !result.OK || len(requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(requests))
	}
	if requests[0]["id"] == requests[1]["id"] || getUserID() != requests[1]["id"] {
		t.Fatalf("identity was not rotated: %#v", requests)
	}
}
