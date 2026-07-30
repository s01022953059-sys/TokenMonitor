// Token Monitor 社区功能模块 (Go, 跨平台)
// 匿名 ID + 上报 + 聚合 + 排名
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	gitcodeCommunityAPI   = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"
	communityReportsPath  = "community/reports"
	communityDataBranch   = "community-data"
	communityRelayDefault = "https://new.taqi.cc/token-monitor-community/v1/report"
)

var (
	communityCache   = make(map[string]interface{})
	communityCacheTs int64
	communityCacheMu sync.Mutex
	historyCache     = make(map[int]communityHistoryCacheEntry)
	historyCacheMu   sync.Mutex
)

const (
	communityLeaderboardLimit = 10
	communitySyncInterval     = 5 * time.Minute
)

// formatCommunityTools lists every tool with non-zero usage, ordered by usage.
func formatCommunityTools(byTool map[string]int64) string {
	type toolUsage struct {
		name   string
		tokens int64
	}
	items := make([]toolUsage, 0, len(byTool))
	for name, tokens := range byTool {
		if tokens > 0 {
			items = append(items, toolUsage{name: name, tokens: tokens})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].tokens != items[j].tokens {
			return items[i].tokens > items[j].tokens
		}
		return items[i].name < items[j].name
	})
	if len(items) == 0 {
		return "?"
	}
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = item.name
	}
	return strings.Join(names, " + ")
}

func communityMemberNames(reports []communityReportData) map[string]string {
	names := map[string]string{}
	for _, report := range reports {
		id := strings.TrimSpace(report.ID)
		name := strings.TrimSpace(report.DisplayName)
		if id != "" && name != "" {
			names[id] = name
		}
	}
	return names
}

// buildToolDistributionJSON 把 by-tool 用量聚合转成 "tool -> pct" 的 JSON object 字符串,
// 按 pct 降序、tool 名升序 tiebreak, 序列化后手动维持该顺序 (encoding/json 对 map 按 key 字母序, 会丢失排序)。
// 抽出来便于单测, 对齐 Python 端 community._format_report_tools 与
// community.py tool_distribution dict 排序语义, 修复 a5c08b4。
func buildToolDistributionJSON(toolTotals map[string]int64) json.RawMessage {
	totalToolTokens := int64(0)
	for _, v := range toolTotals {
		totalToolTokens += v
	}
	if totalToolTokens == 0 {
		totalToolTokens = 1
	}
	type toolDistEntry struct {
		Tool string
		Pct  float64
	}
	pct := map[string]float64{}
	for t, v := range toolTotals {
		pct[t] = math.Round(float64(v)/float64(totalToolTokens)*1000) / 10
	}
	var sorted []toolDistEntry
	for t, v := range pct {
		sorted = append(sorted, toolDistEntry{Tool: t, Pct: v})
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Pct != sorted[j].Pct {
			return sorted[i].Pct > sorted[j].Pct
		}
		return sorted[i].Tool < sorted[j].Tool
	})
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range sorted {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(e.Tool)
		buf.Write(key)
		buf.WriteByte(':')
		val, _ := json.Marshal(e.Pct)
		buf.Write(val)
	}
	buf.WriteByte('}')
	return json.RawMessage(buf.Bytes())
}

type CommunityReportResult struct {
	OK         bool   `json:"ok"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	ReportedAt string `json:"reported_at,omitempty"`
}

type CommunityProfileResult struct {
	OK           bool   `json:"ok"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	NextChangeAt string `json:"next_change_at,omitempty"`
	Unchanged    bool   `json:"unchanged,omitempty"`
}

type communityCredential struct {
	ID           string `json:"id"`
	DeviceSecret string `json:"device_secret"`
}

type communityRelayResponse struct {
	OK         bool   `json:"ok"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	ReportedAt string `json:"reported_at"`
}

func getCommunityDir() string {
	return filepath.Join(homeDir(), ".token_monitor")
}

func newCommunityID() string {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		fallback := strings.ToUpper(strconv.FormatInt(time.Now().UnixNano(), 36))
		if len(fallback) > 8 {
			fallback = fallback[len(fallback)-8:]
		} else if len(fallback) < 8 {
			fallback = strings.Repeat("0", 8-len(fallback)) + fallback
		}
		return "User_" + fallback
	}
	return "User_" + strings.ToUpper(hex.EncodeToString(random))
}

type communityReportData struct {
	ID            string           `json:"id"`
	AuthHash      string           `json:"auth_hash"`
	ReplacesID    string           `json:"replaces_id"`
	DisplayName   string           `json:"display_name"`
	NameChangedAt string           `json:"name_changed_at"`
	UpdatedAt     string           `json:"updated_at"`
	ReportDate    string           `json:"report_date"`
	TodayTokens   int64            `json:"today_tokens"`
	ByTool        map[string]int64 `json:"by_tool"`
}

func communityReportFingerprint(report communityReportData) string {
	day := report.ReportDate
	if day == "" && len(report.UpdatedAt) >= 10 {
		day = report.UpdatedAt[:10]
	}
	tools := make([]string, 0, len(report.ByTool))
	for tool, tokens := range report.ByTool {
		tools = append(tools, tool+"="+strconv.FormatInt(tokens, 10))
	}
	sort.Strings(tools)
	return day + "|" + strconv.FormatInt(report.TodayTokens, 10) + "|" + strings.Join(tools, ",")
}

func dedupeLegacyIdentityReports(reports []communityReportData) []communityReportData {
	replacedIDs := make(map[string]bool)
	authenticated := make(map[string]bool)
	for _, report := range reports {
		if strings.TrimSpace(report.AuthHash) != "" {
			authenticated[communityReportFingerprint(report)] = true
			if strings.TrimSpace(report.ReplacesID) != "" {
				replacedIDs[report.ReplacesID] = true
			}
		}
	}
	result := make([]communityReportData, 0, len(reports))
	for _, report := range reports {
		if replacedIDs[report.ID] {
			continue
		}
		if strings.TrimSpace(report.AuthHash) == "" && authenticated[communityReportFingerprint(report)] {
			continue
		}
		result = append(result, report)
	}
	return result
}

func communityReportRecency(report communityReportData) string {
	// ISO 8601 时间字符串可直接按字典序比较；旧报告没有更新时间时退回报告日期。
	return report.UpdatedAt + "|" + report.ReportDate
}

func dedupeCommunityReportsByID(reports []communityReportData) []communityReportData {
	latestIndex := make(map[string]int)
	for index, report := range reports {
		id := strings.TrimSpace(report.ID)
		if id == "" {
			continue
		}
		previousIndex, exists := latestIndex[id]
		if !exists || communityReportRecency(report) >= communityReportRecency(reports[previousIndex]) {
			latestIndex[id] = index
		}
	}
	result := make([]communityReportData, 0, len(latestIndex))
	for index, report := range reports {
		if latestIndex[strings.TrimSpace(report.ID)] == index {
			result = append(result, report)
		}
	}
	return result
}

func activeCommunityReports(reports []communityReportData) []communityReportData {
	active := make([]communityReportData, 0, len(reports))
	for _, report := range reports {
		if report.TodayTokens > 0 {
			active = append(active, report)
		}
	}
	return active
}

// getUserID 获取或生成匿名用户 ID
func getUserID() string {
	communityDir := getCommunityDir()
	idFile := filepath.Join(communityDir, "community_id.txt")
	if data, err := os.ReadFile(idFile); err == nil {
		uid := strings.TrimSpace(string(data))
		if uid != "" {
			return uid
		}
	}
	os.MkdirAll(communityDir, 0755)
	uid := newCommunityID()
	os.WriteFile(idFile, []byte(uid), 0644)
	return uid
}

func writeCommunityCredential(credential communityCredential) error {
	communityDir := getCommunityDir()
	if err := os.MkdirAll(communityDir, 0755); err != nil {
		return err
	}
	body, _ := json.Marshal(credential)
	tmp := filepath.Join(communityDir, "community_credential.json.tmp")
	path := filepath.Join(communityDir, "community_credential.json")
	if err := os.WriteFile(tmp, body, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func getCommunityCredential() communityCredential {
	uid := getUserID()
	path := filepath.Join(getCommunityDir(), "community_credential.json")
	if body, err := os.ReadFile(path); err == nil {
		var credential communityCredential
		if json.Unmarshal(body, &credential) == nil {
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(credential.DeviceSecret)
			if credential.ID == uid && decodeErr == nil && len(decoded) == 32 {
				return credential
			}
		}
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	credential := communityCredential{ID: uid, DeviceSecret: base64.RawURLEncoding.EncodeToString(secret)}
	_ = writeCommunityCredential(credential)
	return credential
}

func rotateCommunityIdentity() communityCredential {
	uid := newCommunityID()
	_ = os.MkdirAll(getCommunityDir(), 0755)
	_ = os.WriteFile(filepath.Join(getCommunityDir(), "community_id.txt"), []byte(uid), 0644)
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	credential := communityCredential{ID: uid, DeviceSecret: base64.RawURLEncoding.EncodeToString(secret)}
	_ = writeCommunityCredential(credential)
	return credential
}

func communityRelayURL() string {
	if value := strings.TrimSpace(os.Getenv("TOKEN_MONITOR_COMMUNITY_RELAY_URL")); value != "" {
		return value
	}
	return communityRelayDefault
}

func communityReportingEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TOKEN_MONITOR_DISABLE_COMMUNITY_REPORT")))
	return value != "1" && value != "true" && value != "yes"
}

func communityProfileURL() string {
	value := communityRelayURL()
	if strings.HasSuffix(value, "/v1/report") {
		return strings.TrimSuffix(value, "/v1/report") + "/v1/profile"
	}
	return strings.TrimRight(value, "/") + "/v1/profile"
}

// isOptedIn 保留旧接口兼容；社区统计随安装自动启用。
func isOptedIn() bool {
	return true
}

// setOptIn 保留旧 API 兼容，但不再允许关闭自动社区统计。
func setOptIn(enabled bool) {
	communityDir := getCommunityDir()
	os.MkdirAll(communityDir, 0755)
	os.WriteFile(filepath.Join(communityDir, "community_optin.txt"), []byte("true"), 0644)
	invalidateCommunityCache()
}

func communityInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func invalidateCommunityCache() {
	communityCacheMu.Lock()
	communityCache = make(map[string]interface{})
	communityCacheTs = 0
	communityCacheMu.Unlock()
}

func sendCommunityRelay(report map[string]interface{}) communityRelayResponse {
	body, _ := json.Marshal(report)
	req, err := http.NewRequest(http.MethodPost, communityRelayURL(), bytes.NewReader(body))
	if err != nil {
		return communityRelayResponse{Status: "relay_unavailable", Message: "社区中继地址无效"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "TokenMonitor/"+appVersion)
	client := newProxyHTTPClient(20)
	resp, err := client.Do(req)
	if err != nil {
		return communityRelayResponse{Status: "relay_unavailable", Message: "社区中继暂时不可用：" + err.Error()}
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var result communityRelayResponse
	if json.Unmarshal(responseBody, &result) != nil {
		return communityRelayResponse{Status: "relay_invalid_response", Message: "社区中继返回格式异常"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.OK = false
		if result.Status == "" {
			result.Status = "relay_http_error"
		}
		if result.Message == "" {
			result.Message = "社区中继请求失败"
		}
	}
	return result
}

// reportCommunityStats 通过 VPS 中继上报匿名统计
func reportCommunityStats(usage *UsageResponse) CommunityReportResult {
	credential := getCommunityCredential()

	// 构建上报数据
	byTool := make(map[string]int64)
	for tool, stats := range usage.ByTool {
		byTool[tool] = stats.TotalTokens
	}
	totalTokens := int64(0)
	reportDate := time.Now().Format("2006-01-02")
	if usage.Summary != nil {
		totalTokens = communityInt64(usage.Summary["total_tokens"])
		if v, ok := usage.Summary["date"].(string); ok && v != "" {
			reportDate = v
		}
	}
	report := map[string]interface{}{
		"id":            credential.ID,
		"device_secret": credential.DeviceSecret,
		"report_date":   reportDate,
		"today_tokens":  totalTokens,
		"by_tool":       byTool,
		"version":       appVersion,
	}
	result := sendCommunityRelay(report)
	if result.Status == "identity_upgrade_required" {
		previousID := credential.ID
		credential = rotateCommunityIdentity()
		report["id"] = credential.ID
		report["device_secret"] = credential.DeviceSecret
		report["replaces_id"] = previousID
		result = sendCommunityRelay(report)
	}
	if !result.OK {
		return CommunityReportResult{OK: false, Status: result.Status, Message: result.Message}
	}

	invalidateCommunityCache()
	return CommunityReportResult{OK: true, Status: "synced", Message: result.Message, ReportedAt: result.ReportedAt}
}

func updateCommunityProfile(displayName string) CommunityProfileResult {
	credential := getCommunityCredential()
	payload := map[string]interface{}{
		"id": credential.ID, "device_secret": credential.DeviceSecret, "display_name": displayName,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, communityProfileURL(), bytes.NewReader(body))
	if err != nil {
		return CommunityProfileResult{Status: "relay_unavailable", Message: "昵称服务地址无效"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "TokenMonitor/"+appVersion)
	resp, err := newProxyHTTPClient(20).Do(req)
	if err != nil {
		return CommunityProfileResult{Status: "relay_unavailable", Message: "昵称服务暂时不可用：" + err.Error()}
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var result CommunityProfileResult
	if json.Unmarshal(responseBody, &result) != nil {
		return CommunityProfileResult{Status: "relay_invalid_response", Message: "昵称服务返回格式异常"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.OK = false
	}
	if result.OK {
		invalidateCommunityCache()
	}
	return result
}

// getCommunityStats 获取社区聚合统计 (带缓存)
func getCommunityStats(forceRefresh bool) map[string]interface{} {
	// 缓存 5 分钟
	now := time.Now().Unix()
	communityCacheMu.Lock()
	if !forceRefresh && len(communityCache) > 0 && (now-communityCacheTs) < 300 {
		result := map[string]interface{}{}
		for k, v := range communityCache {
			result[k] = v
		}
		communityCacheMu.Unlock()
		result["opted_in"] = isOptedIn()
		result["my_id"] = getUserID()
		if !isOptedIn() {
			result["rank_status"] = "disabled"
			result["rank_message"] = "数据上报未开启"
		}
		return result
	}
	communityCacheMu.Unlock()

	token := ""

	// 公开仓库读取不要求每位用户都配置 GitCode 凭据。
	listing, statusCode, err := gitcodeGetDetailed(communityReportsPath, token)
	if err != nil || statusCode < 200 || statusCode >= 300 {
		message := "社区数据读取失败"
		if err != nil {
			message += "：" + err.Error()
		} else {
			message += ": HTTP " + http.StatusText(statusCode)
		}
		return map[string]interface{}{
			"error": message, "data_status": "load_failed",
			"opted_in": isOptedIn(), "can_report": communityRelayURL() != "", "my_id": getUserID(),
			"total_users": 0, "today_active_users": 0, "all_reporters": 0, "total_tokens_today": 0,
			"leaderboard": []interface{}{}, "tool_distribution": map[string]interface{}{},
		}
	}
	body, _ := json.Marshal(listing)
	var files []map[string]interface{}
	if json.Unmarshal(body, &files) != nil {
		return map[string]interface{}{
			"error": "社区数据读取失败：目录响应格式异常", "data_status": "load_failed",
			"opted_in": isOptedIn(), "can_report": communityRelayURL() != "", "my_id": getUserID(),
			"total_users": 0, "today_active_users": 0, "all_reporters": 0, "total_tokens_today": 0,
			"leaderboard": []interface{}{}, "tool_distribution": map[string]interface{}{},
		}
	}
	client := newProxyHTTPClient(8)

	// GitCode 每份报告是独立文件，使用有限并发避免页面耗时随用户数线性增长。
	var reportURLs []string
	myID := getUserID()
	for _, f := range files {
		name, _ := f["name"].(string)
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if len(reportURLs) >= 200 {
			break
		}
		dlURL, _ := f["download_url"].(string)
		if dlURL == "" {
			dlURL, _ = f["url"].(string)
		}
		if dlURL == "" {
			continue
		}
		reportURLs = append(reportURLs, dlURL)
	}
	type reportReadResult struct {
		report communityReportData
		ok     bool
	}
	results := make(chan reportReadResult, len(reportURLs))
	workers := make(chan struct{}, 8)
	for _, reportURL := range reportURLs {
		go func(downloadURL string) {
			workers <- struct{}{}
			defer func() { <-workers }()
			req, _ := http.NewRequest(http.MethodGet, downloadURL, nil)
			resp, err := client.Do(req)
			if err != nil {
				results <- reportReadResult{}
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var report communityReportData
			ok := resp.StatusCode >= 200 && resp.StatusCode < 300 && json.Unmarshal(body, &report) == nil && report.ID != ""
			results <- reportReadResult{report: report, ok: ok}
		}(reportURL)
	}
	var reports []communityReportData
	readFailures := 0
	for range reportURLs {
		result := <-results
		if result.ok {
			reports = append(reports, result.report)
		} else {
			readFailures++
		}
	}
	if len(reportURLs) > 0 && len(reports) == 0 {
		return map[string]interface{}{
			"error": "社区报告存在，但本次全部读取失败，请稍后重试", "data_status": "load_failed",
			"opted_in": isOptedIn(), "can_report": token != "", "my_id": getUserID(),
			"total_users": 0, "today_active_users": 0, "all_reporters": 0, "total_tokens_today": 0,
			"leaderboard": []interface{}{}, "tool_distribution": map[string]interface{}{},
		}
	}
	reports = dedupeCommunityReportsByID(dedupeLegacyIdentityReports(reports))

	// 只聚合今天的报告，避免离线用户昨天的数据被算进今天。
	today := time.Now().Format("2006-01-02")
	reportDay := func(r communityReportData) string {
		if r.ReportDate != "" {
			return r.ReportDate
		}
		if len(r.UpdatedAt) >= 10 {
			return r.UpdatedAt[:10]
		}
		return ""
	}
	var reportsToday []communityReportData
	for _, r := range reports {
		if reportDay(r) == today {
			reportsToday = append(reportsToday, r)
		}
	}
	// Zero-token startup reports preserve historical membership but never rank today.
	activeReports := activeCommunityReports(reportsToday)

	// 聚合
	totalTokensToday := int64(0)
	for _, r := range activeReports {
		totalTokensToday += r.TodayTokens
	}
	// 排名在全部今日参与者中计算，榜单仅展示前 10。
	sort.SliceStable(activeReports, func(i, j int) bool {
		return activeReports[i].TodayTokens > activeReports[j].TodayTokens
	})
	leaderboard := []map[string]interface{}{}
	myRank := 0
	for i, r := range activeReports {
		if r.ID == myID {
			myRank = i + 1
		}
		if i >= communityLeaderboardLimit {
			continue
		}
		entry := map[string]interface{}{
			"id":           r.ID,
			"display_name": r.DisplayName,
			"tokens":       r.TodayTokens,
			"tool":         formatCommunityTools(r.ByTool),
			"is_me":        r.ID == myID,
		}
		leaderboard = append(leaderboard, entry)
	}
	// 工具占比
	toolTotals := map[string]int64{}
	for _, r := range activeReports {
		for t, v := range r.ByTool {
			toolTotals[t] += v
		}
	}
	toolDistJSON := buildToolDistributionJSON(toolTotals)
	// 趣味统计
	warPeace := float64(totalTokensToday) / 580000
	funFacts := map[string]interface{}{
		"war_and_peace_reads":  int(math.Floor(warPeace)),
		"wikipedia_multiple":   math.Round(float64(totalTokensToday)/4e9*10) / 10,
		"estimated_cost_saved": math.Round(float64(totalTokensToday)*0.000002*100) / 100,
	}

	var myReport *communityReportData
	for i := range reports {
		if reports[i].ID == myID {
			myReport = &reports[i]
			break
		}
	}
	mySyncedToday := myReport != nil && reportDay(*myReport) == today
	myTokens := int64(0)
	myLastSyncedAt := ""
	myDisplayName := ""
	myNameChangedAt := ""
	if myReport != nil {
		myLastSyncedAt = myReport.UpdatedAt
		myDisplayName = myReport.DisplayName
		myNameChangedAt = myReport.NameChangedAt
		if mySyncedToday {
			myTokens = myReport.TodayTokens
		}
	}
	rankStatus := "pending"
	rankMessage := "今日数据准备中"
	if !isOptedIn() {
		rankStatus, rankMessage = "disabled", "数据上报未开启"
	} else if mySyncedToday && myTokens > 0 && myRank > 0 && myRank <= communityLeaderboardLimit {
		rankStatus, rankMessage = "ranked", "今日第 "+strconv.Itoa(myRank)+" 名"
	} else if mySyncedToday && myTokens > 0 {
		rankStatus = "outside_top10"
		rankMessage = "当前第 " + strconv.Itoa(myRank) + " 名（榜单展示前 " + strconv.Itoa(communityLeaderboardLimit) + "）"
	}

	dataStatus := "empty"
	if len(activeReports) > 0 {
		dataStatus = "ok"
	}
	dataWarning := ""
	if readFailures > 0 {
		dataStatus = "partial"
		dataWarning = "有 " + strconv.Itoa(readFailures) + " 份社区报告读取失败，当前统计可能不完整"
	}

	result := map[string]interface{}{
		"total_users":          len(reports),
		"today_active_users":   len(activeReports),
		"all_reporters":        len(reports),
		"total_tokens_today":   totalTokensToday,
		"total_tokens_all":     totalTokensToday * 30,
		"projected_30d_tokens": totalTokensToday * 30,
		"leaderboard":          leaderboard,
		"member_names":         communityMemberNames(reports),
		"tool_distribution":    toolDistJSON,
		"active_hours":         []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"my_rank":              myRank,
		"my_tokens":            myTokens,
		"my_synced_today":      mySyncedToday,
		"my_report_found":      myReport != nil,
		"my_last_synced_at":    myLastSyncedAt,
		"my_display_name":      myDisplayName,
		"my_name_changed_at":   myNameChangedAt,
		"rank_status":          rankStatus,
		"rank_message":         rankMessage,
		"rank_total":           len(activeReports),
		"leaderboard_limit":    communityLeaderboardLimit,
		"can_report":           communityRelayURL() != "",
		"data_status":          dataStatus,
		"data_warning":         dataWarning,
		"fun_facts":            funFacts,
		"updated_at":           time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"opted_in":             isOptedIn(),
		"my_id":                myID,
	}
	communityCacheMu.Lock()
	communityCache = map[string]interface{}{}
	for k, v := range result {
		communityCache[k] = v
	}
	communityCacheTs = now
	communityCacheMu.Unlock()
	return result
}

// gitcodeGet GET GitCode API
func gitcodeGet(path, token string) interface{} {
	result, _, _ := gitcodeGetDetailed(path, token)
	return result
}

func gitcodeGetDetailed(path, token string) (interface{}, int, error) {
	url := gitcodeCommunityAPI + "/contents/" + path + "?ref=" + communityDataBranch
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := newProxyHTTPClient(15)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, resp.StatusCode, err
	}
	return result, resp.StatusCode, nil
}

type communityHistoryEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Tokens      int64  `json:"tokens"`
	Tool        string `json:"tool,omitempty"`
}

type communityHistorySnapshot struct {
	Date         string                  `json:"date"`
	GeneratedAt  string                  `json:"generated_at,omitempty"`
	TotalUsers   int                     `json:"total_users,omitempty"`
	ActiveUsers  int                     `json:"active_users,omitempty"`
	Participants []communityHistoryEntry `json:"participants,omitempty"`
	Leaderboard  []communityHistoryEntry `json:"leaderboard"`
}

type communityArchiveFile struct {
	name string
	url  string
}

type communityHistoryCacheEntry struct {
	data map[string]interface{}
	ts   time.Time
}

type communityRankSeries struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name,omitempty"`
	TotalTokens int64   `json:"total_tokens"`
	Appearances int     `json:"appearances"`
	Ranks       []int   `json:"ranks"`
	Tokens      []int64 `json:"tokens"`
}

func buildCommunityRankSeries(snapshots []communityHistorySnapshot) map[string]interface{} {
	sort.SliceStable(snapshots, func(i, j int) bool { return snapshots[i].Date < snapshots[j].Date })
	participantCountComplete := true
	type memberTotal struct {
		id, name    string
		tokens      int64
		appearances int
	}
	totals := map[string]*memberTotal{}
	dailyTokens := make([]map[string]int64, len(snapshots))
	dates := make([]string, len(snapshots))

	for dayIndex, snapshot := range snapshots {
		if snapshot.Participants == nil {
			participantCountComplete = false
		}
		dates[dayIndex] = snapshot.Date
		entries := snapshot.Participants
		if len(entries) == 0 {
			entries = snapshot.Leaderboard
		}
		byID := map[string]communityHistoryEntry{}
		for _, entry := range entries {
			id := strings.TrimSpace(entry.ID)
			if id == "" {
				id = strings.TrimSpace(entry.DisplayName)
			}
			if id == "" {
				continue
			}
			entry.ID = id
			if entry.Tokens < 0 {
				entry.Tokens = 0
			}
			previous, exists := byID[id]
			if !exists || entry.Tokens >= previous.Tokens {
				byID[id] = entry
			}
		}
		dailyTokens[dayIndex] = map[string]int64{}
		for _, entry := range byID {
			dailyTokens[dayIndex][entry.ID] = entry.Tokens
			member := totals[entry.ID]
			if member == nil {
				member = &memberTotal{id: entry.ID, name: entry.DisplayName}
				totals[entry.ID] = member
			}
			if entry.DisplayName != "" {
				member.name = entry.DisplayName
			}
			member.tokens += entry.Tokens
			member.appearances++
		}
	}

	members := make([]*memberTotal, 0, len(totals))
	for _, member := range totals {
		members = append(members, member)
	}
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].tokens != members[j].tokens {
			return members[i].tokens > members[j].tokens
		}
		if members[i].appearances != members[j].appearances {
			return members[i].appearances > members[j].appearances
		}
		left := members[i].name
		if left == "" {
			left = members[i].id
		}
		right := members[j].name
		if right == "" {
			right = members[j].id
		}
		if left != right {
			return left < right
		}
		return members[i].id < members[j].id
	})
	if len(members) > 10 {
		members = members[:10]
	}
	series := make([]communityRankSeries, len(members))
	historicalTokens := map[string]int64{}
	historicalActiveDays := map[string]int{}
	for index, member := range members {
		series[index] = communityRankSeries{ID: member.id, DisplayName: member.name, TotalTokens: member.tokens, Appearances: member.appearances, Ranks: make([]int, len(snapshots)), Tokens: make([]int64, len(snapshots))}
	}
	for dayIndex := range snapshots {
		order := make([]int, len(series))
		for index := range series {
			order[index] = index
			series[index].Tokens[dayIndex] = dailyTokens[dayIndex][series[index].ID]
		}
		sort.SliceStable(order, func(i, j int) bool {
			left := series[order[i]]
			right := series[order[j]]
			leftTokens := left.Tokens[dayIndex]
			rightTokens := right.Tokens[dayIndex]
			if leftTokens != rightTokens {
				return leftTokens > rightTokens
			}
			if leftTokens == 0 {
				if historicalTokens[left.ID] != historicalTokens[right.ID] {
					return historicalTokens[left.ID] > historicalTokens[right.ID]
				}
				if historicalActiveDays[left.ID] != historicalActiveDays[right.ID] {
					return historicalActiveDays[left.ID] > historicalActiveDays[right.ID]
				}
			}
			leftName := left.DisplayName
			if leftName == "" {
				leftName = left.ID
			}
			rightName := right.DisplayName
			if rightName == "" {
				rightName = right.ID
			}
			if leftName != rightName {
				return leftName < rightName
			}
			return left.ID < right.ID
		})
		for rank, seriesIndex := range order {
			series[seriesIndex].Ranks[dayIndex] = rank + 1
		}
		for index := range series {
			tokens := series[index].Tokens[dayIndex]
			historicalTokens[series[index].ID] += tokens
			if tokens > 0 {
				historicalActiveDays[series[index].ID]++
			}
		}
	}
	return map[string]interface{}{"dates": dates, "series": series, "participant_count": len(totals), "participant_count_complete": participantCountComplete}
}

func fetchCommunityHistorySnapshots(files []communityArchiveFile, maxWorkers int, fetch func(string) (communityHistorySnapshot, error)) []communityHistorySnapshot {
	if len(files) == 0 {
		return nil
	}
	if maxWorkers <= 0 || maxWorkers > len(files) {
		maxWorkers = len(files)
	}
	type fetchJob struct {
		index int
		file  communityArchiveFile
	}
	type fetchResult struct {
		snapshot communityHistorySnapshot
		ok       bool
	}
	jobs := make(chan fetchJob)
	results := make([]fetchResult, len(files))
	var workers sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				snapshot, err := fetch(job.file.url)
				if err == nil && snapshot.Date != "" {
					results[job.index] = fetchResult{snapshot: snapshot, ok: true}
				}
			}
		}()
	}
	for index, file := range files {
		jobs <- fetchJob{index: index, file: file}
	}
	close(jobs)
	workers.Wait()

	snapshots := make([]communityHistorySnapshot, 0, len(files))
	for _, result := range results {
		if result.ok {
			snapshots = append(snapshots, result.snapshot)
		}
	}
	sort.SliceStable(snapshots, func(i, j int) bool { return snapshots[i].Date < snapshots[j].Date })
	return snapshots
}

// getCommunityHistory 读取 community/archive/ 目录下的每日排名快照。
func getCommunityHistory(days int) map[string]interface{} {
	if days <= 0 {
		days = 30
	}
	historyCacheMu.Lock()
	cached, hasCached := historyCache[days]
	historyCacheMu.Unlock()
	if hasCached && time.Since(cached.ts) < 5*time.Minute {
		return cached.data
	}
	token := ""
	listing, statusCode, err := gitcodeGetDetailed("community/archive", token)
	if err != nil || statusCode < 200 || statusCode >= 300 {
		return map[string]interface{}{"snapshots": []interface{}{}, "dates": []string{}, "series": []communityRankSeries{}, "participant_count": 0, "participant_count_complete": true, "data_status": "empty"}
	}
	body, _ := json.Marshal(listing)
	var files []map[string]interface{}
	if json.Unmarshal(body, &files) != nil {
		return map[string]interface{}{"snapshots": []interface{}{}, "dates": []string{}, "series": []communityRankSeries{}, "participant_count": 0, "participant_count_complete": true, "data_status": "empty"}
	}

	// 筛选 .json 文件, 按文件名(日期)降序取最近 N 天
	var archiveFiles []communityArchiveFile
	for _, f := range files {
		name, _ := f["name"].(string)
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		dlURL, _ := f["download_url"].(string)
		if dlURL == "" {
			dlURL, _ = f["url"].(string)
		}
		if dlURL == "" {
			continue
		}
		archiveFiles = append(archiveFiles, communityArchiveFile{name: name, url: dlURL})
	}
	// 按文件名降序 (新→旧)
	sort.SliceStable(archiveFiles, func(i, j int) bool {
		return archiveFiles[i].name > archiveFiles[j].name
	})
	if len(archiveFiles) > days {
		archiveFiles = archiveFiles[:days]
	}

	client := newProxyHTTPClient(8)
	snapshots := fetchCommunityHistorySnapshots(archiveFiles, 8, func(url string) (communityHistorySnapshot, error) {
		req, _ := http.NewRequest("GET", url, nil)
		resp, err := client.Do(req)
		if err != nil {
			return communityHistorySnapshot{}, err
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return communityHistorySnapshot{}, io.ErrUnexpectedEOF
		}
		var snapshot communityHistorySnapshot
		if err := json.Unmarshal(respBody, &snapshot); err != nil {
			return communityHistorySnapshot{}, err
		}
		return snapshot, nil
	})

	status := "ok"
	if len(snapshots) == 0 {
		status = "empty"
	}
	result := buildCommunityRankSeries(snapshots)
	result["snapshots"] = snapshots
	result["data_status"] = status
	historyCacheMu.Lock()
	historyCache[days] = communityHistoryCacheEntry{data: result, ts: time.Now()}
	historyCacheMu.Unlock()
	return result
}
func gitcodeWrite(method, path string, data map[string]interface{}, token string) (interface{}, int, error) {
	url := gitcodeCommunityAPI + "/contents/" + path
	body, _ := json.Marshal(data)
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := newProxyHTTPClient(15)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var result interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, resp.StatusCode, err
	}
	return result, resp.StatusCode, nil
}
