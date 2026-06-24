package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed static/*
var staticFS embed.FS

const defaultPort = 15723
const updateFeedURL = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor/releases/latest"

// 版本号: 优先从同目录 version.txt 读取 (打包时写入), 回退到编译时注入的常量。
// 这和 Python 版从 Info.plist 读版本号的思路一致: 让运行时能拿到真实版本。
var appVersion = "1.3.44"

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
		SELECT created_at, app_type, model, input_tokens, output_tokens, cache_read_tokens
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
		var inputT, outputT, cacheReadT sql.NullInt64
		rows.Scan(&createdAt, &appType, &model, &inputT, &outputT, &cacheReadT)

		iT := inputT.Int64
		oT := outputT.Int64
		cT := cacheReadT.Int64
		uncached := iT - cT
		if uncached < 0 {
			uncached = 0
		}
		totalT := iT + oT

		// Python: app_type.capitalize() — 首字母大写其余小写
		tool := "Other"
		if appType.Valid && appType.String != "" {
			s := strings.ToLower(appType.String)
			tool = strings.ToUpper(s[:1]) + s[1:]
		}

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
		})
	}
	return logs
}

// 2. Antigravity (冰茶 AI)
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

	return []LogEntry{{
		Time:          "实时",
		Timestamp:     time.Now().Unix(),
		Tool:          "Antigravity",
		Model:         "Gemini 3.5 Flash",
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
			m = model.String
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
		return "Antigravity"
	}
	if strings.Contains(lower, "hermes") {
		return "Hermes"
	}
	if strings.Contains(lower, "codex") || strings.Contains(lower, "code") {
		return "Codex"
	}
	return "Other"
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
	tools := []string{"Antigravity", "Hermes", "Codex", "Other"}
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
	result := map[string]interface{}{
		"ok":              false,
		"current_version": appVersion,
		"latest_version":  nil,
		"update_available": false,
		"feed_url":        updateFeedURL,
		"http_status":     nil,
		"error":           nil,
		"raw_excerpt":     nil,
		"title":           nil,
		"download_url":    nil,
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest("GET", updateFeedURL, nil)
	req.Header.Set("User-Agent", fmt.Sprintf("TokenMonitor/%s (+https://gitcode.com/baggiopeng/TokenMonitor)", appVersion))
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
	result["update_available"] = compareVersions(version, appVersion) > 0
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

var singletonLockFile *os.File

func acquireSingletonLock() bool {
	lockPath := os.Getenv("TOKEN_MONITOR_LOCK_FILE")
	if lockPath == "" {
		lockPath = filepath.Join(os.TempDir(), "token_monitor_server.lock")
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

func main() {
	// 读取版本号
	appVersion = readAppVersion()

	// 解析命令行参数
	port := defaultPort
	feedURL := updateFeedURL
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
			Version:       appVersion,
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

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("[+] Token Monitor 仪表盘已启动: http://%s\n", addr)
	fmt.Printf("[+] 更新源: %s\n", feedURL)

	server := &http.Server{Addr: addr}
	if err := server.ListenAndServe(); err != nil {
		fmt.Printf("[-] 服务器错误: %v\n", err)
	}
}
