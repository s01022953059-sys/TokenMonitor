package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

//go:embed static/*
var staticFS embed.FS

const port = 15723

// ───── 数据结构 ─────

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
	Labels  []string             `json:"labels"`
	Values  []int64              `json:"values"`
	ByTool  map[string][]int64   `json:"by_tool"`
	ByModel map[string][]int64   `json:"by_model"`
}

// ───── 路径工具 ─────

func homeDir() string {
	h, _ := os.UserHomeDir()
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
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = filepath.Join(homeDir(), "AppData", "Roaming")
	}
	return filepath.Join(appData, "BingchaAI", "usage_stats.json")
}

func codexDBPath() string {
	return filepath.Join(homeDir(), ".codex", "logs_2.sqlite")
}

func claudeCodeProjectsDir() string {
	return filepath.Join(homeDir(), ".claude", "projects")
}

func todayMidnight() int64 {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return midnight.Unix()
}

// ───── Token 估算 ─────

var nonASCIIRe = regexp.MustCompile(`[^\x00-\x7F]`)

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	nonASCII := nonASCIIRe.FindAllString(text, -1)
	chineseTokens := float64(len(nonASCII)) * 0.8
	asciiOnly := nonASCIIRe.ReplaceAllString(text, " ")
	words := strings.Fields(asciiOnly)
	englishTokens := float64(len(words)) * 1.3
	totalLen := utf8.RuneCountInString(text)
	wordChars := 0
	for _, w := range words {
		wordChars += len(w)
	}
	remaining := totalLen - len(nonASCII) - wordChars
	if remaining < 0 {
		remaining = 0
	}
	otherTokens := float64(remaining) * 0.3
	return int(chineseTokens + englishTokens + otherTokens)
}

// ───── DeepSeek 余额（异步缓存） ─────

var (
	balanceCache   = BalanceInfo{"0.00", "CNY", "Loading..."}
	balanceMu      sync.RWMutex
)

func getCachedBalance() BalanceInfo {
	balanceMu.RLock()
	defer balanceMu.RUnlock()
	return balanceCache
}

func refreshBalance() {
	b := fetchDeepSeekBalance()
	balanceMu.Lock()
	balanceCache = b
	balanceMu.Unlock()
}

func startBalanceRefresher() {
	go func() {
		refreshBalance() // 首次刷新
		for range time.Tick(60 * time.Second) {
			refreshBalance()
		}
	}()
}

func fetchDeepSeekBalance() BalanceInfo {
	dbPath := ccSwitchDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return BalanceInfo{"0.00", "CNY", "Offline"}
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return BalanceInfo{"0.00", "CNY", fmt.Sprintf("Error: %v", err)}
	}
	defer db.Close()

	var settingsJSON string
	err = db.QueryRow("SELECT settings_config FROM providers WHERE id='ddsds'").Scan(&settingsJSON)
	if err != nil {
		return BalanceInfo{"0.00", "CNY", "Provider Not Found"}
	}

	var cfg map[string]interface{}
	json.Unmarshal([]byte(settingsJSON), &cfg)
	key, _ := cfg["apiKey"].(string)
	if key == "" {
		if k2, ok := cfg["api_key"].(string); ok {
			key = k2
		}
	}
	if key == "" {
		return BalanceInfo{"0.00", "CNY", "No Key"}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.deepseek.com/user/balance", nil)
	req.Header.Set("Authorization", "Bearer "+key)
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
				return BalanceInfo{bal, cur, "Active"}
			}
		}
	}
	return BalanceInfo{"0.00", "CNY", "Unknown"}
}

// ───── 数据扫描 ─────

func scanCCSwitchLogs(todayStart int64) []LogEntry {
	dbPath := ccSwitchDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
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

		tool := "Other"
		if appType.Valid && appType.String != "" {
			tool = strings.Title(strings.ToLower(appType.String))
		}
		m := "Unknown"
		if model.Valid {
			m = model.String
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

func scanHermesTokens(todayStart int64) []LogEntry {
	dbPath := hermesDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT started_at, model, input_tokens, output_tokens, cache_read_tokens
		FROM sessions WHERE started_at >= ? ORDER BY started_at ASC
	`, todayStart)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var startedAt int64
		var model sql.NullString
		var inputT, outputT, cacheReadT sql.NullInt64
		rows.Scan(&startedAt, &model, &inputT, &outputT, &cacheReadT)

		iCached := cacheReadT.Int64
		iUncached := inputT.Int64
		totalInput := iCached + iUncached
		oT := outputT.Int64
		totalT := totalInput + oT

		m := "Unknown"
		if model.Valid {
			m = model.String
		}

		logs = append(logs, LogEntry{
			Time:          time.Unix(startedAt, 0).Format("15:04:05"),
			Timestamp:     startedAt,
			Tool:          "Hermes-Native",
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

// ───── Codex 原生日志 ─────

var (
	reCodexTurnID      = regexp.MustCompile(`turn\.id=([0-9a-f-]+)`)
	reCodexModel       = regexp.MustCompile(`model=([^ }]+)`)
	reCodexInput       = regexp.MustCompile(`codex\.turn\.token_usage\.input_tokens=([0-9]+)`)
	reCodexOutput      = regexp.MustCompile(`codex\.turn\.token_usage\.output_tokens=([0-9]+)`)
	reCodexCached      = regexp.MustCompile(`codex\.turn\.token_usage\.cached_input_tokens=([0-9]+)`)
	reCodexTotal       = regexp.MustCompile(`codex\.turn\.token_usage\.total_tokens=([0-9]+)`)
)

func parseCodexInt(re *regexp.Regexp, s string) int64 {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	var v int64
	fmt.Sscanf(m[1], "%d", &v)
	return v
}

func scanCodexLogs(todayStart int64) []LogEntry {
	dbPath := codexDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT ts, feedback_log_body FROM logs
		WHERE ts >= ?
		  AND feedback_log_body LIKE '%codex.turn.token_usage.total_tokens=%'
		ORDER BY ts ASC
	`, todayStart)
	if err != nil {
		return nil
	}
	defer rows.Close()

	seen := map[string]bool{}
	var logs []LogEntry
	for rows.Next() {
		var ts int64
		var body sql.NullString
		rows.Scan(&ts, &body)
		if !body.Valid {
			continue
		}
		b := body.String

		// 按 turn_id 去重
		turnMatch := reCodexTurnID.FindStringSubmatch(b)
		if len(turnMatch) < 2 {
			continue
		}
		turnID := turnMatch[1]
		if seen[turnID] {
			continue
		}

		totalT := parseCodexInt(reCodexTotal, b)
		if totalT == 0 {
			continue
		}
		seen[turnID] = true

		inputT := parseCodexInt(reCodexInput, b)
		outputT := parseCodexInt(reCodexOutput, b)
		cachedT := parseCodexInt(reCodexCached, b)
		uncached := inputT - cachedT
		if uncached < 0 {
			uncached = 0
		}

		model := "Unknown"
		if mm := reCodexModel.FindStringSubmatch(b); len(mm) >= 2 {
			model = mm[1]
		}

		logs = append(logs, LogEntry{
			Time:          time.Unix(ts, 0).Format("15:04:05"),
			Timestamp:     ts,
			Tool:          "Codex-Native",
			Model:         model,
			InputTokens:   inputT,
			OutputTokens:  outputT,
			TotalTokens:   totalT,
			InputCached:   cachedT,
			InputUncached: uncached,
		})
	}
	return logs
}

// ───── Claude Code 日志 ─────

func scanClaudeCodeLogs(todayStart int64) []LogEntry {
	projectsDir := claudeCodeProjectsDir()
	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var logs []LogEntry
	seen := map[string]bool{}
	todayStr := time.Now().Format("2006-01-02")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projDir := filepath.Join(projectsDir, entry.Name())
		files, _ := os.ReadDir(projDir)
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			// 跳过修改时间在今天之前的文件
			fi, err := f.Info()
			if err != nil || fi.ModTime().Format("2006-01-02") < todayStr {
				continue
			}
			data, err := os.ReadFile(filepath.Join(projDir, f.Name()))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if line == "" || !strings.Contains(line, `"assistant"`) {
					continue
				}
				var rec struct {
					Type      string `json:"type"`
					UUID      string `json:"uuid"`
					Timestamp string `json:"timestamp"`
					Message   struct {
						Model string `json:"model"`
						Usage struct {
							InputTokens         int64 `json:"input_tokens"`
							OutputTokens        int64 `json:"output_tokens"`
							CacheReadTokens     int64 `json:"cache_read_input_tokens"`
							CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
						} `json:"usage"`
					} `json:"message"`
				}
				if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "assistant" {
					continue
				}
				// 解析时间戳
				t, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
				if err != nil {
					t, err = time.Parse("2006-01-02T15:04:05.000Z", rec.Timestamp)
					if err != nil {
						continue
					}
				}
				ts := t.Unix()
				if ts < todayStart {
					continue
				}
				u := rec.Message.Usage
				totalT := u.InputTokens + u.OutputTokens
				if totalT == 0 || seen[rec.UUID] {
					continue
				}
				seen[rec.UUID] = true

				cachedT := u.CacheReadTokens + u.CacheCreationTokens
				uncached := u.InputTokens - cachedT
				if uncached < 0 {
					uncached = 0
				}

				model := "Unknown"
				if rec.Message.Model != "" {
					model = rec.Message.Model
				}

				logs = append(logs, LogEntry{
					Time:          time.Unix(ts, 0).Format("15:04:05"),
					Timestamp:     ts,
					Tool:          "ClaudeCode",
					Model:         model,
					InputTokens:   u.InputTokens,
					OutputTokens:  u.OutputTokens,
					TotalTokens:   totalT,
					InputCached:   cachedT,
					InputUncached: uncached,
				})
			}
		}
	}
	return logs
}

// ───── API: /api/usage ─────

func getTodayUsage() UsageResponse {
	todayStart := todayMidnight()

	// 并行扫描五个数据源
	var ccLogs, antigravityLogs, hermesLogs, codexLogs, claudeLogs []LogEntry
	var wg sync.WaitGroup
	wg.Add(5)
	go func() { defer wg.Done(); ccLogs = scanCCSwitchLogs(todayStart) }()
	go func() { defer wg.Done(); antigravityLogs = scanAntigravityTokens() }()
	go func() { defer wg.Done(); hermesLogs = scanHermesTokens(todayStart) }()
	go func() { defer wg.Done(); codexLogs = scanCodexLogs(todayStart) }()
	go func() { defer wg.Done(); claudeLogs = scanClaudeCodeLogs(todayStart) }()
	wg.Wait()

	allLogs := append(append(append(append(ccLogs, antigravityLogs...), hermesLogs...), codexLogs...), claudeLogs...)
	sort.Slice(allLogs, func(i, j int) bool { return allLogs[i].Timestamp < allLogs[j].Timestamp })

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
	estimatedCost := float64(inputCached)*0.0000001 + float64(inputUncached)*0.000001 + float64(outputTokens)*0.000002

	recentEvents := allLogs
	if len(recentEvents) > 30 {
		recentEvents = recentEvents[len(recentEvents)-30:]
	}

	return UsageResponse{
		Summary: map[string]interface{}{
			"total_tokens":       totalTokens,
			"input_tokens":       inputTokens,
			"output_tokens":      outputTokens,
			"input_cached":       inputCached,
			"input_uncached":     inputUncached,
			"estimated_cost_usd": fmt.Sprintf("%.4f", estimatedCost),
			"date":               time.Now().Format("2006-01-02"),
			"deepseek_balance":   dsBalance.Balance,
			"deepseek_currency":  dsBalance.Currency,
			"deepseek_status":    dsBalance.Status,
		},
		ByTool:       byTool,
		ByModel:      byModel,
		RecentEvents: recentEvents,
	}
}

// ───── API: /api/history ─────

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

	tools := []string{"Antigravity", "Hermes", "Hermes-Native", "Codex", "Codex-Native", "ClaudeCode", "Other"}
	models := []string{"deepseek-v4-flash", "gemini 3.5 flash", "deepseek-v4-pro", "gpt-5.5", "gpt-5.4-mini", "gpt-5.4", "glm-5.1", "kimi-for-coding", "qwen3.7-max", "qwen3.7-plus", "qwen3.6-plus-vl", "qwen3.6-plus", "deepseek-v4-flash-free", "Other"}

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

	// 1. cc-switch
	dbPath := ccSwitchDBPath()
	if _, err := os.Stat(dbPath); err == nil {
		if db, err := sql.Open("sqlite", dbPath+"?mode=ro"); err == nil {
			defer db.Close()
			rows, err := db.Query(`
				SELECT created_at, input_tokens, output_tokens, app_type, model
				FROM proxy_request_logs WHERE created_at >= ? AND status_code = 200
			`, startTimestamp)
			if err == nil {
				defer rows.Close()
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
			}
		}
	}

	// 2. Hermes
	hermesPath := hermesDBPath()
	if _, err := os.Stat(hermesPath); err == nil {
		if db, err := sql.Open("sqlite", hermesPath+"?mode=ro"); err == nil {
			defer db.Close()
			rows, err := db.Query(`
				SELECT started_at, input_tokens, output_tokens, cache_read_tokens, model
				FROM sessions WHERE started_at >= ?
			`, startTimestamp)
			if err == nil {
				defer rows.Close()
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
					tokens := inputT.Int64 + cacheReadT.Int64 + outputT.Int64
					dailyTotals[idx] += tokens
					toolData["Hermes-Native"][idx] += tokens
					mNorm := getNormalizedModel(model.String, models)
					modelData[mNorm][idx] += tokens
				}
			}
		}
	}

	// 3. Antigravity 统计文件
	statsPath := antigravityStatsPath()
	if data, err := os.ReadFile(statsPath); err == nil {
		var stats struct {
			Records map[string]struct {
				InputTokens  int64 `json:"inputTokens"`
				OutputTokens int64 `json:"outputTokens"`
			} `json:"records"`
		}
		if json.Unmarshal(data, &stats) == nil {
			for _, dStr := range dateList {
				if rec, ok := stats.Records[dStr]; ok {
					tokens := rec.InputTokens + rec.OutputTokens
					idx := dateIdx[dStr]
					dailyTotals[idx] += tokens
					toolData["Antigravity"][idx] += tokens
					modelData["gemini 3.5 flash"][idx] += tokens
				}
			}
		}
	}

	// 4. Codex 原生日志
	codexPath := codexDBPath()
	if _, err := os.Stat(codexPath); err == nil {
		if db, err := sql.Open("sqlite", codexPath+"?mode=ro"); err == nil {
			defer db.Close()
			rows, err := db.Query(`
				SELECT ts, feedback_log_body FROM logs
				WHERE ts >= ?
				  AND feedback_log_body LIKE '%codex.turn.token_usage.total_tokens=%'
				ORDER BY ts ASC
			`, startTimestamp)
			if err == nil {
				defer rows.Close()
				seen := map[string]bool{}
				for rows.Next() {
					var ts int64
					var body sql.NullString
					rows.Scan(&ts, &body)
					if !body.Valid {
						continue
					}
					b := body.String
					turnMatch := reCodexTurnID.FindStringSubmatch(b)
					if len(turnMatch) < 2 {
						continue
					}
					turnID := turnMatch[1]
					if seen[turnID] {
						continue
					}
					totalT := parseCodexInt(reCodexTotal, b)
					if totalT == 0 {
						continue
					}
					seen[turnID] = true

					inputT := parseCodexInt(reCodexInput, b)
					outputT := parseCodexInt(reCodexOutput, b)

					dStr := time.Unix(ts, 0).Format("2006-01-02")
					idx, ok := dateIdx[dStr]
					if !ok {
						continue
					}
					tokens := inputT + outputT
					dailyTotals[idx] += tokens
					toolData["Codex-Native"][idx] += tokens

					model := "Other"
					if mm := reCodexModel.FindStringSubmatch(b); len(mm) >= 2 {
						model = getNormalizedModel(mm[1], models)
					}
					modelData[model][idx] += tokens
				}
			}
		}
	}

	// 5. Claude Code JSONL
	projectsDir := claudeCodeProjectsDir()
	if entries, err := os.ReadDir(projectsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			projDir := filepath.Join(projectsDir, entry.Name())
			files, _ := os.ReadDir(projDir)
			for _, f := range files {
				if !strings.HasSuffix(f.Name(), ".jsonl") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(projDir, f.Name()))
				if err != nil {
					continue
				}
				for _, line := range strings.Split(string(data), "\n") {
					if line == "" || !strings.Contains(line, `"assistant"`) {
						continue
					}
					var rec struct {
						Type      string `json:"type"`
						UUID      string `json:"uuid"`
						Timestamp string `json:"timestamp"`
						Message   struct {
							Model string `json:"model"`
							Usage struct {
								InputTokens  int64 `json:"input_tokens"`
								OutputTokens int64 `json:"output_tokens"`
							} `json:"usage"`
						} `json:"message"`
					}
					if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "assistant" {
						continue
					}
					t, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
					if err != nil {
						t, err = time.Parse("2006-01-02T15:04:05.000Z", rec.Timestamp)
						if err != nil {
							continue
						}
					}
					dStr := t.Format("2006-01-02")
					idx, ok := dateIdx[dStr]
					if !ok {
						continue
					}
					tokens := rec.Message.Usage.InputTokens + rec.Message.Usage.OutputTokens
					if tokens == 0 {
						continue
					}
					dailyTotals[idx] += tokens
					toolData["ClaudeCode"][idx] += tokens
					mNorm := getNormalizedModel(rec.Message.Model, models)
					modelData[mNorm][idx] += tokens
				}
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

// ───── HTTP 服务器 ─────

func main() {
	// API 路由
	http.HandleFunc("/api/usage", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			return
		}
		data := getTodayUsage()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(data)
	})

	http.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			return
		}
		data := getHistoricalUsage(30)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(data)
	})

	// 静态文件（嵌入的 index.html + chart.js）
	staticContent, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(staticContent))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			// 直接读取嵌入文件，避免 FileServer 的 301 重定向循环
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

	// 检查端口是否已被占用
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("[!] 端口 %d 已被占用，Token Monitor 可能已在运行\n", port)
		return
	}
	ln.Close()

	// 启动异步余额刷新
	startBalanceRefresher()

	fmt.Printf("[+] Token Monitor 已启动: http://%s\n", addr)

	server := &http.Server{Addr: addr}
	if err := server.ListenAndServe(); err != nil {
		fmt.Printf("[-] 服务器错误: %v\n", err)
	}
}

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}
