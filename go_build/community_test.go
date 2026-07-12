package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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

func TestNewCommunityIDIsAlwaysEightCharacters(t *testing.T) {
	if id := newCommunityID(); !regexp.MustCompile(`^User_[A-Z0-9]{8}$`).MatchString(id) {
		t.Fatalf("unexpected community id: %q", id)
	}
}

func TestLegacyOptOutIsMigratedToAutomaticMembership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".token_monitor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "community_optin.txt")
	if err := os.WriteFile(path, []byte("false"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isOptedIn() {
		t.Fatal("legacy opt-out still disabled automatic community membership")
	}
	setOptIn(false)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "true" {
		t.Fatalf("legacy opt-in file was not migrated: %q, %v", data, err)
	}
}

func TestDedupeLegacyIdentityReports(t *testing.T) {
	reports := []communityReportData{
		{ID: "User_OLD01", ReportDate: "2026-07-12", TodayTokens: 100, ByTool: map[string]int64{"Codex": 100}},
		{ID: "User_NEW0001", AuthHash: "hash", ReplacesID: "User_OLD01", ReportDate: "2026-07-12", TodayTokens: 200, ByTool: map[string]int64{"Codex": 200}},
		{ID: "User_OTHER", AuthHash: "other", ReportDate: "2026-07-12", TodayTokens: 10, ByTool: map[string]int64{"WorkBuddy": 10}},
	}
	got := dedupeLegacyIdentityReports(reports)
	if len(got) != 2 || got[0].ID != "User_NEW0001" || got[1].ID != "User_OTHER" {
		t.Fatalf("unexpected deduplicated reports: %#v", got)
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
	if requests[1]["replaces_id"] != requests[0]["id"] {
		t.Fatalf("migration relation missing: %#v", requests)
	}
}

func TestCommunityProfileUsesRelayCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"status":"updated","display_name":"鹏帅","next_change_at":"2026-07-19T08:00:00Z"}`))
	}))
	defer server.Close()
	t.Setenv("TOKEN_MONITOR_COMMUNITY_RELAY_URL", server.URL+"/v1/report")

	result := updateCommunityProfile("鹏帅")
	if !result.OK || result.DisplayName != "鹏帅" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if received["id"] == "" || received["device_secret"] == "" || received["display_name"] != "鹏帅" {
		t.Fatalf("unexpected payload: %#v", received)
	}
}
