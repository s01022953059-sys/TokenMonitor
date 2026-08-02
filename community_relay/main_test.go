package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	existing     *reportDocument
	existingByID map[string]*reportDocument
	sha          string
	written      *reportDocument
	writeSHA     string
	err          error
	listReports  []reportDocument
	listErr      error
	archived     []byte
	archiveDate  string
	archiveErr   error
}

func (s *fakeStore) Get(_ context.Context, id string) (*reportDocument, string, error) {
	if s.existingByID != nil {
		return s.existingByID[id], s.sha, s.err
	}
	return s.existing, s.sha, s.err
}

func (s *fakeStore) Write(_ context.Context, doc reportDocument, sha string) error {
	s.written = &doc
	s.writeSHA = sha
	return s.err
}

func (s *fakeStore) ListReports(_ context.Context) ([]reportDocument, error) {
	return s.listReports, s.listErr
}

func (s *fakeStore) WriteArchive(_ context.Context, date string, data []byte) error {
	s.archiveDate = date
	s.archived = data
	return s.archiveErr
}

func testSecret() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

func testRequest() reportRequest {
	return reportRequest{
		ID: "User_TEST1", DeviceSecret: testSecret(), ReportDate: "2026-07-11",
		TodayTokens: 12345, ByTool: map[string]int64{"Codex": 12000, "Unknown": 345}, Version: "1.4.20",
	}
}

func performReport(t *testing.T, store *fakeStore, request reportRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(request)
	recorder := httptest.NewRecorder()
	handler := &relayHandler{store: store, now: func() time.Time {
		return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	}}
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/report", bytes.NewReader(body)))
	return recorder
}

func TestCreatesAuthenticatedReport(t *testing.T) {
	store := &fakeStore{}
	response := performReport(t, store, testRequest())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if store.written == nil || store.written.AuthHash == "" {
		t.Fatal("authenticated report was not written")
	}
	if store.written.ByTool["Other"] != 345 {
		t.Fatalf("unknown tool was not grouped: %#v", store.written.ByTool)
	}
}

func TestUpdatesWhenCredentialMatches(t *testing.T) {
	secret, _ := base64.RawURLEncoding.DecodeString(testSecret())
	hash := sha256.Sum256(secret)
	store := &fakeStore{existing: &reportDocument{ID: "User_TEST1", AuthHash: hex.EncodeToString(hash[:])}, sha: "existing-sha"}
	response := performReport(t, store, testRequest())
	if response.Code != http.StatusOK || store.writeSHA != "existing-sha" {
		t.Fatalf("status=%d writeSHA=%q", response.Code, store.writeSHA)
	}
}

func TestReportUpdatePreservesProfileFields(t *testing.T) {
	secret, _ := base64.RawURLEncoding.DecodeString(testSecret())
	hash := sha256.Sum256(secret)
	store := &fakeStore{existing: &reportDocument{
		ID: "User_TEST1", AuthHash: hex.EncodeToString(hash[:]),
		DisplayName: "鹏帅", NameChangedAt: "2026-07-11T08:00:00Z",
	}, sha: "existing-sha"}
	response := performReport(t, store, testRequest())
	if response.Code != http.StatusOK || store.written == nil || store.written.DisplayName != "鹏帅" || store.written.NameChangedAt != "2026-07-11T08:00:00Z" {
		t.Fatalf("status=%d written=%#v", response.Code, store.written)
	}
}

func TestRejectsWrongCredential(t *testing.T) {
	store := &fakeStore{existing: &reportDocument{ID: "User_TEST1", AuthHash: "wrong"}, sha: "sha"}
	response := performReport(t, store, testRequest())
	if response.Code != http.StatusForbidden || store.written != nil {
		t.Fatalf("status=%d written=%v", response.Code, store.written != nil)
	}
}

func TestLegacyIdentityRequiresUpgrade(t *testing.T) {
	store := &fakeStore{existing: &reportDocument{ID: "User_TEST1"}, sha: "sha"}
	response := performReport(t, store, testRequest())
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte("identity_upgrade_required")) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMigrationRecordsReplacedLegacyIdentity(t *testing.T) {
	request := testRequest()
	request.ID = "User_NEW001"
	request.ReplacesID = "User_OLD01"
	store := &fakeStore{existingByID: map[string]*reportDocument{
		"User_OLD01": {
			ID: "User_OLD01", ReportDate: request.ReportDate, TodayTokens: request.TodayTokens,
			ByTool: normalizeTools(request.ByTool),
		},
	}}
	response := performReport(t, store, request)
	if response.Code != http.StatusOK || store.written == nil || store.written.ReplacesID != "User_OLD01" {
		t.Fatalf("status=%d written=%#v body=%s", response.Code, store.written, response.Body.String())
	}
}

func TestMigrationRejectsMismatchedLegacyReport(t *testing.T) {
	request := testRequest()
	request.ID = "User_NEW001"
	request.ReplacesID = "User_OLD01"
	store := &fakeStore{existingByID: map[string]*reportDocument{
		"User_OLD01": {ID: "User_OLD01", ReportDate: request.ReportDate, TodayTokens: 1},
	}}
	response := performReport(t, store, request)
	if response.Code != http.StatusConflict || store.written != nil {
		t.Fatalf("status=%d written=%#v", response.Code, store.written)
	}
}

func TestRejectsInvalidReport(t *testing.T) {
	request := testRequest()
	request.ID = "../../etc/passwd"
	response := performReport(t, &fakeStore{}, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestStorageFailureIsHidden(t *testing.T) {
	response := performReport(t, &fakeStore{err: errors.New("secret backend detail")}, testRequest())
	if response.Code != http.StatusBadGateway || bytes.Contains(response.Body.Bytes(), []byte("secret backend detail")) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNewToolsNotGroupedAsOther(t *testing.T) {
	cases := []struct {
		tool   string
		tokens int64
	}{
		{"ZCode", 150000000},
		{"MiniMax Code", 60000000},
	}
	for _, tc := range cases {
		request := testRequest()
		request.ByTool = map[string]int64{tc.tool: tc.tokens, "Codex": 9000000}
		store := &fakeStore{}
		response := performReport(t, store, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", tc.tool, response.Code, response.Body.String())
		}
		if store.written == nil {
			t.Fatalf("%s: report not written", tc.tool)
		}
		if store.written.ByTool["Other"] != 0 {
			t.Errorf("%s: was incorrectly grouped into Other (got %d)", tc.tool, store.written.ByTool["Other"])
		}
		if store.written.ByTool[tc.tool] != tc.tokens {
			t.Errorf("%s: tokens mismatch, got %d want %d", tc.tool, store.written.ByTool[tc.tool], tc.tokens)
		}
	}
}

func TestUnknownToolStillGroupedAsOther(t *testing.T) {
	request := testRequest()
	request.ByTool = map[string]int64{"SomeRandomTool": 500, "Codex": 1000}
	store := &fakeStore{}
	response := performReport(t, store, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if store.written.ByTool["Other"] != 500 {
		t.Fatalf("unknown tool not grouped as Other: %#v", store.written.ByTool)
	}
}

func TestArchiveCreatesTop10Snapshot(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	reports := []reportDocument{
		{ID: "User_A001", ReportDate: "2026-07-24", TodayTokens: 1000, ByTool: map[string]int64{"Codex": 1000}, DisplayName: "Alice", UpdatedAt: "2026-07-24T11:00:00Z"},
		{ID: "User_B002", ReportDate: "2026-07-24", TodayTokens: 2000, ByTool: map[string]int64{"ZCode": 2000}, DisplayName: "Bob", UpdatedAt: "2026-07-24T11:30:00Z"},
		{ID: "User_C003", ReportDate: "2026-07-23", TodayTokens: 500, ByTool: map[string]int64{"Codex": 500}, DisplayName: "Carol", UpdatedAt: "2026-07-23T20:00:00Z"},
	}
	store := &fakeStore{listReports: reports}
	handler := &relayHandler{store: store, now: func() time.Time { return now }}

	if err := handler.runArchive(); err != nil {
		t.Fatalf("runArchive error: %v", err)
	}

	if store.archiveDate != "2026-07-24" {
		t.Fatalf("archive date: got %s want 2026-07-24", store.archiveDate)
	}

	var snapshot archiveSnapshot
	if err := json.Unmarshal(store.archived, &snapshot); err != nil {
		t.Fatalf("unmarshal archive: %v", err)
	}

	// Carol (report_date=07-23) 不计入当天活跃, 只有 Alice + Bob
	if snapshot.ActiveUsers != 2 {
		t.Fatalf("active users: got %d want 2", snapshot.ActiveUsers)
	}
	if snapshot.TotalUsers != 3 {
		t.Fatalf("total users: got %d want 3", snapshot.TotalUsers)
	}

	// TOP10 按 token 降序: Bob(2000) > Alice(1000)
	if len(snapshot.Leaderboard) != 2 {
		t.Fatalf("leaderboard length: got %d want 2", len(snapshot.Leaderboard))
	}
	if snapshot.Leaderboard[0].ID != "User_B002" || snapshot.Leaderboard[0].Tokens != 2000 {
		t.Fatalf("first place: got %+v", snapshot.Leaderboard[0])
	}
	if snapshot.Leaderboard[1].ID != "User_A001" || snapshot.Leaderboard[1].Tokens != 1000 {
		t.Fatalf("second place: got %+v", snapshot.Leaderboard[1])
	}
	if snapshot.Leaderboard[0].Tool != "ZCode" {
		t.Fatalf("tool format: got %s want ZCode", snapshot.Leaderboard[0].Tool)
	}
}

func TestCommunityArchiveScheduleUsesAsiaShanghai(t *testing.T) {
	now := time.Date(2026, 7, 28, 15, 54, 0, 0, time.UTC)
	wantNext := time.Date(2026, 7, 28, 15, 55, 0, 0, time.UTC)
	if got := nextCommunityArchiveTime(now); !got.Equal(wantNext) {
		t.Fatalf("next archive: got %s want %s", got, wantNext)
	}
	if got := communityArchiveDate(wantNext); got != "2026-07-28" {
		t.Fatalf("archive date: got %s want 2026-07-28", got)
	}
	if got := communityArchiveDate(time.Date(2026, 7, 28, 23, 55, 0, 0, time.UTC)); got != "2026-07-29" {
		t.Fatalf("late UTC archive date: got %s want 2026-07-29", got)
	}
}

func TestArchiveEndpointReturnsOK(t *testing.T) {
	store := &fakeStore{listReports: []reportDocument{}}
	handler := &relayHandler{store: store, now: func() time.Time {
		return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	}}
	body, _ := json.Marshal(map[string]string{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/archive", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestArchiveDeduplicatesByID(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	// 同一用户两份报告, 保留 UpdatedAt 更新的
	reports := []reportDocument{
		{ID: "User_A001", ReportDate: "2026-07-24", TodayTokens: 100, UpdatedAt: "2026-07-24T10:00:00Z"},
		{ID: "User_A001", ReportDate: "2026-07-24", TodayTokens: 500, UpdatedAt: "2026-07-24T11:00:00Z"},
	}
	store := &fakeStore{listReports: reports}
	handler := &relayHandler{store: store, now: func() time.Time { return now }}

	handler.runArchive()

	var snapshot archiveSnapshot
	json.Unmarshal(store.archived, &snapshot)
	if len(snapshot.Leaderboard) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d", len(snapshot.Leaderboard))
	}
	if snapshot.Leaderboard[0].Tokens != 500 {
		t.Fatalf("expected 500 tokens (latest), got %d", snapshot.Leaderboard[0].Tokens)
	}
}

// TestArchiveIncludesZeroTokenReports 复现 "7.24 统计只显示 2 人" 的 bug:
// 当天 ReportDate 命中但 TodayTokens == 0 的用户 (今天刚开 app 没产生用量就上报一条占位)
// 应当出现在 leaderboard (token=0), 不能被过滤掉, 否则排名变化弹窗里看不到 "当天出现过" 的人。
func TestArchiveIncludesZeroTokenReports(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	reports := []reportDocument{
		{ID: "User_A001", ReportDate: "2026-07-24", TodayTokens: 0, ByTool: map[string]int64{}, DisplayName: "Alice", UpdatedAt: "2026-07-24T08:00:00Z"},
		{ID: "User_B002", ReportDate: "2026-07-24", TodayTokens: 2000, ByTool: map[string]int64{"ZCode": 2000}, DisplayName: "Bob", UpdatedAt: "2026-07-24T11:30:00Z"},
		{ID: "User_C003", ReportDate: "2026-07-24", TodayTokens: 100, ByTool: map[string]int64{"Codex": 100}, DisplayName: "Carol", UpdatedAt: "2026-07-24T11:00:00Z"},
		{ID: "User_D004", ReportDate: "2026-07-23", TodayTokens: 99999, ByTool: map[string]int64{"Codex": 99999}, DisplayName: "Yesterday", UpdatedAt: "2026-07-23T20:00:00Z"},
	}
	store := &fakeStore{listReports: reports}
	handler := &relayHandler{store: store, now: func() time.Time { return now }}

	if err := handler.runArchive(); err != nil {
		t.Fatalf("runArchive error: %v", err)
	}

	var snapshot archiveSnapshot
	if err := json.Unmarshal(store.archived, &snapshot); err != nil {
		t.Fatalf("unmarshal archive: %v", err)
	}

	// 跨日期去重的总 user 数仍记作 4 (含昨日那位)
	if snapshot.TotalUsers != 4 {
		t.Fatalf("total users: got %d want 4", snapshot.TotalUsers)
	}
	// 7.24 当天 ReportDate 命中 3 个, leaderboard 应有 3 条 (含 0-token 的 Alice)
	if len(snapshot.Leaderboard) != 3 {
		t.Fatalf("leaderboard length: got %d want 3 (must include 0-token user)", len(snapshot.Leaderboard))
	}
	// Top1 = Bob (2000), Top2 = Carol (100), Top3 = Alice (0)
	if snapshot.Leaderboard[0].ID != "User_B002" || snapshot.Leaderboard[0].Tokens != 2000 {
		t.Fatalf("first place: got %+v", snapshot.Leaderboard[0])
	}
	if snapshot.Leaderboard[1].ID != "User_C003" || snapshot.Leaderboard[1].Tokens != 100 {
		t.Fatalf("second place: got %+v", snapshot.Leaderboard[1])
	}
	// 关键: 0-token 用户应在 leaderboard 里 (不能在 fix 前被过滤掉)
	var foundAlice bool
	for _, e := range snapshot.Leaderboard {
		if e.ID == "User_A001" {
			foundAlice = true
			if e.Tokens != 0 {
				t.Fatalf("Alice TodayTokens: got %d want 0", e.Tokens)
			}
		}
	}
	if !foundAlice {
		t.Fatalf("0-token user Alice missing from leaderboard (this is the bug)")
	}
	// 昨日 (2026-07-23) 的 Yesterday 不出现在 7.24 leaderboard
	for _, e := range snapshot.Leaderboard {
		if e.ID == "User_D004" {
			t.Fatalf("Yesterday (07-23) leaked into 7.24 leaderboard: %+v", e)
		}
	}
}

func TestArchiveStoresAllParticipantsButLeaderboardRemainsTopTen(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	reports := make([]reportDocument, 0, 12)
	for i := 0; i < 12; i++ {
		reports = append(reports, reportDocument{
			ID: "User_" + strconv.Itoa(i), ReportDate: "2026-07-27", TodayTokens: int64(12 - i), UpdatedAt: "2026-07-27T11:00:00Z",
		})
	}
	store := &fakeStore{listReports: reports}
	handler := &relayHandler{store: store, now: func() time.Time { return now }}

	if err := handler.runArchive(); err != nil {
		t.Fatalf("runArchive error: %v", err)
	}
	var snapshot archiveSnapshot
	if err := json.Unmarshal(store.archived, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Participants) != 12 {
		t.Fatalf("participants=%d want 12", len(snapshot.Participants))
	}
	if len(snapshot.Leaderboard) != 10 {
		t.Fatalf("leaderboard=%d want 10", len(snapshot.Leaderboard))
	}
}

// TestArchiveRespectsTop10LimitEvenWithZeroTokenEntries 11+ 个用户,
func TestArchiveRespectsTop10LimitEvenWithZeroTokenEntries(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	reports := make([]reportDocument, 0, 12)
	for i := 0; i < 12; i++ {
		// 前 2 名有 token, 后 10 名 0 token (以 ID 升序来避免重复)
		tokens := int64(0)
		if i < 2 {
			tokens = int64(10000 - i*1000)
		}
		updated := "2026-07-24T08:00:00Z"
		if i < 2 {
			updated = "2026-07-24T10:00:00Z"
		}
		reports = append(reports, reportDocument{
			ID: fmt.Sprintf("User_T%03d", i), ReportDate: "2026-07-24", TodayTokens: tokens,
			ByTool: map[string]int64{}, DisplayName: fmt.Sprintf("user%d", i),
			UpdatedAt: updated,
		})
	}
	store := &fakeStore{listReports: reports}
	handler := &relayHandler{store: store, now: func() time.Time { return now }}

	if err := handler.runArchive(); err != nil {
		t.Fatalf("runArchive error: %v", err)
	}

	var snapshot archiveSnapshot
	if err := json.Unmarshal(store.archived, &snapshot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// leaderboard 仍限制 10 条
	if len(snapshot.Leaderboard) != 10 {
		t.Fatalf("leaderboard length: got %d want 10 (cap)", len(snapshot.Leaderboard))
	}
	// Top1 仍是有 token 的 (10000), 不被 0-token 占位
	if snapshot.Leaderboard[0].Tokens == 0 {
		t.Fatalf("top1 should be the highest-token user, not zero")
	}
}

// TestGitCodeStoreCacheControlHeaders 验证所有 GitCode API GET 请求都携带
// Cache-Control: no-cache 和 Pragma: no-cache 头，避免 CDN 返回缓存的旧数据。
// v1.4.88 修复: 客户端 community.py 已加，这里补齐 VPS 中继的 Go 实现。
func TestGitCodeStoreCacheControlHeaders(t *testing.T) {
	// 记录所有 GET 请求的 header
	var capturedHeaders []http.Header
	var serverURL string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			capturedHeaders = append(capturedHeaders, r.Header.Clone())
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/contents/community/reports" {
			// 目录列表：返回一个 JSON 数组
			json.NewEncoder(w).Encode([]map[string]string{
				{"name": "User_TESTCDN.json", "download_url": serverURL + "/raw/User_TESTCDN.json"},
			})
		} else if strings.HasPrefix(r.URL.Path, "/raw/") {
			// 单个报告文件下载
			json.NewEncoder(w).Encode(reportDocument{ID: "User_TESTCDN", ReportDate: "2026-08-02", TodayTokens: 100})
		} else if strings.Contains(r.URL.Path, "/archive/") {
			// 归档文件
			json.NewEncoder(w).Encode(map[string]string{"sha": "test-sha-123"})
		} else {
			// 单个报告 Get: 返回 base64 编码的内容
			content, _ := json.Marshal(reportDocument{ID: "User_TESTCDN", ReportDate: "2026-08-02", TodayTokens: 100})
			encoded := base64.StdEncoding.EncodeToString(content)
			json.NewEncoder(w).Encode(map[string]string{"sha": "test-sha", "content": encoded})
		}
	}))
	defer ts.Close()
	serverURL = ts.URL

	store := &gitCodeStore{
		apiBase: ts.URL,
		branch:  "community-data",
		token:   "test-token",
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	ctx := context.Background()

	// 1. Get: 读取单个报告
	_, _, _ = store.Get(ctx, "User_TESTCDN")

	// 2. ListReports: 目录列表 + 单个文件下载
	_, _ = store.ListReports(ctx)

	// 3. WriteArchive: GET 已有归档
	_ = store.WriteArchive(ctx, "2026-08-02", []byte(`{}`))

	// 验证所有 GET 请求都携带了正确的缓存头
	if len(capturedHeaders) == 0 {
		t.Fatal("no GET requests were captured")
	}

	for i, headers := range capturedHeaders {
		cacheControl := headers.Get("Cache-Control")
		pragma := headers.Get("Pragma")
		if cacheControl != "no-cache" {
			t.Errorf("request %d: Cache-Control = %q, want %q", i, cacheControl, "no-cache")
		}
		if pragma != "no-cache" {
			t.Errorf("request %d: Pragma = %q, want %q", i, pragma, "no-cache")
		}
	}

	// 至少应捕获到 4 个 GET 请求: Get + ListReports(目录) + ListReports(文件) + WriteArchive
	if len(capturedHeaders) < 4 {
		t.Errorf("expected at least 4 GET requests, got %d", len(capturedHeaders))
	}

	t.Logf("all %d GET requests have correct cache-control headers", len(capturedHeaders))
}
