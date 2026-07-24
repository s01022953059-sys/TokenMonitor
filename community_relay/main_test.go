package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		tool  string
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
