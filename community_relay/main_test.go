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
