package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const maxRequestBytes = 16 * 1024
const maxTokenCount int64 = 1_000_000_000_000

var communityIDPattern = regexp.MustCompile(`^User_[A-Z0-9]{5,12}$`)

var allowedTools = map[string]bool{
	"Codex": true, "Claude": true, "Hermes": true, "OpenCode": true,
	"WorkBuddy": true, "Antigravity": true, "Other": true,
}

type reportRequest struct {
	ID           string           `json:"id"`
	DeviceSecret string           `json:"device_secret"`
	ReportDate   string           `json:"report_date"`
	TodayTokens  int64            `json:"today_tokens"`
	ByTool       map[string]int64 `json:"by_tool"`
	Version      string           `json:"version"`
	ReplacesID   string           `json:"replaces_id,omitempty"`
}

type reportDocument struct {
	ID            string           `json:"id"`
	AuthHash      string           `json:"auth_hash"`
	UpdatedAt     string           `json:"updated_at"`
	ReportDate    string           `json:"report_date"`
	TodayTokens   int64            `json:"today_tokens"`
	ByTool        map[string]int64 `json:"by_tool"`
	ToolCount     int              `json:"tool_count"`
	Version       string           `json:"version"`
	ReplacesID    string           `json:"replaces_id,omitempty"`
	DisplayName   string           `json:"display_name,omitempty"`
	NameChangedAt string           `json:"name_changed_at,omitempty"`
}

type reportStore interface {
	Get(ctx context.Context, id string) (*reportDocument, string, error)
	Write(ctx context.Context, doc reportDocument, sha string) error
}

type relayHandler struct {
	store    reportStore
	profiles *profileDatabase
	now      func() time.Time
}

func (h *relayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "service": "token-monitor-community"})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/report":
		h.handleReport(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/profile":
		h.handleProfile(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "接口不存在")
	}
}

func (h *relayHandler) handleReport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request reportRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求只能包含一个 JSON 对象")
		return
	}

	secret, err := validateReport(request, h.now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_report", err.Error())
		return
	}
	authHashBytes := sha256.Sum256(secret)
	authHash := hex.EncodeToString(authHashBytes[:])

	existing, sha, err := h.store.Get(r.Context(), request.ID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "storage_unavailable", "社区存储暂时不可用")
		return
	}
	if existing != nil {
		if existing.AuthHash == "" {
			writeError(w, http.StatusConflict, "identity_upgrade_required", "旧社区身份需要自动升级")
			return
		}
		if subtle.ConstantTimeCompare([]byte(existing.AuthHash), []byte(authHash)) != 1 {
			writeError(w, http.StatusForbidden, "credential_invalid", "设备凭据不匹配")
			return
		}
	}
	replacesID := ""
	displayName := ""
	nameChangedAt := ""
	if existing != nil {
		replacesID = existing.ReplacesID
		displayName = existing.DisplayName
		nameChangedAt = existing.NameChangedAt
	} else if request.ReplacesID != "" {
		previous, _, previousErr := h.store.Get(r.Context(), request.ReplacesID)
		if previousErr != nil {
			writeError(w, http.StatusBadGateway, "storage_unavailable", "社区存储暂时不可用")
			return
		}
		if previous == nil || previous.AuthHash != "" || !sameReportContent(*previous, request) {
			writeError(w, http.StatusConflict, "identity_migration_invalid", "旧社区身份迁移校验失败")
			return
		}
		replacesID = request.ReplacesID
	}

	now := h.now().UTC()
	doc := reportDocument{
		ID: request.ID, AuthHash: authHash, UpdatedAt: now.Format(time.RFC3339),
		ReportDate: request.ReportDate, TodayTokens: request.TodayTokens,
		ByTool: normalizeTools(request.ByTool), ToolCount: len(request.ByTool),
		Version: strings.TrimSpace(request.Version), ReplacesID: replacesID,
		DisplayName: displayName, NameChangedAt: nameChangedAt,
	}
	if err := h.store.Write(r.Context(), doc, sha); err != nil {
		writeError(w, http.StatusBadGateway, "upload_failed", "匿名统计写入失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "status": "synced", "message": "匿名统计已同步", "reported_at": doc.UpdatedAt,
	})
}

func (h *relayHandler) handleProfile(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请求必须使用 JSON")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request profileRequest
	if err := decoder.Decode(&request); err != nil || ensureJSONEnd(decoder) != nil || validateProfileRequest(request) != nil {
		writeError(w, http.StatusBadRequest, "name_invalid", "昵称请求格式不正确")
		return
	}
	displayName, canonicalName, err := normalizeDisplayName(request.DisplayName, blockedNamesPath())
	if err != nil {
		writeError(w, http.StatusBadRequest, "name_invalid", err.Error())
		return
	}
	report, sha, err := h.store.Get(r.Context(), request.ID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "storage_unavailable", "社区存储暂时不可用")
		return
	}
	if report == nil || report.AuthHash == "" {
		writeError(w, http.StatusNotFound, "profile_not_found", "请先完成一次社区同步")
		return
	}
	if !validateDeviceSecret(request.DeviceSecret, report.AuthHash) {
		writeError(w, http.StatusForbidden, "credential_invalid", "设备凭据不匹配")
		return
	}
	if h.profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "昵称服务暂时不可用")
		return
	}
	original := *report
	profileNow := h.now().UTC()
	result, reportWritten, err := h.profiles.updateName(r.Context(), request.ID, displayName, canonicalName, profileNow, func() error {
		report.DisplayName = displayName
		report.NameChangedAt = profileNow.Format(time.RFC3339)
		return h.store.Write(r.Context(), *report, sha)
	})
	if err != nil {
		if reportWritten {
			_ = h.store.Write(context.Background(), original, sha)
		}
		typed := profileStorageError(err)
		payload := map[string]interface{}{"ok": false, "status": typed.code, "message": typed.message}
		if !typed.nextChangeAt.IsZero() {
			payload["next_change_at"] = typed.nextChangeAt.UTC().Format(time.RFC3339)
		}
		writeJSON(w, typed.status, payload)
		return
	}
	payload := map[string]interface{}{
		"ok": true, "status": "updated", "display_name": result.DisplayName,
		"unchanged": result.NoChange,
	}
	if !result.NextChangeAt.IsZero() {
		payload["next_change_at"] = result.NextChangeAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, payload)
}

func sameReportContent(previous reportDocument, request reportRequest) bool {
	return previous.ReportDate == request.ReportDate &&
		previous.TodayTokens == request.TodayTokens &&
		reflect.DeepEqual(normalizeTools(previous.ByTool), normalizeTools(request.ByTool))
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}

func validateReport(request reportRequest, now time.Time) ([]byte, error) {
	if !communityIDPattern.MatchString(request.ID) {
		return nil, errors.New("匿名 ID 格式不正确")
	}
	if request.ReplacesID != "" && (!communityIDPattern.MatchString(request.ReplacesID) || request.ReplacesID == request.ID) {
		return nil, errors.New("旧匿名 ID 格式不正确")
	}
	secret, err := base64.RawURLEncoding.DecodeString(request.DeviceSecret)
	if err != nil || len(secret) != 32 {
		return nil, errors.New("设备凭据格式不正确")
	}
	reportDay, err := time.Parse("2006-01-02", request.ReportDate)
	if err != nil {
		return nil, errors.New("报告日期格式不正确")
	}
	serverDay, _ := time.Parse("2006-01-02", now.Format("2006-01-02"))
	delta := reportDay.Sub(serverDay)
	if delta < -24*time.Hour || delta > 24*time.Hour {
		return nil, errors.New("报告日期超出允许范围")
	}
	if request.TodayTokens < 0 || request.TodayTokens > maxTokenCount {
		return nil, errors.New("Token 总数超出允许范围")
	}
	if len(request.ByTool) > 20 {
		return nil, errors.New("工具数量超出允许范围")
	}
	for _, tokens := range request.ByTool {
		if tokens < 0 || tokens > maxTokenCount {
			return nil, errors.New("工具 Token 数超出允许范围")
		}
	}
	if len(request.Version) > 32 {
		return nil, errors.New("版本号过长")
	}
	return secret, nil
}

func normalizeTools(input map[string]int64) map[string]int64 {
	result := make(map[string]int64)
	for tool, tokens := range input {
		if !allowedTools[tool] {
			result["Other"] += tokens
			continue
		}
		result[tool] += tokens
	}
	return result
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, map[string]interface{}{"ok": false, "status": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	writeJSONStatus(w, status, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type gitCodeStore struct {
	apiBase string
	branch  string
	token   string
	client  *http.Client
}

func (s *gitCodeStore) Get(ctx context.Context, id string) (*reportDocument, string, error) {
	url := fmt.Sprintf("%s/contents/community/reports/%s.json?ref=%s", s.apiBase, id, s.branch)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("gitcode GET HTTP %d", resp.StatusCode)
	}
	var payload struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", err
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return nil, "", err
	}
	var doc reportDocument
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, "", err
	}
	return &doc, payload.SHA, nil
}

func (s *gitCodeStore) Write(ctx context.Context, doc reportDocument, sha string) error {
	content, _ := json.MarshalIndent(doc, "", "  ")
	payload := map[string]interface{}{
		"message": "community: " + doc.ID + " report",
		"content": base64.StdEncoding.EncodeToString(content),
		"branch":  s.branch,
	}
	method := http.MethodPost
	if sha != "" {
		method = http.MethodPut
		payload["sha"] = sha
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/contents/community/reports/%s.json", s.apiBase, doc.ID)
	req, _ := http.NewRequestWithContext(ctx, method, url, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitcode write HTTP %d", resp.StatusCode)
	}
	return nil
}

func main() {
	token := strings.TrimSpace(os.Getenv("GITCODE_TOKEN"))
	if token == "" {
		log.Fatal("GITCODE_TOKEN is required")
	}
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:18190"
	}
	apiBase := os.Getenv("GITCODE_API_BASE")
	if apiBase == "" {
		apiBase = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"
	}
	branch := os.Getenv("COMMUNITY_BRANCH")
	if branch == "" {
		branch = "community-data"
	}
	profiles, err := openProfileDatabase(profileDatabasePath())
	if err != nil {
		log.Fatalf("open profile database: %v", err)
	}
	defer profiles.Close()
	handler := &relayHandler{
		store:    &gitCodeStore{apiBase: apiBase, branch: branch, token: token, client: &http.Client{Timeout: 15 * time.Second}},
		profiles: profiles, now: time.Now,
	}
	server := &http.Server{
		Addr: listenAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	log.Printf("community relay listening on %s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
