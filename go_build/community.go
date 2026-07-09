// Token Monitor 社区功能模块 (Go, 跨平台)
// v1.4.11: 匿名 ID + opt-in + 上报 + 聚合
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
	"strings"
	"time"
)

const (
	gitcodeCommunityAPI = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"
	communityReportsPath = "community/reports"
)

var (
	communityCache = make(map[string]interface{})
	communityCacheTs int64
)

// getUserID 获取或生成匿名用户 ID (User_XXXXX)
func getUserID() string {
	communityDir := filepath.Join(os.Getenv("HOME"), ".token_monitor")
	if communityDir == "" {
		communityDir = filepath.Join(os.Getenv("USERPROFILE"), ".token_monitor")
	}
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
	communityDir := filepath.Join(os.Getenv("HOME"), ".token_monitor")
	if communityDir == "" {
		communityDir = filepath.Join(os.Getenv("USERPROFILE"), ".token_monitor")
	}
	data, err := os.ReadFile(filepath.Join(communityDir, "community_optin.txt"))
	if err != nil {
		return true // 默认开启
	}
	return strings.TrimSpace(strings.ToLower(string(data))) != "false"
}

// setOptIn 设置 opt-in
func setOptIn(enabled bool) {
	communityDir := filepath.Join(os.Getenv("HOME"), ".token_monitor")
	if communityDir == "" {
		communityDir = filepath.Join(os.Getenv("USERPROFILE"), ".token_monitor")
	}
	os.MkdirAll(communityDir, 0755)
	val := "false"
	if enabled {
		val = "true"
	}
	os.WriteFile(filepath.Join(communityDir, "community_optin.txt"), []byte(val), 0644)
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

// reportCommunityStats 上报当前用户统计到 GitCode
func reportCommunityStats(usage *UsageResponse) bool {
	if !isOptedIn() {
		return false
	}
	token := getGitcodeToken()
	if token == "" {
		return false
	}
	uid := getUserID()

	// 构建上报数据
	byTool := make(map[string]int64)
	for tool, stats := range usage.ByTool {
		byTool[tool] = stats.TotalTokens
	}
	totalTokens := int64(0)
	if usage.Summary != nil {
		if v, ok := usage.Summary["total_tokens"].(float64); ok {
			totalTokens = int64(v)
		}
	}
	report := map[string]interface{}{
		"id":           uid,
		"updated_at":   time.Now().UTC().Format("2006-01-02T15:04:05Z"),
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
		"branch":  "main",
	}
	if sha != "" {
		payload["sha"] = sha
	}
	result := gitcodePut(filePath, payload, token)
	return result != nil
}

// getCommunityStats 获取社区聚合统计 (带缓存)
func getCommunityStats() map[string]interface{} {
	// 缓存 5 分钟
	now := time.Now().Unix()
	if communityCache != nil && (now-communityCacheTs) < 300 {
		result := map[string]interface{}{}
		for k, v := range communityCache {
			result[k] = v
		}
		result["opted_in"] = isOptedIn()
		result["my_id"] = getUserID()
		return result
	}

	token := getGitcodeToken()
	if token == "" {
		return map[string]interface{}{
			"error":               "无法获取 GitCode 凭据",
			"opted_in":            isOptedIn(),
			"my_id":               getUserID(),
			"total_users":         0,
			"total_tokens_today":  0,
			"leaderboard":         []interface{}{},
			"tool_distribution":   map[string]interface{}{},
		}
	}

	// GET reports 目录列表
	url := gitcodeCommunityAPI + "/contents/" + communityReportsPath
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]interface{}{"error": "获取目录失败: " + err.Error()}
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var files []map[string]interface{}
	json.Unmarshal(body, &files)

	// 批量读取每个用户的 report
	type reportData struct {
		ID          string `json:"id"`
		TodayTokens int64  `json:"today_tokens"`
		ByTool      map[string]int64 `json:"by_tool"`
	}
	var reports []reportData
	myID := getUserID()
	for i, f := range files {
		if i >= 200 { break }
		dlURL, _ := f["download_url"].(string)
		if dlURL == "" {
			dlURL, _ = f["url"].(string)
		}
		if dlURL == "" { continue }
		req2, _ := http.NewRequest("GET", dlURL, nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2, err := client.Do(req2)
		if err != nil { continue }
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		var r reportData
		if json.Unmarshal(body2, &r) == nil && r.ID != "" {
			reports = append(reports, r)
		}
	}

	// 聚合
	totalTokensToday := int64(0)
	for _, r := range reports {
		totalTokensToday += r.TodayTokens
	}
	// 排行榜
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].TodayTokens > reports[j].TodayTokens
	})
	leaderboard := []map[string]interface{}{}
	myRank := 0
	for i, r := range reports {
		if i >= 10 { break }
		topTool := "?"
		var maxT int64
		for t, v := range r.ByTool {
			if v > maxT { maxT = v; topTool = t }
		}
		entry := map[string]interface{}{
			"id":     r.ID,
			"tokens": r.TodayTokens,
			"tool":   topTool,
			"is_me":  r.ID == myID,
		}
		leaderboard = append(leaderboard, entry)
		if r.ID == myID {
			myRank = i + 1
		}
	}
	// 工具占比
	toolTotals := map[string]int64{}
	for _, r := range reports {
		for t, v := range r.ByTool {
			toolTotals[t] += v
		}
	}
	totalToolTokens := int64(1)
	for _, v := range toolTotals { totalToolTokens += v }
	toolDist := map[string]float64{}
	for t, v := range toolTotals {
		toolDist[t] = math.Round(float64(v)/float64(totalToolTokens)*1000) / 10
	}
	// 趣味统计
	warPeace := float64(totalTokensToday) / 580000
	funFacts := map[string]interface{}{
		"war_and_peace_reads":      int(math.Floor(warPeace)),
		"wikipedia_multiple":        math.Round(float64(totalTokensToday)/4e9*10) / 10,
		"estimated_cost_saved":     math.Round(float64(totalTokensToday)*0.000002*100) / 100,
	}

	result := map[string]interface{}{
		"total_users":         len(reports),
		"total_tokens_today":  totalTokensToday,
		"total_tokens_all":    totalTokensToday * 30,
		"leaderboard":         leaderboard,
		"tool_distribution":   toolDist,
		"active_hours":        []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"my_rank":             myRank,
		"fun_facts":           funFacts,
		"updated_at":          time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"opted_in":            isOptedIn(),
		"my_id":               myID,
	}
	communityCache = map[string]interface{}{
		"total_users":        result["total_users"],
		"total_tokens_today": result["total_tokens_today"],
		"total_tokens_all":   result["total_tokens_all"],
		"leaderboard":        result["leaderboard"],
		"tool_distribution":  result["tool_distribution"],
		"active_hours":       result["active_hours"],
		"my_rank":            result["my_rank"],
		"fun_facts":          result["fun_facts"],
		"updated_at":         result["updated_at"],
	}
	communityCacheTs = now
	return result
}

// gitcodeGet GET GitCode API
func gitcodeGet(path, token string) interface{} {
	url := gitcodeCommunityAPI + "/contents/" + path
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return nil }
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var result interface{}
	json.Unmarshal(body, &result)
	return result
}

// gitcodePut PUT GitCode API
func gitcodePut(path string, data map[string]interface{}, token string) interface{} {
	url := gitcodeCommunityAPI + "/contents/" + path
	body, _ := json.Marshal(data)
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return nil }
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var result interface{}
	json.Unmarshal(respBody, &result)
	return result
}
