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
	"sort"
	"strings"
	"sync"
	"time"
)

const maxRequestBytes = 16 * 1024
const maxTokenCount int64 = 1_000_000_000_000

var communityIDPattern = regexp.MustCompile(`^User_[A-Z0-9]{5,12}$`)
var communityTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

var allowedTools = map[string]bool{
	"Codex": true, "Claude": true, "Hermes": true, "OpenCode": true,
	"WorkBuddy": true, "Antigravity": true, "ZCode": true, "MiniMax Code": true,
	"Other": true,
}

type reportRequest struct {
	ID           string           `json:"id"`
	DeviceSecret string           `json:"device_secret"`
	ReportDate   string           `json:"report_date"`
	TodayTokens  int64            `json:"today_tokens"`
	ByTool       map[string]int64 `json:"by_tool"`
	Version      string           `json:"version"`
	ReplacesID   string           `json:"replaces_id,omitempty"`
	GroupCode    string           `json:"group_code,omitempty"`
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
	GroupCode     string           `json:"group_code,omitempty"`
}

type reportStore interface {
	Get(ctx context.Context, id string) (*reportDocument, string, error)
	Write(ctx context.Context, doc reportDocument, sha string) error
	ListReports(ctx context.Context) ([]reportDocument, error)
	WriteArchive(ctx context.Context, date string, data []byte) error
}

type relayHandler struct {
	store    reportStore
	profiles *profileDatabase
	now      func() time.Time
}

func communityArchiveDate(now time.Time) string {
	return now.In(communityTimeZone).Format("2006-01-02")
}

func nextCommunityArchiveTime(now time.Time) time.Time {
	localNow := now.In(communityTimeZone)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 55, 0, 0, communityTimeZone)
	if localNow.After(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
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
case r.Method == http.MethodPost && r.URL.Path == "/v1/archive":
			h.handleArchive(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/groups":
			h.handleCreateGroup(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/groups/"):
			h.handleGetGroup(w, r)
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
	// Preserve existing group code if the request doesn't provide one
	// (old clients that don't know about groups).
	groupCode := request.GroupCode
	if groupCode == "" && existing != nil {
		groupCode = existing.GroupCode
	}

	now := h.now().UTC()
	doc := reportDocument{
		ID: request.ID, AuthHash: authHash, UpdatedAt: now.Format(time.RFC3339),
		ReportDate: request.ReportDate, TodayTokens: request.TodayTokens,
		ByTool: normalizeTools(request.ByTool), ToolCount: len(request.ByTool),
		Version: strings.TrimSpace(request.Version), ReplacesID: replacesID,
		DisplayName: displayName, NameChangedAt: nameChangedAt,
		GroupCode: groupCode,
	}
	if err := h.store.Write(r.Context(), doc, sha); err != nil {
		writeError(w, http.StatusBadGateway, "upload_failed", "匿名统计写入失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "status": "synced", "message": "匿名统计已同步", "reported_at": doc.UpdatedAt,
	})
}

// archiveEntry 是每日快照里的一条参与者记录。
type archiveEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Tokens      int64  `json:"tokens"`
	Tool        string `json:"tool"`
	GroupCode   string `json:"group_code,omitempty"`
}

// archiveGroup 是每日快照里的一组统计。
type archiveGroup struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	TotalTokens int64  `json:"total_tokens"`
	MemberCount int    `json:"member_count"`
	TopMember   string `json:"top_member"`
}

// archiveSnapshot 是一天的完整快照。
type archiveSnapshot struct {
	Date         string         `json:"date"`
	GeneratedAt  string         `json:"generated_at"`
	TotalUsers   int            `json:"total_users"`
	ActiveUsers  int            `json:"active_users"`
	Participants []archiveEntry `json:"participants"`
	Leaderboard  []archiveEntry `json:"leaderboard"`
	Groups       []archiveGroup `json:"groups,omitempty"`
}

// formatArchiveTools 按 token 降序拼接工具名, 对齐客户端 _format_report_tools。
func formatArchiveTools(byTool map[string]int64) string {
	type toolItem struct {
		name   string
		tokens int64
	}
	items := make([]toolItem, 0, len(byTool))
	for name, tokens := range byTool {
		if tokens > 0 {
			items = append(items, toolItem{name, tokens})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].tokens != items[j].tokens {
			return items[i].tokens > items[j].tokens
		}
		return items[i].name < items[j].name
	})
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = item.name
	}
	if len(parts) == 0 {
		return "?"
	}
	return strings.Join(parts, " + ")
}

// runArchive 读取所有报告，归档当天全量参与者并保留兼容用 TOP10。
func (h *relayHandler) runArchive() error {
	ctx := context.Background()
	now := h.now()
	reports, err := h.store.ListReports(ctx)
	if err != nil {
		return fmt.Errorf("list reports: %w", err)
	}

	// 按 ID 去重, 保留最新
	latest := map[string]reportDocument{}
	for _, r := range reports {
		if existing, ok := latest[r.ID]; !ok || r.UpdatedAt > existing.UpdatedAt {
			latest[r.ID] = r
		}
	}

	// 按 report_date 过滤当天。
	// 当天上过报 (无论当日是否产生用量) 的用户都进 leaderboard,
	// 让 0-token 占位用户也能在归档中露出, 客户端 TOP10 排名变化弹窗
	// 会显示 "出现过但没操作" 的人, 而不是只显示有操作的两三个人。
	today := communityArchiveDate(now)
	var active []reportDocument
	for _, r := range latest {
		if r.ReportDate == today {
			active = append(active, r)
		}
	}

	// 按 token 降序排序；participants 保留全量，leaderboard 只取 TOP10。
	sort.SliceStable(active, func(i, j int) bool {
		return active[i].TodayTokens > active[j].TodayTokens
	})
	participants := make([]archiveEntry, 0, len(active))
	for i := 0; i < len(active); i++ {
		r := active[i]
		participants = append(participants, archiveEntry{
			ID:          r.ID,
			DisplayName: r.DisplayName,
			Tokens:      r.TodayTokens,
			Tool:        formatArchiveTools(r.ByTool),
			GroupCode:   r.GroupCode,
		})
	}
	limit := 10
	if len(participants) < limit {
		limit = len(participants)
	}
	entries := append([]archiveEntry(nil), participants[:limit]...)

	// 组队聚合: 按 group_code 分组统计
	groupStats := map[string]*archiveGroup{}
	groupNames := map[string]string{} // code -> name (from groups table)
	for _, r := range active {
		code := r.GroupCode
		if code == "" {
			continue
		}
		if _, ok := groupStats[code]; !ok {
			name, _, _ := h.profiles.getGroup(ctx, code)
			groupStats[code] = &archiveGroup{Code: code, Name: name}
			groupNames[code] = name
		}
		groupStats[code].TotalTokens += r.TodayTokens
		groupStats[code].MemberCount++
		if r.DisplayName != "" && (groupStats[code].TopMember == "" || r.TodayTokens > 0) {
			groupStats[code].TopMember = r.DisplayName
		}
	}
	groups := make([]archiveGroup, 0, len(groupStats))
	for _, g := range groupStats {
		if g.MemberCount > 0 {
			groups = append(groups, *g)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].TotalTokens > groups[j].TotalTokens
	})

	snapshot := archiveSnapshot{
		Date:         today,
		GeneratedAt:  now.UTC().Format(time.RFC3339),
		TotalUsers:   len(latest),
		ActiveUsers:  len(active),
		Participants: participants,
		Leaderboard:  entries,
		Groups:       groups,
	}
	data, _ := json.MarshalIndent(snapshot, "", "  ")
	return h.store.WriteArchive(ctx, today, data)
}

func (h *relayHandler) handleArchive(w http.ResponseWriter, r *http.Request) {
	if err := h.runArchive(); err != nil {
		writeError(w, http.StatusBadGateway, "archive_failed", "归档失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "status": "archived", "message": "每日排行榜快照已归档",
	})
}

func (h *relayHandler) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请求必须使用 JSON")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request groupRequest
	if err := decoder.Decode(&request); err != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	if request.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "组名不能为空")
		return
	}

	// Authenticate via device secret in the profile header
	credential := r.Header.Get("X-Device-ID")
	secret := r.Header.Get("X-Device-Secret")
	if !communityIDPattern.MatchString(credential) {
		writeError(w, http.StatusBadRequest, "invalid_credential", "设备凭据格式不正确")
		return
	}
	existing, _, err := h.store.Get(r.Context(), credential)
	if err != nil {
		writeError(w, http.StatusBadGateway, "storage_unavailable", "社区存储暂时不可用")
		return
	}
	if existing == nil || existing.AuthHash == "" {
		writeError(w, http.StatusNotFound, "profile_not_found", "请先完成一次社区同步")
		return
	}
	if !validateDeviceSecret(secret, existing.AuthHash) {
		writeError(w, http.StatusForbidden, "credential_invalid", "设备凭据不匹配")
		return
	}

	result, err := h.profiles.createGroup(r.Context(), credential, request.Name, h.now())
	if err != nil {
		if pe, ok := err.(*profileError); ok {
			writeJSON(w, pe.status, map[string]interface{}{"ok": false, "status": pe.code, "message": pe.message})
			return
		}
		writeError(w, http.StatusInternalServerError, "group_creation_failed", "创建组队失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"code": result.Code,
		"name": result.Name,
	})
}

func (h *relayHandler) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/v1/groups/")
	if len(code) != 5 || !regexp.MustCompile(`^[0-9]{5}$`).MatchString(code) {
		writeError(w, http.StatusBadRequest, "invalid_code", "组码格式不正确")
		return
	}
	name, createdBy, ok := h.profiles.getGroup(r.Context(), code)
	if !ok {
		writeError(w, http.StatusNotFound, "group_not_found", "组队不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"code":       code,
		"name":       name,
		"created_by": createdBy,
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
		// v1.4.88: 用 Asia/Shanghai 日期做校验基准，与 runArchive 归档过滤保持一致。
		// 客户端已统一用北京时间上报 report_date，这里确保校验窗口也使用同一时区。
		serverDay, _ := time.Parse("2006-01-02", communityArchiveDate(now))
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
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
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

// ListReports 读取 community/reports/ 目录下所有报告文件并解析。
func (s *gitCodeStore) ListReports(ctx context.Context) ([]reportDocument, error) {
		url := fmt.Sprintf("%s/contents/community/reports?ref=%s", s.apiBase, s.branch)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+s.token)
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitcode list HTTP %d", resp.StatusCode)
	}
	var files []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	// 限制并发读取, 避免给 GitCode API 造成突发压力。
	sem := make(chan struct{}, 8)
	var mu sync.Mutex
	var reports []reportDocument
	var wg sync.WaitGroup
	for _, f := range files {
		if !strings.HasSuffix(f.Name, ".json") || f.DownloadURL == "" {
			continue
		}
		wg.Add(1)
go func(downloadURL string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
				req.Header.Set("Authorization", "Bearer "+s.token)
				req.Header.Set("Cache-Control", "no-cache")
				req.Header.Set("Pragma", "no-cache")
			r, err := s.client.Do(req)
			if err != nil {
				return
			}
			defer r.Body.Close()
			if r.StatusCode < 200 || r.StatusCode >= 300 {
				return
			}
			var doc reportDocument
			if json.NewDecoder(r.Body).Decode(&doc) != nil {
				return
			}
			if doc.ID != "" {
				mu.Lock()
				reports = append(reports, doc)
				mu.Unlock()
			}
		}(f.DownloadURL)
	}
	wg.Wait()
	return reports, nil
}

// WriteArchive 将每日 TOP10 快照写入 community/archive/{date}.json。
func (s *gitCodeStore) WriteArchive(ctx context.Context, date string, data []byte) error {
		// 先尝试 GET 获取 sha (已存在则 PUT, 不存在则 POST)
		url := fmt.Sprintf("%s/contents/community/archive/%s.json?ref=%s", s.apiBase, date, s.branch)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+s.token)
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	sha := ""
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var payload struct {
			SHA string `json:"sha"`
		}
		json.NewDecoder(resp.Body).Decode(&payload)
		sha = payload.SHA
	}
	resp.Body.Close()

	payload := map[string]interface{}{
		"message": "community: archive " + date,
		"content": base64.StdEncoding.EncodeToString(data),
		"branch":  s.branch,
	}
	method := http.MethodPost
	if sha != "" {
		method = http.MethodPut
		payload["sha"] = sha
	}
	body, _ := json.Marshal(payload)
	url = fmt.Sprintf("%s/contents/community/archive/%s.json", s.apiBase, date)
	req, _ = http.NewRequestWithContext(ctx, method, url, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitcode archive write HTTP %d", resp.StatusCode)
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

	// 每日北京时间 23:55 自动归档当天 TOP10 排行榜快照。
	go func() {
		for {
			now := time.Now()
			next := nextCommunityArchiveTime(now)
			time.Sleep(next.Sub(now))
			if err := handler.runArchive(); err != nil {
				log.Printf("[-] daily archive failed: %v", err)
			} else {
				log.Printf("[+] daily archive completed for %s", communityArchiveDate(time.Now()))
			}
		}
	}()

	log.Fatal(server.ListenAndServe())
}
