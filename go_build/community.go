// Token Monitor 社区功能模块 (Go, 跨平台)
// 匿名 ID + 上报 + 聚合 + 排名
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	gitcodeCommunityAPI  = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"
	communityReportsPath = "community/reports"
	communityDataBranch  = "community-data"
)

var (
	communityCache   = make(map[string]interface{})
	communityCacheTs int64
	communityCacheMu sync.Mutex
)

const communityLeaderboardLimit = 10

type CommunityReportResult struct {
	OK         bool   `json:"ok"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	ReportedAt string `json:"reported_at,omitempty"`
}

func getCommunityDir() string {
	return filepath.Join(homeDir(), ".token_monitor")
}

// getUserID 获取或生成匿名用户 ID (User_XXXXX)
func getUserID() string {
	communityDir := getCommunityDir()
	idFile := filepath.Join(communityDir, "community_id.txt")
	if data, err := os.ReadFile(idFile); err == nil {
		uid := strings.TrimSpace(string(data))
		if uid != "" {
			return uid
		}
	}
	// 生成新 ID
	os.MkdirAll(communityDir, 0755)
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	suffix := make([]byte, 5)
	for i := range suffix {
		suffix[i] = chars[time.Now().UnixNano()%int64(len(chars))]
		time.Sleep(1 * time.Nanosecond)
	}
	uid := "User_" + string(suffix)
	os.WriteFile(idFile, []byte(uid), 0644)
	return uid
}

// isOptedIn 检查 opt-in 状态 (v1.4.12: 默认开启, 用户量小先自动收集)
func isOptedIn() bool {
	communityDir := getCommunityDir()
	data, err := os.ReadFile(filepath.Join(communityDir, "community_optin.txt"))
	if err != nil {
		return true // 默认开启
	}
	return strings.TrimSpace(strings.ToLower(string(data))) != "false"
}

// setOptIn 设置 opt-in
func setOptIn(enabled bool) {
	communityDir := getCommunityDir()
	os.MkdirAll(communityDir, 0755)
	val := "false"
	if enabled {
		val = "true"
	}
	os.WriteFile(filepath.Join(communityDir, "community_optin.txt"), []byte(val), 0644)
	invalidateCommunityCache()
}

// getGitcodeToken 从 git credential 获取 GitCode token
func getGitcodeToken() string {
	cmd := exec.Command("git", "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=gitcode.com\n\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "password=") {
			return strings.TrimPrefix(line, "password=")
		}
	}
	return ""
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

func communityWriteMethod(sha string) string {
	if sha == "" {
		return http.MethodPost
	}
	return http.MethodPut
}

func invalidateCommunityCache() {
	communityCacheMu.Lock()
	communityCache = make(map[string]interface{})
	communityCacheTs = 0
	communityCacheMu.Unlock()
}

// reportCommunityStats 上报当前用户统计到 GitCode
func reportCommunityStats(usage *UsageResponse) CommunityReportResult {
	if !isOptedIn() {
		return CommunityReportResult{OK: false, Status: "disabled", Message: "社区数据上报未开启"}
	}
	token := getGitcodeToken()
	if token == "" {
		return CommunityReportResult{OK: false, Status: "credential_missing", Message: "本机未配置 GitCode 凭据，无法提交匿名统计"}
	}
	uid := getUserID()

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
	reportedAt := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	report := map[string]interface{}{
		"id":           uid,
		"updated_at":   reportedAt,
		"report_date":  reportDate,
		"today_tokens": totalTokens,
		"by_tool":      byTool,
		"tool_count":   len(byTool),
		"version":      appVersion,
	}
	content, _ := json.Marshal(report)
	contentB64 := base64.StdEncoding.EncodeToString(content)
	filePath := communityReportsPath + "/" + uid + ".json"

	// 先 GET 看文件是否存在 (拿 sha)
	sha := ""
	existing := gitcodeGet(filePath, token)
	if m, ok := existing.(map[string]interface{}); ok {
		if s, ok := m["sha"].(string); ok {
			sha = s
		}
	}

	// PUT 上传
	payload := map[string]interface{}{
		"message": "community: " + uid + " report",
		"content": contentB64,
		"branch":  communityDataBranch,
	}
	if sha != "" {
		payload["sha"] = sha
	}
	method := communityWriteMethod(sha)
	_, statusCode, err := gitcodeWrite(method, filePath, payload, token)
	if err != nil || statusCode < 200 || statusCode >= 300 {
		message := "匿名统计提交失败"
		if err != nil {
			message += "：" + err.Error()
		} else {
			message += ": HTTP " + http.StatusText(statusCode)
		}
		return CommunityReportResult{OK: false, Status: "upload_failed", Message: message}
	}

	invalidateCommunityCache()
	return CommunityReportResult{OK: true, Status: "synced", Message: "匿名统计已同步", ReportedAt: reportedAt}
}

// getCommunityStats 获取社区聚合统计 (带缓存)
func getCommunityStats() map[string]interface{} {
	// 缓存 5 分钟
	now := time.Now().Unix()
	communityCacheMu.Lock()
	if len(communityCache) > 0 && (now-communityCacheTs) < 300 {
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

	token := getGitcodeToken()

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
			"opted_in": isOptedIn(), "can_report": token != "", "my_id": getUserID(),
			"total_users": 0, "total_tokens_today": 0,
			"leaderboard": []interface{}{}, "tool_distribution": map[string]interface{}{},
		}
	}
	body, _ := json.Marshal(listing)
	var files []map[string]interface{}
	if json.Unmarshal(body, &files) != nil {
		return map[string]interface{}{
			"error": "社区数据读取失败：目录响应格式异常", "data_status": "load_failed",
			"opted_in": isOptedIn(), "can_report": token != "", "my_id": getUserID(),
			"total_users": 0, "total_tokens_today": 0,
			"leaderboard": []interface{}{}, "tool_distribution": map[string]interface{}{},
		}
	}
	client := &http.Client{Timeout: 8 * time.Second}

	// 批量读取每个用户的 report
	type reportData struct {
		ID          string           `json:"id"`
		UpdatedAt   string           `json:"updated_at"`
		ReportDate  string           `json:"report_date"`
		TodayTokens int64            `json:"today_tokens"`
		ByTool      map[string]int64 `json:"by_tool"`
	}
	var reports []reportData
	readFailures := 0
	reportFileCount := 0
	myID := getUserID()
	for _, f := range files {
		name, _ := f["name"].(string)
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		reportFileCount++
		if reportFileCount > 200 {
			break
		}
		dlURL, _ := f["download_url"].(string)
		if dlURL == "" {
			dlURL, _ = f["url"].(string)
		}
		if dlURL == "" {
			readFailures++
			continue
		}
		req2, _ := http.NewRequest("GET", dlURL, nil)
		if token != "" {
			req2.Header.Set("Authorization", "Bearer "+token)
		}
		resp2, err := client.Do(req2)
		if err != nil {
			readFailures++
			continue
		}
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
			readFailures++
			continue
		}
		var r reportData
		if json.Unmarshal(body2, &r) == nil && r.ID != "" {
			reports = append(reports, r)
		} else {
			readFailures++
		}
	}
	if reportFileCount > 0 && len(reports) == 0 {
		return map[string]interface{}{
			"error": "社区报告存在，但本次全部读取失败，请稍后重试", "data_status": "load_failed",
			"opted_in": isOptedIn(), "can_report": token != "", "my_id": getUserID(),
			"total_users": 0, "total_tokens_today": 0,
			"leaderboard": []interface{}{}, "tool_distribution": map[string]interface{}{},
		}
	}

	// 只聚合今天的报告，避免离线用户昨天的数据被算进今天。
	today := time.Now().Format("2006-01-02")
	reportDay := func(r reportData) string {
		if r.ReportDate != "" {
			return r.ReportDate
		}
		if len(r.UpdatedAt) >= 10 {
			return r.UpdatedAt[:10]
		}
		return ""
	}
	var reportsToday []reportData
	for _, r := range reports {
		if reportDay(r) == today {
			reportsToday = append(reportsToday, r)
		}
	}

	// 聚合
	totalTokensToday := int64(0)
	for _, r := range reportsToday {
		totalTokensToday += r.TodayTokens
	}
	// 排名在全部今日参与者中计算，榜单仅展示前 10。
	sort.Slice(reportsToday, func(i, j int) bool {
		return reportsToday[i].TodayTokens > reportsToday[j].TodayTokens
	})
	leaderboard := []map[string]interface{}{}
	myRank := 0
	for i, r := range reportsToday {
		if r.ID == myID {
			myRank = i + 1
		}
		if i >= communityLeaderboardLimit {
			continue
		}
		topTool := "?"
		var maxT int64
		for t, v := range r.ByTool {
			if v > maxT {
				maxT = v
				topTool = t
			}
		}
		entry := map[string]interface{}{
			"id":     r.ID,
			"tokens": r.TodayTokens,
			"tool":   topTool,
			"is_me":  r.ID == myID,
		}
		leaderboard = append(leaderboard, entry)
	}
	// 工具占比
	toolTotals := map[string]int64{}
	for _, r := range reportsToday {
		for t, v := range r.ByTool {
			toolTotals[t] += v
		}
	}
	totalToolTokens := int64(0)
	for _, v := range toolTotals {
		totalToolTokens += v
	}
	if totalToolTokens == 0 {
		totalToolTokens = 1
	}
	toolDist := map[string]float64{}
	for t, v := range toolTotals {
		toolDist[t] = math.Round(float64(v)/float64(totalToolTokens)*1000) / 10
	}
	// 趣味统计
	warPeace := float64(totalTokensToday) / 580000
	funFacts := map[string]interface{}{
		"war_and_peace_reads":  int(math.Floor(warPeace)),
		"wikipedia_multiple":   math.Round(float64(totalTokensToday)/4e9*10) / 10,
		"estimated_cost_saved": math.Round(float64(totalTokensToday)*0.000002*100) / 100,
	}

	var myReport *reportData
	for i := range reports {
		if reports[i].ID == myID {
			myReport = &reports[i]
			break
		}
	}
	mySyncedToday := myReport != nil && reportDay(*myReport) == today
	myTokens := int64(0)
	myLastSyncedAt := ""
	if myReport != nil {
		myLastSyncedAt = myReport.UpdatedAt
		if mySyncedToday {
			myTokens = myReport.TodayTokens
		}
	}
	rankStatus := "pending"
	rankMessage := "等待今日首次同步"
	if !isOptedIn() {
		rankStatus, rankMessage = "disabled", "数据上报未开启"
	} else if mySyncedToday && myRank > 0 && myRank <= communityLeaderboardLimit {
		rankStatus, rankMessage = "ranked", "今日第 "+strconv.Itoa(myRank)+" 名"
	} else if mySyncedToday {
		rankStatus = "outside_top10"
		rankMessage = "已同步，当前第 " + strconv.Itoa(myRank) + " 名（榜单展示前 " + strconv.Itoa(communityLeaderboardLimit) + "）"
	} else if token == "" {
		rankStatus, rankMessage = "credential_missing", "仅可查看：本机未配置 GitCode 上报凭据"
	}

	dataStatus := "empty"
	if len(reportsToday) > 0 {
		dataStatus = "ok"
	}
	dataWarning := ""
	if readFailures > 0 {
		dataStatus = "partial"
		dataWarning = "有 " + strconv.Itoa(readFailures) + " 份社区报告读取失败，当前统计可能不完整"
	}

	result := map[string]interface{}{
		"total_users":          len(reportsToday),
		"all_reporters":        len(reports),
		"total_tokens_today":   totalTokensToday,
		"total_tokens_all":     totalTokensToday * 30,
		"projected_30d_tokens": totalTokensToday * 30,
		"leaderboard":          leaderboard,
		"tool_distribution":    toolDist,
		"active_hours":         []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"my_rank":              myRank,
		"my_tokens":            myTokens,
		"my_synced_today":      mySyncedToday,
		"my_report_found":      myReport != nil,
		"my_last_synced_at":    myLastSyncedAt,
		"rank_status":          rankStatus,
		"rank_message":         rankMessage,
		"rank_total":           len(reportsToday),
		"leaderboard_limit":    communityLeaderboardLimit,
		"can_report":           token != "",
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
	client := &http.Client{Timeout: 15 * time.Second}
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

// gitcodeWrite 使用 POST 创建文件、PUT 更新文件。
func gitcodeWrite(method, path string, data map[string]interface{}, token string) (interface{}, int, error) {
	url := gitcodeCommunityAPI + "/contents/" + path
	body, _ := json.Marshal(data)
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
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
