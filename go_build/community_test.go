package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"
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

func TestFormatCommunityToolsShowsAllToolsByUsage(t *testing.T) {
	if got := formatCommunityTools(map[string]int64{"Claude": 40, "Codex": 60}); got != "Codex + Claude" {
		t.Fatalf("formatCommunityTools() = %q, want %q", got, "Codex + Claude")
	}
	if got := formatCommunityTools(map[string]int64{"Claude": 10, "Codex": 10}); got != "Claude + Codex" {
		t.Fatalf("formatCommunityTools() tie = %q, want %q", got, "Claude + Codex")
	}
	if got := formatCommunityTools(map[string]int64{"Claude": 0}); got != "?" {
		t.Fatalf("formatCommunityTools() empty = %q, want %q", got, "?")
	}
}

func TestBuildCommunityRankSeriesCountsAllParticipantsAndLimitsTopTen(t *testing.T) {
	snapshots := []communityHistorySnapshot{}
	for _, date := range []string{"2026-07-26", "2026-07-27"} {
		entries := make([]communityHistoryEntry, 0, 12)
		for i := 0; i < 12; i++ {
			multiplier := int64(100)
			if date == "2026-07-27" {
				multiplier = 200
			}
			entries = append(entries, communityHistoryEntry{
				ID: "User_" + strconv.Itoa(i), DisplayName: "User " + strconv.Itoa(i), Tokens: int64(12-i) * multiplier,
			})
		}
		snapshots = append(snapshots, communityHistorySnapshot{Date: date, Participants: entries, Leaderboard: entries[:10]})
	}

	result := buildCommunityRankSeries(snapshots)
	series := result["series"].([]communityRankSeries)
	if result["participant_count"] != 12 || len(series) != 10 {
		t.Fatalf("participant_count=%v series=%d", result["participant_count"], len(series))
	}
	if result["participant_count_complete"] != true {
		t.Fatalf("expected complete participant count: %#v", result)
	}
	if series[0].ID != "User_0" || len(series[0].Ranks) != 2 || series[0].Ranks[0] != 1 || series[0].Ranks[1] != 1 {
		t.Fatalf("unexpected first series: %+v", series[0])
	}
	legacy := buildCommunityRankSeries([]communityHistorySnapshot{{Date: "2026-07-25", Leaderboard: snapshots[0].Leaderboard}})
	if legacy["participant_count_complete"] != false {
		t.Fatalf("legacy snapshots must be marked incomplete: %#v", legacy)
	}
}

func TestBuildCommunityRankSeriesKeepsZeroUsageDaysRankedByPriorHistory(t *testing.T) {
	snapshots := []communityHistorySnapshot{
		{Date: "2026-07-26", Participants: []communityHistoryEntry{
			{ID: "User_A", DisplayName: "A", Tokens: 1000},
			{ID: "User_B", DisplayName: "B", Tokens: 10},
		}},
		{Date: "2026-07-27", Participants: []communityHistoryEntry{
			{ID: "User_A", DisplayName: "A", Tokens: 1},
			{ID: "User_B", DisplayName: "B", Tokens: 20},
			{ID: "User_C", DisplayName: "C", Tokens: 2000},
		}},
		{Date: "2026-07-28", Participants: []communityHistoryEntry{}},
	}

	result := buildCommunityRankSeries(snapshots)
	series := result["series"].([]communityRankSeries)
	byID := map[string]communityRankSeries{}
	for _, item := range series {
		byID[item.ID] = item
	}
	wantRanks := map[string][]int{
		"User_A": {1, 3, 2},
		"User_B": {2, 2, 3},
		"User_C": {3, 1, 1},
	}
	wantTokens := map[string][]int64{
		"User_A": {1000, 1, 0},
		"User_B": {10, 20, 0},
		"User_C": {0, 2000, 0},
	}
	for id, want := range wantRanks {
		got := byID[id]
		if !reflect.DeepEqual(got.Ranks, want) || !reflect.DeepEqual(got.Tokens, wantTokens[id]) {
			t.Fatalf("%s ranks/tokens = %v/%v, want %v/%v", id, got.Ranks, got.Tokens, want, wantTokens[id])
		}
		for _, rank := range got.Ranks {
			if rank <= 0 {
				t.Fatalf("%s contains missing rank: %v", id, got.Ranks)
			}
		}
	}
}

func TestFetchCommunityHistorySnapshotsUsesBoundedConcurrencyAndKeepsOrder(t *testing.T) {
	files := make([]communityArchiveFile, 12)
	for i := range files {
		files[i] = communityArchiveFile{name: "2026-07-" + strconv.Itoa(i+1), url: strconv.Itoa(i + 1)}
	}
	active := 0
	peak := 0
	var mu sync.Mutex
	fetch := func(url string) (communityHistorySnapshot, error) {
		day, _ := strconv.Atoi(url)
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		if day == 6 {
			return communityHistorySnapshot{}, errors.New("temporary failure")
		}
		return communityHistorySnapshot{Date: "2026-07-" + strconv.Itoa(day)}, nil
	}

	snapshots := fetchCommunityHistorySnapshots(files, 8, fetch)
	if peak <= 1 || peak > 8 {
		t.Fatalf("unexpected peak concurrency: %d", peak)
	}
	if len(snapshots) != 11 {
		t.Fatalf("got %d snapshots, want 11", len(snapshots))
	}
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i-1].Date > snapshots[i].Date {
			t.Fatalf("snapshots are not date ordered: %#v", snapshots)
		}
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

func TestDedupeCommunityReportsByIDKeepsLatestReport(t *testing.T) {
	reports := []communityReportData{
		{ID: "User_TEST1", ReportDate: "2026-07-13", UpdatedAt: "2026-07-13T08:00:00Z", TodayTokens: 100},
		{ID: "User_TEST1", ReportDate: "2026-07-13", UpdatedAt: "2026-07-13T09:00:00Z", TodayTokens: 200},
		{ID: "User_OTHER", ReportDate: "2026-07-13", UpdatedAt: "2026-07-13T08:30:00Z", TodayTokens: 10},
	}
	got := dedupeCommunityReportsByID(reports)
	if len(got) != 2 || got[0].ID != "User_TEST1" || got[0].TodayTokens != 200 || got[1].ID != "User_OTHER" {
		t.Fatalf("unexpected ID-deduplicated reports: %#v", got)
	}
}

func TestActiveCommunityReportsExcludeZeroTokenStartupReports(t *testing.T) {
	reports := []communityReportData{
		{ID: "User_ACTIVE", TodayTokens: 123},
		{ID: "User_IDLE", TodayTokens: 0},
	}
	active := activeCommunityReports(reports)
	if len(active) != 1 || active[0].ID != "User_ACTIVE" {
		t.Fatalf("zero-token startup report entered active ranking: %#v", active)
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
	if err := writeCommunityCodeList("group_code.txt", []string{"12345", "67890"}); err != nil {
		t.Fatal(err)
	}
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
	groupCodes, ok := received["group_codes"].([]interface{})
	if !ok || len(groupCodes) != 2 || groupCodes[0] != "12345" || groupCodes[1] != "67890" {
		t.Fatalf("group codes missing from relay payload: %#v", received)
	}
	if _, err := os.Stat(filepath.Join(home, ".token_monitor", "community_credential.json")); err != nil {
		t.Fatalf("credential was not persisted: %v", err)
	}
}

func TestCommunityGroupJoinValidatesAndCachesRealGroup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/groups/12345" {
			_, _ = w.Write([]byte(`{"ok":true,"code":"12345","name":"跨平台组","created_by":"User_OWNER"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"ok":false,"status":"group_not_found","message":"组队不存在"}`))
	}))
	defer server.Close()
	t.Setenv("TOKEN_MONITOR_COMMUNITY_RELAY_URL", server.URL+"/v1/report")

	missing := addCommunityGroupCode("00000")
	if missing.OK || missing.Status != "group_not_found" || len(getCommunityGroupCodes()) != 0 {
		t.Fatalf("missing group was persisted: result=%+v codes=%v", missing, getCommunityGroupCodes())
	}
	if err := writeCommunityCodeList("group_code.txt", []string{"00000"}); err != nil {
		t.Fatal(err)
	}
	missing = addCommunityGroupCode("00000")
	if missing.OK || len(getCommunityGroupCodes()) != 0 {
		t.Fatalf("stale missing group was not cleaned: result=%+v codes=%v", missing, getCommunityGroupCodes())
	}
	joined := addCommunityGroupCode("12345")
	if !joined.OK || joined.Name != "跨平台组" || !reflect.DeepEqual(getCommunityGroupCodes(), []string{"12345"}) {
		t.Fatalf("valid group was not persisted: result=%+v codes=%v", joined, getCommunityGroupCodes())
	}
	if got := readCommunityGroupNames()["12345"]; got != "跨平台组" {
		t.Fatalf("group name cache = %q", got)
	}
	repeated := addCommunityGroupCode("12345")
	if !repeated.OK || !repeated.AlreadyMember {
		t.Fatalf("repeat join should be idempotent: %+v", repeated)
	}
}

func TestCommunityGroupJoinRejectsInvalidCodeWithoutNetwork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TOKEN_MONITOR_COMMUNITY_RELAY_URL", "http://127.0.0.1:1/v1/report")
	for _, code := range []string{"", "123", "ABCDE", "123456"} {
		result := addCommunityGroupCode(code)
		if result.OK || result.Status != "invalid_code" {
			t.Fatalf("code %q unexpectedly accepted: %+v", code, result)
		}
	}
	if len(getCommunityGroupCodes()) != 0 {
		t.Fatalf("invalid codes were persisted: %v", getCommunityGroupCodes())
	}
}

func TestCommunityGroupCreatePersistsCreatorMembership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/groups" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Device-ID") == "" || r.Header.Get("X-Device-Secret") == "" {
			t.Fatal("missing device credential headers")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"code":"54321","name":"我创建的组"}`))
	}))
	defer server.Close()
	t.Setenv("TOKEN_MONITOR_COMMUNITY_RELAY_URL", server.URL+"/v1/report")

	result := createCommunityGroup("我创建的组")
	if !result.OK || result.Code != "54321" || !reflect.DeepEqual(getCommunityGroupCodes(), []string{"54321"}) || !reflect.DeepEqual(getCreatedCommunityGroupCodes(), []string{"54321"}) {
		t.Fatalf("created group was not persisted: result=%+v groups=%v created=%v", result, getCommunityGroupCodes(), getCreatedCommunityGroupCodes())
	}
}

func TestBuildCommunityGroupViewsMatchesMacGroupSemantics(t *testing.T) {
	reports := []communityReportData{
		{ID: "User_OTHER", DisplayName: "领先者", TodayTokens: 500, GroupCodes: []string{"11111"}},
		{ID: "User_ME", DisplayName: "我", TodayTokens: 300, GroupCodes: []string{"11111", "22222"}},
		{ID: "User_B", DisplayName: "B", TodayTokens: 100, GroupCodes: []string{"22222"}},
	}
	groups, mine, ranks := buildCommunityGroupViews(
		reports, "User_ME", []string{"11111", "22222", "33333"}, []string{"33333"},
		map[string]string{"11111": "一组", "22222": "二组", "33333": "待同步组"},
	)
	if len(groups) != 2 || groups[0].Code != "11111" || groups[0].TotalTokens != 800 || groups[0].MemberCount != 2 {
		t.Fatalf("unexpected groups: %#v", groups)
	}
	if len(mine) != 3 || ranks["11111"] != 2 || ranks["22222"] != 1 {
		t.Fatalf("unexpected my groups/ranks: mine=%#v ranks=%#v", mine, ranks)
	}
	pendingFound := false
	for _, group := range mine {
		if group.Code == "33333" {
			pendingFound = group.PendingReport && group.IsCreator && group.Name == "待同步组"
		}
	}
	if !pendingFound {
		t.Fatalf("pending local group missing: %#v", mine)
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

func TestCommunityMemberNamesIncludesLatestPublicNames(t *testing.T) {
	reports := []communityReportData{
		{ID: "User_OLD", DisplayName: ""},
		{ID: "User_RENAMED", DisplayName: "琪琪"},
		{ID: "User_OTHER", DisplayName: "小昆"},
	}
	got := communityMemberNames(reports)
	want := map[string]string{"User_RENAMED": "琪琪", "User_OTHER": "小昆"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("communityMemberNames() = %#v, want %#v", got, want)
	}
}

// jsonKeys 提取 json.RawMessage (对象) 的顶层 key 顺序, 供 JSON 序断言用。
func jsonKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.Token() // '{'
	var keys []string
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			t.Fatalf("decode token: %v", err)
		}
		key, ok := token.(string)
		if !ok {
			t.Fatalf("expected string key, got %T", token)
		}
		keys = append(keys, key)
		// skip 1 个 value (number/string)
		if _, err := dec.Token(); err != nil {
			t.Fatalf("decode value: %v", err)
		}
	}
	return keys
}

func TestBuildToolDistributionJSONOrderByUsageDesc(t *testing.T) {
	raw := buildToolDistributionJSON(map[string]int64{
		"Codex": 1000, "ZCode": 500, "Claude": 200, "Hermes": 50,
	})
	keys := jsonKeys(t, raw)
	want := []string{"Codex", "ZCode", "Claude", "Hermes"}
	if len(keys) != len(want) {
		t.Fatalf("key count mismatch: got %v want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("key[%d] = %q, want %q (full: %v)", i, keys[i], want[i], keys)
		}
	}
}

func TestBuildToolDistributionJSONTiebreakByName(t *testing.T) {
	raw := buildToolDistributionJSON(map[string]int64{
		"Claude": 10,
		"Codex":  10,
		"ZCode":  10,
		"Hermes": 10,
	})
	keys := jsonKeys(t, raw)
	want := []string{"Claude", "Codex", "Hermes", "ZCode"}
	if len(keys) != len(want) {
		t.Fatalf("key count mismatch: got %v want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("tiebreak key[%d] = %q, want %q (full: %v)", i, keys[i], want[i], keys)
		}
	}
}

func TestBuildToolDistributionJSONEmpty(t *testing.T) {
	raw := buildToolDistributionJSON(map[string]int64{})
	if string(raw) != "{}" {
		t.Fatalf("expected {}, got %s", string(raw))
	}
}

func TestBuildToolDistributionJSONPctRounding(t *testing.T) {
	// 333 + 333 + 334 = 1000 → 33.3 / 33.3 / 33.4 (1 位小数, 不累计误差)
	raw := buildToolDistributionJSON(map[string]int64{
		"Codex": 333, "Claude": 333, "ZCode": 334,
	})
	keys := jsonKeys(t, raw)
	// ZCode 用量最高, 应排在第一; 同 pct 时按名字升序 (Claude 在 Codex 前)
	want := []string{"ZCode", "Claude", "Codex"}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("pct ordering key[%d] = %q, want %q (full: %v)", i, keys[i], want[i], keys)
		}
	}
	// 验证精确百分比
	var got map[string]float64
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["ZCode"] != 33.4 || got["Claude"] != 33.3 || got["Codex"] != 33.3 {
		t.Fatalf("pct rounding wrong: %+v", got)
	}
}
