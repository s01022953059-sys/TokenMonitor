package main

import (
	"bufio"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"


	_ "modernc.org/sqlite"
)

//go:embed static/*
var staticFS embed.FS

const defaultPort = 15723
const updateFeedURL = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor/releases/latest"

// 版本号: 优先从同目录 version.txt 读取 (打包时写入), 回退到编译时注入的常量。
// 这和 Python 版从 Info.plist 读版本号的思路一致: 让运行时能拿到真实版本。
var appVersion = "1.3.71"

// feedURL 在 main() 里从命令行参数解析, 默认用 updateFeedURL。
// 提升为包级变量让 checkUpdateRemote 能访问 (对齐 Python 版的全局 UPDATE_FEED_URL)。
var feedURL = updateFeedURL

// ───── 数据结构 (与 Python 版 JSON 输出完全对齐) ─────

type LogEntry struct {
	Time          string `json:"time"`
	Timestamp     int64  `json:"timestamp"`
	Tool          string `json:"tool"`
	Model         string `json:"model"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	TotalTokens   int64  `json:"total_tokens"`
	InputCached   int64  `json:"input_cached"`
	InputUncached int64  `json:"input_uncached"`
	SessionID     string `json:"session_id"`
}

type ToolStats struct {
	TotalTokens  int64 `json:"total_tokens"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type BalanceInfo struct {
	Balance  string `json:"balance"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

type UsageResponse struct {
	Summary      map[string]interface{} `json:"summary"`
	ByTool       map[string]*ToolStats  `json:"by_tool"`
	ByModel      map[string]int64       `json:"by_model"`
	RecentEvents []LogEntry             `json:"recent_events"`
}

type HistoryResponse struct {
	Labels  []string           `json:"labels"`
	Values  []int64            `json:"values"`
	ByTool  map[string][]int64 `json:"by_tool"`
	ByModel map[string][]int64 `json:"by_model"`
}

type AppInfoResponse struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	UpdateFeedURL string `json:"update_feed_url"`
	UpdateEnabled bool   `json:"update_enabled"`
}

type SessionEntry struct {
	Timestamp     int64  `json:"timestamp"`
	Time          string `json:"time"`
	Tool          string `json:"tool"`
	Model         string `json:"model"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	TotalTokens   int64  `json:"total_tokens"`
	InputCached   int64  `json:"input_cached"`
	InputUncached int64  `json:"input_uncached"`
	LatencyMs     int64  `json:"latency_ms"`
	SessionID     string `json:"session_id"`
}

type SessionListResponse struct {
	Sessions   []SessionEntry      `json:"sessions"`
	Total      int                 `json:"total"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	TotalPages int                 `json:"total_pages"`
	Summary    map[string]interface{} `json:"summary,omitempty"`
}

type HeatmapDay struct {
	Date    string `json:"date"`
	Label   string `json:"label"`
	Weekday int    `json:"weekday"`
	Month   int    `json:"month"`
	Tokens  int64  `json:"tokens"`
}

type HeatmapResponse struct {
	Days      []HeatmapDay `json:"days"`
	MaxValue  int64        `json:"max_value"`
	StartDate string       `json:"start_date"`
	EndDate   string       `json:"end_date"`
}

// ───── 路径工具 (跨平台) ─────

func homeDir() string {
	h, _ := os.UserHomeDir()
	if h == "" {
		h, _ = os.UserHomeDir()
	}
	return h
}

func ccSwitchDBPath() string {
	return filepath.Join(homeDir(), ".cc-switch", "cc-switch.db")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hermesDBPath() string {
	return filepath.Join(homeDir(), ".hermes", "state.db")
}

func antigravityStatsPath() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(homeDir(), "Library", "Application Support", "BingchaAI", "usage_stats.json")
	}
	// Windows: Antigravity 可能不存在, 但路径留着以防万一
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = filepath.Join(homeDir(), "AppData", "Roaming")
	}
	return filepath.Join(appData, "BingchaAI", "usage_stats.json")
}

func todayMidnight() int64 {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return midnight.Unix()
}

// ───── 模型名归一化 (对齐 scanner.py normalize_model_name) ─────

var dateSuffixRe = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

func normalizeModelName(rawModel string) string {
	if rawModel == "" {
		return "Other"
	}
	s := strings.ToLower(strings.TrimSpace(rawModel))
	// 去掉 -YYYY-MM-DD 日期后缀
	s = dateSuffixRe.ReplaceAllString(s, "")
	// 别名表
	aliases := map[string]string{
		"qwen3.6-plus":    "qwen3.6-plus",
		"qwen3.6-plus-vl": "qwen3.6-plus-vl",
		"qwen3.7-plus":    "qwen3.7-plus",
		"qwen3.7-max":     "qwen3.7-max",
	}
	if v, ok := aliases[s]; ok {
		return v
	}
	// 启发式: qwen3.6-plus 家族折叠 (排除 vl 变体)
	if strings.HasPrefix(s, "qwen3.6-plus") && s != "qwen3.6-plus-vl" {
		return "qwen3.6-plus"
	}
	return s
}

// ───── DeepSeek 余额 (对齐 scanner.py get_deepseek_balance) ─────
// 语义匹配: provider_type / name / app_type 任一字段含 deepseek

var (
	balanceCache = BalanceInfo{"0.00", "CNY", "Loading..."}
	balanceMu    sync.RWMutex
)

func getCachedBalance() BalanceInfo {
	balanceMu.RLock()
	defer balanceMu.RUnlock()
	return balanceCache
}

func startBalanceRefresher() {
	go func() {
		refreshBalance()
		ticker := time.NewTicker(60 * time.Second)
		for range ticker.C {
			refreshBalance()
		}
	}()
}

func refreshBalance() {
	b := fetchDeepSeekBalance()
	balanceMu.Lock()
	balanceCache = b
	balanceMu.Unlock()
}

func fetchDeepSeekBalance() BalanceInfo {
	dbPath := ccSwitchDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return BalanceInfo{"0.00", "CNY", "Offline"}
	}
	// modernc.org/sqlite 不支持 ?mode=ro URI, 用只读方式打开
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return BalanceInfo{"0.00", "CNY", fmt.Sprintf("Error: %v", err)}
	}
	defer db.Close()

	// 语义匹配: provider_type / name / app_type 任一字段含 deepseek (大小写不敏感)
	rows, err := db.Query(`
		SELECT id, settings_config FROM providers
		WHERE LOWER(COALESCE(provider_type, '')) LIKE '%deepseek%'
		   OR LOWER(COALESCE(name, '')) LIKE '%deepseek%'
		   OR LOWER(COALESCE(app_type, '')) LIKE '%deepseek%'
	`)
	if err != nil {
		return BalanceInfo{"0.00", "CNY", fmt.Sprintf("Error: %v", err)}
	}
	defer rows.Close()

	var apiKey string
	for rows.Next() {
		var id string
		var cfgRaw sql.NullString
		rows.Scan(&id, &cfgRaw)
		if !cfgRaw.Valid || cfgRaw.String == "" {
			continue
		}
		var cfg map[string]interface{}
		if json.Unmarshal([]byte(cfgRaw.String), &cfg) != nil {
			continue
		}
		if k, ok := cfg["apiKey"].(string); ok && k != "" {
			apiKey = k
			break
		}
		if k, ok := cfg["api_key"].(string); ok && k != "" {
			apiKey = k
			break
		}
	}

	if apiKey == "" {
		return BalanceInfo{"0.00", "CNY", "No Key"}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.deepseek.com/user/balance", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return BalanceInfo{"0.00", "CNY", fmt.Sprintf("Error: %v", err)}
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if avail, ok := result["is_available"].(bool); ok && avail {
		if infos, ok := result["balance_infos"].([]interface{}); ok && len(infos) > 0 {
			if info, ok := infos[0].(map[string]interface{}); ok {
				bal, _ := info["total_balance"].(string)
				cur, _ := info["currency"].(string)
				if cur == "" {
					cur = "CNY"
				}
				if bal == "" {
					bal = "0.00"
				}
				return BalanceInfo{bal, cur, "Active"}
			}
		}
	}
	return BalanceInfo{"0.00", "CNY", "Unknown"}
}

// ───── 三源扫描 (对齐 scanner.py) ─────

// 1. cc-switch
func scanCCSwitchLogs(todayStart int64) []LogEntry {
	dbPath := ccSwitchDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("[-] 扫描 cc-switch 数据库出错: %v\n", err)
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT created_at, app_type, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, session_id
		FROM proxy_request_logs
		WHERE created_at >= ? AND status_code = 200
		ORDER BY created_at ASC
	`, todayStart)
	if err != nil {
		fmt.Printf("[-] 扫描 cc-switch 数据库出错: %v\n", err)
		return nil
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var createdAt int64
		var appType, model sql.NullString
		var inputT, outputT, cacheReadT, cacheCreationT sql.NullInt64
		var sessionID sql.NullString
		rows.Scan(&createdAt, &appType, &model, &inputT, &outputT, &cacheReadT, &cacheCreationT, &sessionID)

		iT := inputT.Int64
		oT := outputT.Int64
		// 跟 Python _normalize_app_type 保持一致: cache = cache_read + cache_creation,
		// 但 OpenAI 兼容协议的 input_t 已含 cache 部分, 这里 cap 防双计
		cRaw := cacheReadT.Int64 + cacheCreationT.Int64
		var cT, uncached int64
		if cRaw > iT {
			cT = iT
			uncached = 0
		} else {
			cT = cRaw
			uncached = iT - cT
		}
		totalT := iT + oT

		// 工具归一化 (跟 Python _normalize_app_type 保持一致, 避免首页/详情名字不一致)
		tool := normalizeAppTypeForCCSwitch(appType.String)

		m := "Other"
		if model.Valid && model.String != "" {
			m = normalizeModelName(model.String)
		}

		logs = append(logs, LogEntry{
			Time:          time.Unix(createdAt, 0).Format("15:04:05"),
			Timestamp:     createdAt,
			Tool:          tool,
			Model:         m,
			InputTokens:   iT,
			OutputTokens:  oT,
			TotalTokens:   totalT,
			InputCached:   cT,
			InputUncached: uncached,
			SessionID:     sessionID.String,
		})
	}
	return logs
}

// 2. 冰茶 AI (Antigravity 旧名, 用户反馈"我应该没有使用 Antigravity" 因为
//    不认识 Antigravity 跟冰茶 AI 是同一客户端. 改工具名让统计更直观)
func scanAntigravityTokens() []LogEntry {
	statsPath := antigravityStatsPath()
	data, err := os.ReadFile(statsPath)
	if err != nil {
		return nil
	}

	var stats struct {
		Records map[string]struct {
			InputTokens  int64 `json:"inputTokens"`
			OutputTokens int64 `json:"outputTokens"`
			CachedTokens int64 `json:"cachedTokens"`
			ByModel      map[string]struct {
				ModelKey string `json:"modelKey"`
			} `json:"byModel"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &stats); err != nil {
		fmt.Printf("[-] 读取冰茶 AI 统计文件出错: %v\n", err)
		return nil
	}

	todayStr := time.Now().Format("2006-01-02")
	record, ok := stats.Records[todayStr]
	if !ok {
		return nil
	}

	totalT := record.InputTokens + record.OutputTokens
	uncached := record.InputTokens - record.CachedTokens
	if uncached < 0 {
		uncached = 0
	}

	// model 字段不再写死 "Gemini 3.5 Flash", 改为 byModel 第一名
	// (冰茶 AI 客户端实际跑的多是 gpt-5.5 / gpt-5.4-mini, 真实模型)
	modelName := "Gemini 3.5 Flash"
	for m := range record.ByModel {
		modelName = m
		break
	}

	return []LogEntry{{
		Time:          "实时",
		Timestamp:     time.Now().Unix(),
		Tool:          "冰茶 AI",
		Model:         modelName,
		InputTokens:   record.InputTokens,
		OutputTokens:  record.OutputTokens,
		TotalTokens:   totalT,
		InputCached:   record.CachedTokens,
		InputUncached: uncached,
	}}
}

// 3. Hermes
func scanHermesTokens(todayStart int64) []LogEntry {
	dbPath := hermesDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("[-] 扫描 Hermes 数据库出错: %v\n", err)
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT started_at, model, input_tokens, output_tokens, cache_read_tokens
		FROM sessions WHERE started_at >= ? ORDER BY started_at ASC
	`, todayStart)
	if err != nil {
		fmt.Printf("[-] 扫描 Hermes 数据库出错: %v\n", err)
		return nil
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var startedAt int64
		var model sql.NullString
		var inputT, outputT, cacheReadT sql.NullInt64
		rows.Scan(&startedAt, &model, &inputT, &outputT, &cacheReadT)

		// Python: Hermes 的 input_t 是未缓存部分, cache_read_t 是已缓存部分
		iCached := cacheReadT.Int64
		iUncached := inputT.Int64
		totalInput := iUncached + iCached
		oT := outputT.Int64
		totalT := totalInput + oT

		m := "Unknown"
		if model.Valid && model.String != "" {
			m = normalizeModelName(model.String)
		}

		logs = append(logs, LogEntry{
			Time:          time.Unix(startedAt, 0).Format("15:04:05"),
			Timestamp:     startedAt,
			Tool:          "Hermes",
			Model:         m,
			InputTokens:   totalInput,
			OutputTokens:  oT,
			TotalTokens:   totalT,
			InputCached:   iCached,
			InputUncached: iUncached,
		})
	}
	return logs
}

// ───── 跨源去重 (对齐 scanner.py _dedup_events) ─────

const dedupWindowSeconds = 2

type dedupKey struct {
	Bucket     int64
	ModelLower string
	TotalToken int64
}

func dedupEvents(events []LogEntry) []LogEntry {
	if len(events) == 0 {
		return nil
	}
	// 按时间戳升序, 保证同 key 时保留最早的
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})
	seen := make(map[dedupKey]bool)
	var deduped []LogEntry
	for _, ev := range events {
		bucket := ev.Timestamp / dedupWindowSeconds
		key := dedupKey{
			Bucket:     bucket,
			ModelLower: strings.ToLower(ev.Model),
			TotalToken: ev.TotalTokens,
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, ev)
	}
	return deduped
}

// ───── API: /api/usage (对齐 scanner.py get_today_usage) ─────

func getTodayUsage() UsageResponse {
	todayStart := todayMidnight()

	// 三源并行扫描
	var ccLogs, antigravityLogs, hermesLogs []LogEntry
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); ccLogs = scanCCSwitchLogs(todayStart) }()
	go func() { defer wg.Done(); antigravityLogs = scanAntigravityTokens() }()
	go func() { defer wg.Done(); hermesLogs = scanHermesTokens(todayStart) }()
	wg.Wait()

	// 合并去重
	eventsBeforeDedup := len(ccLogs) + len(antigravityLogs) + len(hermesLogs)
	allLogs := append(append(ccLogs, antigravityLogs...), hermesLogs...)
	allLogs = dedupEvents(allLogs)

	var totalTokens, inputTokens, outputTokens, inputCached, inputUncached int64
	byTool := map[string]*ToolStats{}
	byModel := map[string]int64{}

	for _, log := range allLogs {
		totalTokens += log.TotalTokens
		inputTokens += log.InputTokens
		outputTokens += log.OutputTokens
		inputCached += log.InputCached
		inputUncached += log.InputUncached

		if _, ok := byTool[log.Tool]; !ok {
			byTool[log.Tool] = &ToolStats{}
		}
		byTool[log.Tool].TotalTokens += log.TotalTokens
		byTool[log.Tool].InputTokens += log.InputTokens
		byTool[log.Tool].OutputTokens += log.OutputTokens

		byModel[log.Model] += log.TotalTokens
	}

	dsBalance := getCachedBalance()

	recentEvents := allLogs
	if len(recentEvents) > 30 {
		recentEvents = recentEvents[len(recentEvents)-30:]
	}

	return UsageResponse{
		Summary: map[string]interface{}{
			"total_tokens":         totalTokens,
			"input_tokens":         inputTokens,
			"output_tokens":        outputTokens,
			"input_cached":         inputCached,
			"input_uncached":       inputUncached,
			"date":                 time.Now().Format("2006-01-02"),
			"deepseek_balance":     dsBalance.Balance,
			"deepseek_currency":    dsBalance.Currency,
			"deepseek_status":      dsBalance.Status,
			"events_after_dedup":   len(allLogs),
			"events_before_dedup":  eventsBeforeDedup,
		},
		ByTool:       byTool,
		ByModel:      byModel,
		RecentEvents: recentEvents,
	}
}

// ───── API: /api/history (对齐 scanner.py get_historical_usage) ─────

func getNormalizedTool(appType string) string {
	if appType == "" {
		return "Other"
	}
	lower := strings.ToLower(appType)
	if strings.Contains(lower, "antigravity") {
		return "冰茶 AI"
	}
	if strings.Contains(lower, "hermes") {
		return "Hermes"
	}
	if strings.Contains(lower, "claude") {
		// 不区分 desktop / cli / code, 统一为 Claude
		return "Claude"
	}
	if strings.Contains(lower, "opencode") {
		return "OpenCode"
	}
	if strings.Contains(lower, "codex") {
		return "Codex"
	}
	return "Other"
}

// cc-switch 数据源的归一化: 历史代码用 app_type.capitalize() 给 "claude-desktop"
// → "Claude-desktop", 跟详情/会话列表里 "Other" 不一致。统一改成跟 Python
// _normalize_app_type 同名 (Claude-Desktop), 保证首页/详情字段一致。
func normalizeAppTypeForCCSwitch(appType string) string {
	if appType == "" {
		return "Other"
	}
	return getNormalizedTool(appType)
}

func getNormalizedModel(modelName string, models []string) string {
	if modelName == "" {
		return "Other"
	}
	lower := strings.ToLower(strings.TrimSpace(modelName))
	// 长前缀优先匹配
	for _, m := range models {
		if m != "Other" && strings.Contains(lower, m) {
			return m
		}
	}
	return "Other"
}

func getHistoricalUsage(days int) HistoryResponse {
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	startDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	startTimestamp := startDate.Unix()

	dateList := make([]string, days)
	for i := 0; i < days; i++ {
		d := startDate.AddDate(0, 0, i)
		dateList[i] = d.Format("2006-01-02")
	}

	// 与 Python 版完全一致的工具和模型列表
	tools := []string{"冰茶 AI", "Hermes", "Codex", "Other"}
	models := []string{"deepseek-v4-flash", "gemini 3.5 flash", "deepseek-v4-pro", "gpt-5.5", "deepseek-v4-flash-free", "Other"}

	dateIdx := map[string]int{}
	for i, d := range dateList {
		dateIdx[d] = i
	}

	toolData := map[string][]int64{}
	for _, t := range tools {
		toolData[t] = make([]int64, days)
	}
	modelData := map[string][]int64{}
	for _, m := range models {
		modelData[m] = make([]int64, days)
	}
	dailyTotals := make([]int64, days)

	// 1. cc-switch 历史
	dbPath := ccSwitchDBPath()
	if _, err := os.Stat(dbPath); err == nil {
		if db, err := sql.Open("sqlite", dbPath); err == nil {
			rows, err := db.Query(`
				SELECT created_at, input_tokens, output_tokens, app_type, model
				FROM proxy_request_logs WHERE created_at >= ? AND status_code = 200
			`, startTimestamp)
			if err == nil {
				for rows.Next() {
					var createdAt int64
					var inputT, outputT sql.NullInt64
					var appType, model sql.NullString
					rows.Scan(&createdAt, &inputT, &outputT, &appType, &model)

					dStr := time.Unix(createdAt, 0).Format("2006-01-02")
					idx, ok := dateIdx[dStr]
					if !ok {
						continue
					}
					tokens := inputT.Int64 + outputT.Int64
					dailyTotals[idx] += tokens
					tNorm := getNormalizedTool(appType.String)
					toolData[tNorm][idx] += tokens
					mNorm := getNormalizedModel(model.String, models)
					modelData[mNorm][idx] += tokens
				}
				rows.Close()
			}
			db.Close()
		}
	}

	// 2. Hermes 历史
	hermesPath := hermesDBPath()
	if _, err := os.Stat(hermesPath); err == nil {
		if db, err := sql.Open("sqlite", hermesPath); err == nil {
			rows, err := db.Query(`
				SELECT started_at, input_tokens, output_tokens, cache_read_tokens, model
				FROM sessions WHERE started_at >= ?
			`, startTimestamp)
			if err == nil {
				for rows.Next() {
					var startedAt int64
					var inputT, outputT, cacheReadT sql.NullInt64
					var model sql.NullString
					rows.Scan(&startedAt, &inputT, &outputT, &cacheReadT, &model)

					dStr := time.Unix(startedAt, 0).Format("2006-01-02")
					idx, ok := dateIdx[dStr]
					if !ok {
						continue
					}
					tokens := (inputT.Int64) + (cacheReadT.Int64) + (outputT.Int64)
					dailyTotals[idx] += tokens
					toolData["Hermes"][idx] += tokens
					mNorm := getNormalizedModel(model.String, models)
					modelData[mNorm][idx] += tokens
				}
				rows.Close()
			}
			db.Close()
		}
	}

	// 3. Antigravity 历史
	antigravityPath := antigravityStatsPath()
	if data, err := os.ReadFile(antigravityPath); err == nil {
		var stats struct {
			Records map[string]struct {
				InputTokens  int64 `json:"inputTokens"`
				OutputTokens int64 `json:"outputTokens"`
			} `json:"records"`
		}
		if json.Unmarshal(data, &stats) == nil {
			for _, dStr := range dateList {
				record, ok := stats.Records[dStr]
				if !ok {
					continue
				}
				tokens := record.InputTokens + record.OutputTokens
				idx := dateIdx[dStr]
				dailyTotals[idx] += tokens
				toolData["Antigravity"][idx] += tokens
				modelData["gemini 3.5 flash"][idx] += tokens
			}
		}
	}

	resTool := map[string][]int64{}
	for _, t := range tools {
		resTool[t] = toolData[t]
	}
	resModel := map[string][]int64{}
	for _, m := range models {
		resModel[m] = modelData[m]
	}

	return HistoryResponse{
		Labels:  dateList,
		Values:  dailyTotals,
		ByTool:  resTool,
		ByModel: resModel,
	}
}

// ───── API: /api/check-update (对齐 server.py _check_update_remote) ─────

func normalizeVersion(value string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(value), "vV "))
}

func parseVersionTuple(value string) []int {
	normalized := normalizeVersion(value)
	parts := strings.Split(normalized, ".")
	result := []int{}
	for _, p := range parts {
		digits := ""
		for _, ch := range p {
			if ch >= '0' && ch <= '9' {
				digits += string(ch)
			}
		}
		if digits == "" {
			digits = "0"
		}
		n := 0
		fmt.Sscanf(digits, "%d", &n)
		result = append(result, n)
	}
	return result
}

func compareVersions(latest, current string) int {
	a := parseVersionTuple(latest)
	b := parseVersionTuple(current)
	length := len(a)
	if len(b) > length {
		length = len(b)
	}
	for i := len(a); i < length; i++ {
		a = append(a, 0)
	}
	for i := len(b); i < length; i++ {
		b = append(b, 0)
	}
	for i := 0; i < length; i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}

func pickAssetURL(payload map[string]interface{}) string {
	var assetList []interface{}
	// 优先 assets, 回退 files
	if a, ok := payload["assets"].([]interface{}); ok {
		assetList = a
	} else if f, ok := payload["files"].([]interface{}); ok {
		assetList = f
	}
	if len(assetList) == 0 {
		return ""
	}
	// 两轮: 先找 .dmg/.zip, 再退到第一个
	suffixes := []string{".dmg", ".zip"}
	for _, suffix := range suffixes {
		for _, a := range assetList {
			if asset, ok := a.(map[string]interface{}); ok {
				name := strings.ToLower(fmt.Sprintf("%v", asset["name"]))
				if strings.HasSuffix(name, suffix) {
					return getAssetURL(asset)
				}
			}
		}
	}
	// 兜底: 第一个
	if asset, ok := assetList[0].(map[string]interface{}); ok {
		return getAssetURL(asset)
	}
	return ""
}

func getAssetURL(asset map[string]interface{}) string {
	for _, key := range []string{"browser_download_url", "download_url", "downloadUrl", "url", "html_url"} {
		if v, ok := asset[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func extractReleaseInfo(payload map[string]interface{}) (version, title, notes, downloadURL string, ok bool) {
	rawVersion := ""
	if v, ok := payload["version"]; ok {
		rawVersion = fmt.Sprintf("%v", v)
	} else if v, ok := payload["tag_name"]; ok {
		rawVersion = fmt.Sprintf("%v", v)
	} else if v, ok := payload["tagName"]; ok {
		rawVersion = fmt.Sprintf("%v", v)
	}
	version = normalizeVersion(rawVersion)
	if version == "" {
		return "", "", "", "", false
	}
	if t, ok := payload["title"]; ok {
		title = fmt.Sprintf("%v", t)
	} else if t, ok := payload["name"]; ok {
		title = fmt.Sprintf("%v", t)
	} else {
		title = "Token Monitor " + version
	}
	if n, ok := payload["notes"]; ok {
		notes = fmt.Sprintf("%v", n)
	} else if n, ok := payload["body"]; ok {
		notes = fmt.Sprintf("%v", n)
	}
	// download_url
	for _, key := range []string{"download_url", "downloadUrl", "html_url", "htmlUrl"} {
		if v, ok := payload[key].(string); ok && v != "" {
			downloadURL = v
			break
		}
	}
	if downloadURL == "" {
		downloadURL = pickAssetURL(payload)
	}
	return version, title, notes, downloadURL, true
}

func checkUpdateRemote() map[string]interface{} {
	// 每次请求重读版本号 (对齐 Python 版 _read_app_version() 每次调用都重读)
	currentVer := readAppVersion()

	result := map[string]interface{}{
		"ok":              false,
		"current_version": currentVer,
		"latest_version":  nil,
		"update_available": false,
		"feed_url":        feedURL,
		"http_status":     nil,
		"error":           nil,
		"raw_excerpt":     nil,
		"title":           nil,
		"download_url":    nil,
	}

	// 未配置更新源时直接返回错误 (对齐 Python 版)
	if feedURL == "" {
		result["error"] = "未配置更新源 (--update-feed-url 参数缺失)"
		return result
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest("GET", feedURL, nil)
	req.Header.Set("User-Agent", fmt.Sprintf("TokenMonitor/%s (+https://gitcode.com/baggiopeng/TokenMonitor)", currentVer))
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		result["error"] = fmt.Sprintf("网络错误: %v", err)
		return result
	}
	defer resp.Body.Close()

	result["http_status"] = resp.StatusCode

	// 读最多 64KB
	buf := make([]byte, 64*1024)
	n, _ := resp.Body.Read(buf)
	body := buf[:n]

	if resp.StatusCode != 200 {
		excerpt := string(body)
		if len(excerpt) > 512 {
			excerpt = excerpt[:512]
		}
		result["raw_excerpt"] = excerpt
		result["error"] = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		excerpt := string(body)
		if len(excerpt) > 512 {
			excerpt = excerpt[:512]
		}
		result["raw_excerpt"] = excerpt
		result["error"] = "更新源返回的内容不是 JSON"
		return result
	}

	version, title, _, downloadURL, ok := extractReleaseInfo(payload)
	if !ok {
		excerpt := string(body)
		if len(excerpt) > 512 {
			excerpt = excerpt[:512]
		}
		result["raw_excerpt"] = excerpt
		result["error"] = "更新源 JSON 中缺少版本字段"
		return result
	}

	result["latest_version"] = version
	result["title"] = title
	if downloadURL != "" {
		result["download_url"] = downloadURL
	} else {
		result["download_url"] = nil
	}
	result["update_available"] = compareVersions(version, currentVer) > 0
	result["ok"] = true
	return result
}

// ───── 版本号读取 (对齐 server.py _read_app_version) ─────

func readAppVersion() string {
	// Go 版: 优先从同目录 version.txt 读 (打包时写入)
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		versionFile := filepath.Join(dir, "version.txt")
		if data, err := os.ReadFile(versionFile); err == nil {
			v := strings.TrimSpace(string(data))
			if v != "" {
				return v
			}
		}
		// 也检查工作目录 (开发模式)
		versionFile = filepath.Join(".", "version.txt")
		if data, err := os.ReadFile(versionFile); err == nil {
			v := strings.TrimSpace(string(data))
			if v != "" {
				return v
			}
		}
	}
	// macOS: 尝试从 Info.plist 读
	for _, plistPath := range []string{
		filepath.Join(getExeDir(), "Info.plist"),
		"/Applications/Token Monitor.app/Contents/Info.plist",
		filepath.Join(homeDir(), "Applications", "Token Monitor.app", "Contents", "Info.plist"),
	} {
		if v := readVersionFromPlist(plistPath); v != "" {
			return v
		}
	}
	return appVersion
}

func getExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// 简易 plist 版本读取: 找 CFBundleShortVersionString 后面的 <string>
func readVersionFromPlist(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	idx := strings.Index(content, "CFBundleShortVersionString")
	if idx < 0 {
		return ""
	}
	rest := content[idx:]
	startTag := "<string>"
	startIdx := strings.Index(rest, startTag)
	if startIdx < 0 {
		return ""
	}
	startIdx += len(startTag)
	endIdx := strings.Index(rest[startIdx:], "</string>")
	if endIdx < 0 {
		return ""
	}
	return strings.TrimSpace(rest[startIdx : startIdx+endIdx])
}

// ───── 单实例锁 (跨平台) ─────


func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal(0) 仅探测进程是否存在, 不发实际信号
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

var singletonLockFile *os.File

func acquireSingletonLock() bool {
	lockPath := os.Getenv("TOKEN_MONITOR_LOCK_FILE")
	if lockPath == "" {
		lockPath = filepath.Join(os.TempDir(), "token_monitor_server.lock")
	}

	// 先检查锁文件里记录的 PID 是否还活着; 如果已死, 删除残留锁文件再重试。
	if data, err := os.ReadFile(lockPath); err == nil {
		var stalePid int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &stalePid); err == nil && stalePid > 0 {
			if !processAlive(stalePid) {
				os.Remove(lockPath)
			}
		}
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Printf("[server] 无法打开单实例锁文件 %s: %v\n", lockPath, err)
		return false
	}

	// 尝试非阻塞独占锁
	if err := tryLockFile(f); err != nil {
		f.Close()
		return false
	}

	f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	f.Sync()
	singletonLockFile = f
	return true
}

// ───── HTTP 服务器 ─────

// openBrowser 跨平台打开默认浏览器
func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		// cmd /c start 最可靠, 兼容所有 Windows 版本
		exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(statusCode)
	w.Write(body)
}

// ───── API: /api/sessions ─────
func getSessionList(days, page, pageSize int) SessionListResponse {
	now := time.Now()
	start := now.AddDate(0, 0, -days)
	startMidnight := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	startTimestamp := startMidnight.Unix()

	var ccLogs, hermesLogs []LogEntry
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ccLogs = scanCCSwitchLogs(startTimestamp) }()
	go func() { defer wg.Done(); hermesLogs = scanHermesTokens(startTimestamp) }()
	wg.Wait()

	allLogs := append(ccLogs, hermesLogs...)
	allLogs = dedupEvents(allLogs)

	sort.Slice(allLogs, func(i, j int) bool {
		return allLogs[i].Timestamp > allLogs[j].Timestamp
	})

	sessions := make([]SessionEntry, 0, len(allLogs))
	for _, ev := range allLogs {
		sessions = append(sessions, SessionEntry{
			Timestamp:     ev.Timestamp,
			Time:          ev.Time,
			Tool:          ev.Tool,
			Model:         ev.Model,
			InputTokens:   ev.InputTokens,
			OutputTokens:  ev.OutputTokens,
			TotalTokens:   ev.TotalTokens,
			InputCached:   ev.InputCached,
			InputUncached: ev.InputUncached,
			LatencyMs:     0,
			SessionID:     ev.SessionID,
		})
	}
	return SessionListResponse{Sessions: sessions, Total: len(sessions)}
}

// ───── API: /api/session_detail ─────
type SessionMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

type SessionDetailResponse struct {
	SessionID  string           `json:"session_id"`
	Messages   []SessionMessage `json:"messages"`
	Total      int              `json:"total"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	TotalPages int              `json:"total_pages"`
}

func getSessionDetail(sessionID string, page, pageSize int) SessionDetailResponse {
	resp := SessionDetailResponse{SessionID: sessionID, Messages: []SessionMessage{}}
	if sessionID == "" {
		return resp
	}

	sessionsDir := filepath.Join(homeDir(), ".codex", "sessions")
	var rolloutPath string
	filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".jsonl") && strings.Contains(info.Name(), sessionID) {
			rolloutPath = path
			return filepath.SkipDir
		}
		return nil
	})

	if rolloutPath == "" {
		return resp
	}

	file, err := os.Open(rolloutPath)
	if err != nil {
		return resp
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	maxMessages := 500
	for scanner.Scan() {
		if len(resp.Messages) >= maxMessages {
			break
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}

		objType, _ := obj["type"].(string)
		if objType != "response_item" {
			continue
		}

		payload, ok := obj["payload"].(map[string]interface{})
		if !ok {
			continue
		}

		role, _ := payload["role"].(string)
		if role != "user" && role != "assistant" {
			continue
		}

		content, _ := payload["content"]
		var text string
		switch c := content.(type) {
		case []interface{}:
			var parts []string
			for _, item := range c {
				if m, ok := item.(map[string]interface{}); ok {
					if t, ok := m["text"].(string); ok && t != "" {
						parts = append(parts, t)
					}
				} else if s, ok := item.(string); ok {
					parts = append(parts, s)
				}
			}
			text = strings.Join(parts, "\n")
		case string:
			text = c
		}

		if strings.TrimSpace(text) == "" {
			continue
		}

		if len(text) > 5000 {
			text = text[:5000] + "\n...(内容过长已截断)"
		}

		timestamp, _ := payload["timestamp"].(string)

		resp.Messages = append(resp.Messages, SessionMessage{
			Role:      role,
			Text:      text,
			Timestamp: timestamp,
		})
	}

	// 分页
	total := len(resp.Messages)
	resp.Total = total
	resp.Page = page
	resp.PageSize = pageSize
	if pageSize <= 0 {
		pageSize = 20
		resp.PageSize = 20
	}
	resp.TotalPages = (total + pageSize - 1) / pageSize
	if resp.TotalPages < 1 {
		resp.TotalPages = 1
	}
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	if start < total {
		resp.Messages = resp.Messages[start:end]
	} else {
		resp.Messages = []SessionMessage{}
	}
	return resp
}

// ───── API: /api/heatmap ─────
func getHeatmapData(days int) HeatmapResponse {
	now := time.Now()
	start := now.AddDate(0, 0, -(days - 1))
	startMidnight := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	startTimestamp := startMidnight.Unix()

	// daily tokens map
	dailyTokens := map[string]int64{}
	seen := map[string]bool{}

	if dbPath := ccSwitchDBPath(); fileExists(dbPath) {
		db, err := sql.Open("sqlite", dbPath)
		if err == nil {
			rows, err := db.Query(`SELECT created_at, input_tokens, output_tokens FROM proxy_request_logs WHERE created_at >= ? AND status_code = 200`, startTimestamp)
			if err == nil {
				for rows.Next() {
					var createdAt, inputT, outputT int64
					rows.Scan(&createdAt, &inputT, &outputT)
					tokens := inputT + outputT
					bucket := createdAt / dedupWindowSeconds
					key := fmt.Sprintf("%d-%d", bucket, tokens)
					if seen[key] {
						continue
					}
					seen[key] = true
					dt := time.Unix(createdAt, 0)
					dStr := dt.Format("2006-01-02")
					dailyTokens[dStr] += tokens
				}
				rows.Close()
			}
			db.Close()
		}
	}

	if dbPath := hermesDBPath(); fileExists(dbPath) {
		db, err := sql.Open("sqlite", dbPath)
		if err == nil {
			rows, err := db.Query(`SELECT started_at, input_tokens, output_tokens, cache_read_tokens FROM sessions WHERE started_at >= ?`, startTimestamp)
			if err == nil {
				for rows.Next() {
					var startedAt, inputT, outputT, cacheReadT int64
					rows.Scan(&startedAt, &inputT, &outputT, &cacheReadT)
					tokens := inputT + outputT + cacheReadT
					bucket := startedAt / dedupWindowSeconds
					key := fmt.Sprintf("%d-%d", bucket, tokens)
					if seen[key] {
						continue
					}
					seen[key] = true
					dt := time.Unix(startedAt, 0)
					dStr := dt.Format("2006-01-02")
					dailyTokens[dStr] += tokens
				}
				rows.Close()
			}
			db.Close()
		}
	}

	var maxVal int64
	dayList := make([]HeatmapDay, 0, days)
	for i := 0; i < days; i++ {
		d := startMidnight.AddDate(0, 0, i)
		dStr := d.Format("2006-01-02")
		tokens := dailyTokens[dStr]
		if tokens > maxVal {
			maxVal = tokens
		}
		wd := int(d.Weekday())
		if wd == 0 {
			wd = 6
		} else {
			wd--
		}
		dayList = append(dayList, HeatmapDay{
			Date:    dStr,
			Label:   d.Format("01-02"),
			Weekday: wd,
			Month:   int(d.Month()),
			Tokens:  tokens,
		})
	}

	return HeatmapResponse{
		Days:      dayList,
		MaxValue:  maxVal,
		StartDate: startMidnight.Format("2006-01-02"),
		EndDate:   now.Format("2006-01-02"),
	}
}

// ───── API: /api/heatmap_detail ─────
func getHeatmapDetail(weekday, hour, days, page, pageSize int, dateStr string) SessionListResponse {
	now := time.Now()
	start := now.AddDate(0, 0, -(days - 1))
	startMidnight := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	startTimestamp := startMidnight.Unix()

	var ccLogs, hermesLogs []LogEntry
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ccLogs = scanCCSwitchLogs(startTimestamp) }()
	go func() { defer wg.Done(); hermesLogs = scanHermesTokens(startTimestamp) }()
	wg.Wait()

	allLogs := append(ccLogs, hermesLogs...)
	allLogs = dedupEvents(allLogs)

	var filtered []LogEntry
	if dateStr != "" {
		// 按 date 过滤 (yyyy-MM-dd), 用于"点击热力图格子下钻当日详情"
		d, err := time.Parse("2006-01-02", dateStr)
		if err == nil {
			dayStart := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local).Unix()
			dayEnd := dayStart + 86400
			for _, ev := range allLogs {
				if ev.Timestamp >= dayStart && ev.Timestamp < dayEnd {
					filtered = append(filtered, ev)
				}
			}
		}
	} else {
		// 按 weekday + hour 过滤 (历史接口, 暂时保留)
		for _, ev := range allLogs {
			t := time.Unix(ev.Timestamp, 0)
			goWd := int(t.Weekday())
			var wd int
			if goWd == 0 {
				wd = 6
			} else {
				wd = goWd - 1
			}
			if wd == weekday && t.Hour() == hour {
				filtered = append(filtered, ev)
			}
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp > filtered[j].Timestamp
	})

	sessions := make([]SessionEntry, 0, len(filtered))
	for _, ev := range filtered {
		sessions = append(sessions, SessionEntry{
			Timestamp:     ev.Timestamp,
			Time:          time.Unix(ev.Timestamp, 0).Format("01-02 15:04:05"),
			Tool:          ev.Tool,
			Model:         ev.Model,
			InputTokens:   ev.InputTokens,
			OutputTokens:  ev.OutputTokens,
			TotalTokens:   ev.TotalTokens,
			InputCached:   ev.InputCached,
			InputUncached: ev.InputUncached,
			LatencyMs:     0,
			SessionID:     ev.SessionID,
		})
	}

	// 当天统计 (与 Python scanner.get_heatmap_detail 保持一致)
	summary := map[string]interface{}{}
	if len(filtered) > 0 {
		var totalTokens, totalCached int64
		peakIdx := 0
		for i, s := range sessions {
			totalTokens += s.TotalTokens
			totalCached += s.InputCached
			if s.TotalTokens > sessions[peakIdx].TotalTokens {
				peakIdx = i
			}
		}
		summary["total_tokens"] = totalTokens
		summary["total_cached"] = totalCached
		summary["call_count"] = len(sessions)
		summary["peak_tokens"] = sessions[peakIdx].TotalTokens
		summary["peak_time"] = sessions[peakIdx].Time
		// avg/max latency: Go 当前 cc-switch 不存 latency, 给 0 占位
		summary["avg_latency_ms"] = 0
		summary["max_latency_ms"] = 0
	}

	return SessionListResponse{
		Sessions: sessions, Total: len(sessions), Summary: summary,
	}
}

func main() {
	// 读取版本号
	appVersion = readAppVersion()

	// 解析命令行参数 (feedURL 是包级变量, checkUpdateRemote 会用到)
	port := defaultPort
	noBrowser := false
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "--port" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &port)
			i++
		}
		if args[i] == "--update-feed-url" && i+1 < len(args) {
			feedURL = args[i+1]
			i++
		}
		if args[i] == "--no-browser" {
			noBrowser = true
		}
	}

	// 单实例锁
	if !acquireSingletonLock() {
		fmt.Printf("[server] 已有 Token Monitor 实例在运行, 退出本次启动。\n")
		return
	}

	// 启动异步余额刷新
	startBalanceRefresher()

	// API 路由
	http.HandleFunc("/api/usage", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		data := getTodayUsage()
		writeJSON(w, 200, data)
	})

	http.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		data := getHistoricalUsage(30)
		writeJSON(w, 200, data)
	})

	http.HandleFunc("/api/app-info", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		writeJSON(w, 200, AppInfoResponse{
			Name:          "Token Monitor",
			Version:       readAppVersion(),
			UpdateFeedURL: feedURL,
			UpdateEnabled: feedURL != "",
		})
	})

	http.HandleFunc("/api/check-update", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		result := checkUpdateRemote()
		writeJSON(w, 200, result)
	})

	http.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		days := 1
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 {
				days = n
			}
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if pageSize < 1 {
			pageSize = 50
		}
		writeJSON(w, 200, getSessionList(days, page, pageSize))
	})

	http.HandleFunc("/api/heatmap", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		days := 30
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 {
				days = n
			}
		}
		writeJSON(w, 200, getHeatmapData(days))
	})
	http.HandleFunc("/api/heatmap_detail", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		weekday, _ := strconv.Atoi(r.URL.Query().Get("weekday"))
		hour, _ := strconv.Atoi(r.URL.Query().Get("hour"))
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days == 0 {
			days = 30
		}
		dateStr := r.URL.Query().Get("date")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if pageSize < 1 {
			pageSize = 50
		}
		writeJSON(w, 200, getHeatmapDetail(weekday, hour, days, page, pageSize, dateStr))
	})
	// 静态文件 (嵌入的 index.html + chart.js)
	staticContent, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(staticContent))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			data, err := fs.ReadFile(staticContent, "index.html")
			if err != nil {
				http.Error(w, "Not Found", 404)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	// 端口自动递增: 默认 15723, 被占就试下一个, 最多试 10 个
	// 避免端口冲突导致启动失败 (macOS 版固定 15723, Go 版自动避让)
	var ln net.Listener
	var err error
	actualPort := port
	for i := 0; i < 10; i++ {
		tryPort := port + i
		addr := fmt.Sprintf("127.0.0.1:%d", tryPort)
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			actualPort = tryPort
			break
		}
		if i == 0 {
			fmt.Printf("[*] 端口 %d 被占用, 尝试其他端口...\n", tryPort)
		}
	}
	if ln == nil {
		fmt.Printf("[-] 端口 %d-%d 全部被占用, 无法启动\n", port, port+9)
		fmt.Printf("[*] 按回车键退出...\n")
		fmt.Scanln()
		return
	}
	port = actualPort
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	server := &http.Server{Addr: addr}

	// 启动后自动打开浏览器 (--no-browser 跳过, 由 launcher 等外部程序自己开窗)
	if !noBrowser {
		go func() {
			time.Sleep(1 * time.Second)
			openBrowser(fmt.Sprintf("http://127.0.0.1:%d", port))
		}()
	} else {
		fmt.Println("[*] --no-browser 模式, 不自动打开系统浏览器 (由调用方负责 UI)")
	}

	fmt.Printf("[+] Token Monitor 仪表盘已启动: http://%s\n", addr)
	fmt.Printf("[+] 更新源: %s\n", feedURL)
	fmt.Printf("[+] 按 Ctrl+C 退出\n")

	// 用预检的 listener 启动服务
	if err := server.Serve(ln); err != nil {
		fmt.Printf("[-] 服务器错误: %v\n", err)
		fmt.Printf("[*] 按回车键退出...\n")
		fmt.Scanln()
	}
}
