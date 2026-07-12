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
	"time"

	_ "modernc.org/sqlite"
)

//go:embed static/*
var staticFS embed.FS

const defaultPort = 15723
const updateFeedURL = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor/releases/latest"

// 版本号: 优先从同目录 version.txt 读取 (打包时写入), 回退到编译时注入的常量。
// 这和 Python 版从 Info.plist 读版本号的思路一致: 让运行时能拿到真实版本。
var appVersion = "1.4.21"

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
	Sessions   []SessionEntry         `json:"sessions"`
	Total      int                    `json:"total"`
	Page       int                    `json:"page"`
	PageSize   int                    `json:"page_size"`
	TotalPages int                    `json:"total_pages"`
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

func workbuddyDBPath() string {
	return filepath.Join(homeDir(), ".workbuddy", "workbuddy.db")
}

func workbuddyProjectsPath() string {
	return filepath.Join(homeDir(), ".workbuddy", "projects")
}

func codexLogDBPath() string {
	return filepath.Join(homeDir(), ".codex", "logs_2.sqlite")
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
		// v1.3.92 修复: Go 端 by_model 出现 "Other: 1.6B" 而 Python 总数 ~3.5B.
		// 根因 Go 端别名表缺 minimax-m2.7 / -highspeed, 不同 prefix 的子型号
		// 在最后 return s 时各算一个, 被 by_model 各自成项累加成 Other.
		// 修法: 给每个有 -highspeed / -free 等子变体的 model 家族加前缀折叠规则.
		// 注: gpt-5.4 和 gpt-5.4-mini 是两个真不同模型 (mini 是轻量版), 不折叠
		"minimax-m2.7":           "minimax-m2.7",
		"minimax-m2.7-highspeed": "minimax-m2.7",
		"minimax-m3":             "minimax-m3",
		"minimax-m3-free":        "minimax-m3",
	}
	if v, ok := aliases[s]; ok {
		return v
	}
	// 启发式: qwen3.6-plus 家族折叠 (排除 vl 变体)
	if strings.HasPrefix(s, "qwen3.6-plus") && s != "qwen3.6-plus-vl" {
		return "qwen3.6-plus"
	}
	// v1.3.92 修复: minimax 家族前缀折叠 (排除 -m3 系列以保留 m2/m3 区分)
	if strings.HasPrefix(s, "minimax-m2") && !strings.HasPrefix(s, "minimax-m3") {
		return "minimax-m2.7"
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

var providerModelPattern = regexp.MustCompile(`(?m)^\s*model\s*=\s*["']([^"']+)["']`)

func loadProviderModelMaps(db *sql.DB) (map[string]string, map[string]string) {
	byProvider := map[string]string{}
	activeByApp := map[string]string{}
	rows, err := db.Query(`SELECT id, app_type, settings_config, is_current FROM providers`)
	if err != nil {
		return byProvider, activeByApp
	}
	defer rows.Close()
	for rows.Next() {
		var id, appType, raw string
		var current bool
		if rows.Scan(&id, &appType, &raw, &current) != nil {
			continue
		}
		var settings struct {
			Config string `json:"config"`
		}
		if json.Unmarshal([]byte(raw), &settings) != nil {
			continue
		}
		match := providerModelPattern.FindStringSubmatch(settings.Config)
		if len(match) != 2 {
			continue
		}
		byProvider[id] = match[1]
		if current {
			activeByApp[appType] = match[1]
		}
	}
	return byProvider, activeByApp
}

func ccTokenBreakdown(appType string, input, output, cacheRead, cacheCreate int64) (int64, int64, int64, int64) {
	separateCache := strings.Contains(strings.ToLower(appType), "claude") || cacheRead > input || cacheCreate > 0
	if separateCache {
		cached := cacheRead
		uncached := input + cacheCreate
		totalInput := cached + uncached
		return totalInput, totalInput + output, cached, uncached
	}
	cached := cacheRead
	if cached < 0 {
		cached = 0
	}
	if cached > input {
		cached = input
	}
	return input, input + output, cached, input - cached
}

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
	providerModels, activeModels := loadProviderModelMaps(db)

	rows, err := db.Query(`
		SELECT created_at, app_type, model, input_tokens, output_tokens, cache_read_tokens,
		       cache_creation_tokens, session_id, provider_id, data_source
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
		var appType, model, providerID, dataSource sql.NullString
		var inputT, outputT, cacheReadT, cacheCreationT sql.NullInt64
		var sessionID sql.NullString
		rows.Scan(&createdAt, &appType, &model, &inputT, &outputT, &cacheReadT, &cacheCreationT, &sessionID, &providerID, &dataSource)

		iT := inputT.Int64
		oT := outputT.Int64
		totalInput, totalT, cT, uncached := ccTokenBreakdown(
			appType.String, iT, oT, cacheReadT.Int64, cacheCreationT.Int64,
		)

		// 工具归一化 (跟 Python _normalize_app_type 保持一致, 避免首页/详情名字不一致)
		tool := normalizeAppTypeForCCSwitch(appType.String)

		actualModel := model.String
		if configured := providerModels[providerID.String]; configured != "" {
			actualModel = configured
		} else if dataSource.String == "codex_session" || providerID.String == "_codex_session" {
			if configured := activeModels[appType.String]; configured != "" {
				actualModel = configured
			}
		}
		m := normalizeModelName(actualModel)

		logs = append(logs, LogEntry{
			Time:          time.Unix(createdAt, 0).Format("15:04:05"),
			Timestamp:     createdAt,
			Tool:          tool,
			Model:         m,
			InputTokens:   totalInput,
			OutputTokens:  oT,
			TotalTokens:   totalT,
			InputCached:   cT,
			InputUncached: uncached,
			SessionID:     sessionID.String,
		})
	}
	return logs
}

type codexUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	InputTokenDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func codexLogEntry(ts int64, model string, usage codexUsage, sessionID string) (LogEntry, bool) {
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.TotalTokens <= 0 {
		return LogEntry{}, false
	}
	cached := usage.InputTokenDetails.CachedTokens
	if cached == 0 {
		cached = usage.CachedInputTokens
	}
	if cached < 0 {
		cached = 0
	}
	if cached > usage.InputTokens {
		cached = usage.InputTokens
	}
	return LogEntry{
		Time:          time.Unix(ts, 0).Format("15:04:05"),
		Timestamp:     ts,
		Tool:          "Codex",
		Model:         normalizeModelName(model),
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		TotalTokens:   usage.TotalTokens,
		InputCached:   cached,
		InputUncached: usage.InputTokens - cached,
		SessionID:     sessionID,
	}, true
}

func scanCodexRollouts(startTimestamp int64) []LogEntry {
	var events []LogEntry
	for _, root := range []string{
		filepath.Join(homeDir(), ".codex", "sessions"),
		filepath.Join(homeDir(), ".codex", "archived_sessions"),
	} {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
				return nil
			}
			if info.ModTime().Unix() < startTimestamp-86400 {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer file.Close()
			model := "Other"
			var previousCumulative codexUsage
			hasPreviousCumulative := false
			sessionID := ""
			if ids := regexp.MustCompile(`[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}`).FindAllString(info.Name(), -1); len(ids) > 0 {
				sessionID = ids[len(ids)-1]
			}
			scanner := bufio.NewScanner(file)
			// Codex 工具输出可能形成超长 JSONL 单行；AgentsView 同样使用 64 MiB 上限。
			scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
			for scanner.Scan() {
				var item struct {
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Payload   struct {
						Type  string `json:"type"`
						Model string `json:"model"`
						Info  struct {
							LastTokenUsage  codexUsage `json:"last_token_usage"`
							TotalTokenUsage codexUsage `json:"total_token_usage"`
						} `json:"info"`
					} `json:"payload"`
				}
				if json.Unmarshal(scanner.Bytes(), &item) != nil {
					continue
				}
				if item.Type == "turn_context" {
					model = item.Payload.Model
					if model == "" {
						model = "Other"
					}
					continue
				}
				if item.Type != "event_msg" || item.Payload.Type != "token_count" {
					continue
				}
				cumulative := item.Payload.Info.TotalTokenUsage
				if cumulative.TotalTokens > 0 {
					if hasPreviousCumulative && cumulative == previousCumulative {
						continue
					}
					previousCumulative = cumulative
					hasPreviousCumulative = true
				}
				parsed, err := time.Parse(time.RFC3339Nano, item.Timestamp)
				if err != nil || parsed.Unix() < startTimestamp {
					continue
				}
				if event, ok := codexLogEntry(parsed.Unix(), model, item.Payload.Info.LastTokenUsage, sessionID); ok {
					events = append(events, event)
				}
			}
			return nil
		})
	}
	return dedupEvents(events)
}

func scanCodexTokens(startTimestamp int64) []LogEntry {
	dbPath := codexLogDBPath()
	if !fileExists(dbPath) {
		return scanCodexRollouts(startTimestamp)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return scanCodexRollouts(startTimestamp)
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT ts, feedback_log_body, thread_id
		FROM logs
		WHERE ts >= ? AND target = 'codex_api::sse::responses'
		  AND feedback_log_body LIKE '%response.completed%'
		ORDER BY ts ASC, ts_nanos ASC
	`, startTimestamp)
	if err != nil {
		return scanCodexRollouts(startTimestamp)
	}
	defer rows.Close()
	seenIDs := map[string]bool{}
	var events []LogEntry
	for rows.Next() {
		var ts int64
		var body, threadID sql.NullString
		if rows.Scan(&ts, &body, &threadID) != nil || !strings.HasPrefix(body.String, "SSE event: ") {
			continue
		}
		var envelope struct {
			Type     string `json:"type"`
			Response struct {
				ID          string     `json:"id"`
				CompletedAt int64      `json:"completed_at"`
				Model       string     `json:"model"`
				Usage       codexUsage `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(body.String, "SSE event: ")), &envelope) != nil || envelope.Type != "response.completed" {
			continue
		}
		if envelope.Response.ID != "" && seenIDs[envelope.Response.ID] {
			continue
		}
		seenIDs[envelope.Response.ID] = true
		if envelope.Response.CompletedAt > 0 {
			ts = envelope.Response.CompletedAt
		}
		if event, ok := codexLogEntry(ts, envelope.Response.Model, envelope.Response.Usage, threadID.String); ok {
			events = append(events, event)
		}
	}
	// logs_2.sqlite 可能仅覆盖当前 Codex 进程；始终与 rollout 合并，补齐重启前记录。
	return dedupEvents(append(events, scanCodexRollouts(startTimestamp)...))
}

//  2. 冰茶 AI 客户端 (Antigravity 旧名, 用户反馈"我应该没有使用 Antigravity" 因为
//     不认识 Antigravity 跟冰茶 AI 是同一客户端. 改工具名让统计更直观)
//     v1.3.90 降级为纯数据源: 冰茶 AI 是 IDE/代理配置入口, 调 LLM 都经 cc-switch
//     Codex 代理. usage_stats.json 是 BingchaAI 客户端本地累计, 跟 cc-switch
//     代理记录**同一批请求** (双计). scanner 不再产出 events, 流量归到真实
//     调用工具 (Codex / Claude / Other). 用户完全没装 cc-switch 时冰茶 AI
//     流量丢失, 但这种情况极罕见, 用户可在 BingchaAI 客户端配 cc-switch provider.
func scanAntigravityTokens() []LogEntry {
	return nil
}

// ccswitchTodayModels 拿今天 cc-switch.db 里出现过的 model 集合 (归一化后),
// 给 Antigravity 去重判断用
func ccswitchTodayModels() map[string]struct{} {
	result := map[string]struct{}{}
	dbPath := ccSwitchDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return result
	}
	todayStart := todayMidnight()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return result
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT DISTINCT model FROM proxy_request_logs
		WHERE created_at >= ? AND status_code = 200
	`, todayStart)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil || m == "" {
			continue
		}
		result[normalizeModelName(m)] = struct{}{}
	}
	return result
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
		SELECT id, CAST(COALESCE(NULLIF(ended_at, 0), started_at) AS INTEGER) AS occurred_at,
		       model, input_tokens, output_tokens,
		       cache_read_tokens, cache_write_tokens
		FROM sessions
		WHERE COALESCE(NULLIF(ended_at, 0), started_at) >= ?
		ORDER BY occurred_at ASC
	`, todayStart)
	if err != nil {
		fmt.Printf("[-] 扫描 Hermes 数据库出错: %v\n", err)
		return nil
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var sessionID sql.NullString
		var occurredAt int64
		var model sql.NullString
		var inputT, outputT, cacheReadT, cacheWriteT sql.NullInt64
		rows.Scan(&sessionID, &occurredAt, &model, &inputT, &outputT, &cacheReadT, &cacheWriteT)

		// Python: Hermes 的 input_t 是未缓存部分, cache_read_t 是已缓存部分
		iCached := cacheReadT.Int64
		iUncached := inputT.Int64 + cacheWriteT.Int64
		totalInput := iUncached + iCached
		oT := outputT.Int64
		totalT := totalInput + oT

		m := "Unknown"
		if model.Valid && model.String != "" {
			m = normalizeModelName(model.String)
		}

		logs = append(logs, LogEntry{
			Time:          time.Unix(occurredAt, 0).Format("15:04:05"),
			Timestamp:     occurredAt,
			Tool:          "Hermes",
			Model:         m,
			InputTokens:   totalInput,
			OutputTokens:  oT,
			TotalTokens:   totalT,
			InputCached:   iCached,
			InputUncached: iUncached,
			SessionID:     sessionID.String,
		})
	}
	return logs
}

// ───── 跨源去重 (对齐 scanner.py _dedup_events) ─────

const dedupWindowSeconds = 2

type workBuddyUsage struct {
	InputTokensSnake  int64 `json:"input_tokens"`
	OutputTokensSnake int64 `json:"output_tokens"`
	TotalTokensSnake  int64 `json:"total_tokens"`
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	TotalTokens       int64 `json:"totalTokens"`
	PromptTokens      int64 `json:"prompt_tokens"`
	CompletionTokens  int64 `json:"completion_tokens"`
	InputDetails      []struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"inputTokensDetails"`
	PromptDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func scanWorkBuddyProjects(startTimestamp int64) []LogEntry {
	root := workbuddyProjectsPath()
	if !fileExists(root) {
		return nil
	}
	var events []LogEntry
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		if info.ModTime().Unix() < startTimestamp-86400 {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for scanner.Scan() {
			var item struct {
				Timestamp    int64 `json:"timestamp"`
				ProviderData struct {
					Model    string          `json:"model"`
					Usage    json.RawMessage `json:"usage"`
					RawUsage json.RawMessage `json:"rawUsage"`
				} `json:"providerData"`
			}
			if json.Unmarshal(scanner.Bytes(), &item) != nil || item.Timestamp/1000 < startTimestamp {
				continue
			}
			raw := item.ProviderData.Usage
			if len(raw) == 0 || string(raw) == "null" {
				raw = item.ProviderData.RawUsage
			}
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			var usage workBuddyUsage
			if json.Unmarshal(raw, &usage) != nil {
				continue
			}
			input := usage.InputTokens
			if input == 0 {
				input = usage.InputTokensSnake
			}
			if input == 0 {
				input = usage.PromptTokens
			}
			output := usage.OutputTokens
			if output == 0 {
				output = usage.OutputTokensSnake
			}
			if output == 0 {
				output = usage.CompletionTokens
			}
			total := usage.TotalTokens
			if total == 0 {
				total = usage.TotalTokensSnake
			}
			if total == 0 {
				total = input + output
			}
			if total <= 0 {
				continue
			}
			cached := usage.PromptDetails.CachedTokens
			for _, detail := range usage.InputDetails {
				cached += detail.CachedTokens
			}
			if cached < 0 {
				cached = 0
			}
			if cached > input {
				cached = input
			}
			ts := item.Timestamp / 1000
			events = append(events, LogEntry{
				Time: time.Unix(ts, 0).Format("15:04:05"), Timestamp: ts,
				Tool: "WorkBuddy", Model: normalizeModelName(item.ProviderData.Model),
				InputTokens: input, OutputTokens: output, TotalTokens: total,
				InputCached: cached, InputUncached: input - cached,
				SessionID: strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())),
			})
		}
		return nil
	})
	return dedupEvents(events)
}

// scanWorkBuddyTokens 优先扫描 AgentsView 同口径的项目 JSONL；旧版本回退数据库近似值。
func scanWorkBuddyTokens(todayStart int64) []LogEntry {
	if fileExists(workbuddyProjectsPath()) {
		return scanWorkBuddyProjects(todayStart)
	}
	dbPath := workbuddyDBPath()
	if !fileExists(dbPath) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil
	}
	defer db.Close()
	todayStartMs := todayStart * 1000
	rows, err := db.Query(`
		SELECT s.id, s.model, s.created_at, s.updated_at, su.used
		FROM sessions s
		LEFT JOIN session_usage su ON s.id = su.session_id
		WHERE s.deleted_at IS NULL AND s.updated_at >= ?
		ORDER BY s.updated_at ASC
	`, todayStartMs)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var logs []LogEntry
	for rows.Next() {
		var sid, model sql.NullString
		var createdAt, updatedAt, used sql.NullInt64
		rows.Scan(&sid, &model, &createdAt, &updatedAt, &used)
		if !used.Valid || used.Int64 <= 0 {
			continue
		}
		ts := updatedAt.Int64 / 1000
		if ts < todayStart {
			continue
		}
		m := model.String
		if idx := strings.Index(m, ":"); idx >= 0 {
			m = m[idx+1:]
		}
		logs = append(logs, LogEntry{
			Time:          time.Unix(ts, 0).Format("15:04:05"),
			Timestamp:     ts,
			Tool:          "WorkBuddy",
			Model:         normalizeModelName(m),
			InputTokens:   used.Int64,
			OutputTokens:  0,
			TotalTokens:   used.Int64,
			InputCached:   0,
			InputUncached: used.Int64,
		})
	}
	return logs
}

func dedupEvents(events []LogEntry) []LogEntry {
	if len(events) == 0 {
		return nil
	}
	// 按传入顺序决定来源优先级。调用方把 cc-switch 放在官方 Codex 日志前，
	// 优先保留第三方 provider 的真实模型名。
	recentByTokens := make(map[int64][]int64)
	var deduped []LogEntry
	for _, ev := range events {
		duplicate := false
		for _, seenTS := range recentByTokens[ev.TotalTokens] {
			delta := ev.Timestamp - seenTS
			if delta < 0 {
				delta = -delta
			}
			if delta <= dedupWindowSeconds {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		recentByTokens[ev.TotalTokens] = append(recentByTokens[ev.TotalTokens], ev.Timestamp)
		deduped = append(deduped, ev)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		return deduped[i].Timestamp < deduped[j].Timestamp
	})
	return deduped
}

// ───── API: /api/usage (对齐 scanner.py get_today_usage) ─────

func getTodayUsage() UsageResponse {
	todayStart := todayMidnight()

	// 三源并行扫描
	var ccLogs, codexLogs, antigravityLogs, hermesLogs []LogEntry
	var wg sync.WaitGroup
	wg.Add(5)
	go func() { defer wg.Done(); ccLogs = scanCCSwitchLogs(todayStart) }()
	go func() { defer wg.Done(); codexLogs = scanCodexTokens(todayStart) }()
	go func() { defer wg.Done(); antigravityLogs = scanAntigravityTokens() }()
	go func() { defer wg.Done(); hermesLogs = scanHermesTokens(todayStart) }()
	var workbuddyLogs []LogEntry
	go func() { defer wg.Done(); workbuddyLogs = scanWorkBuddyTokens(todayStart) }()
	wg.Wait()

	// 合并去重
	eventsBeforeDedup := len(ccLogs) + len(codexLogs) + len(antigravityLogs) + len(hermesLogs) + len(workbuddyLogs)
	allLogs := append(append(append(append(ccLogs, codexLogs...), antigravityLogs...), hermesLogs...), workbuddyLogs...)
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
			"total_tokens":        totalTokens,
			"input_tokens":        inputTokens,
			"output_tokens":       outputTokens,
			"input_cached":        inputCached,
			"input_uncached":      inputUncached,
			"date":                time.Now().Format("2006-01-02"),
			"deepseek_balance":    dsBalance.Balance,
			"deepseek_currency":   dsBalance.Currency,
			"deepseek_status":     dsBalance.Status,
			"events_after_dedup":  len(allLogs),
			"events_before_dedup": eventsBeforeDedup,
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
	// v1.3.92 标记为 deprecated: 子串匹配会把 gpt-5.5 误匹配到 gpt-5.4,
	// 且会漏掉 cc-switch 出现但不在 models 列表的 model (全部归 Other, 1.6B 累加 bug).
	// 直接用 normalizeModelName (含 minimax/gpt-5.4 等家族折叠规则),
	// 然后在调用方检查 modelData 是否含此 key, 不含就归 Other.
	_ = models
	return normalizeModelName(modelName)
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

	events := dedupEvents(append(append(append(
		scanCCSwitchLogs(startTimestamp),
		scanCodexTokens(startTimestamp)...),
		scanHermesTokens(startTimestamp)...),
		scanWorkBuddyTokens(startTimestamp)...))
	tools := []string{"Hermes", "Codex", "Claude", "OpenCode", "WorkBuddy", "Other"}
	knownTools := map[string]bool{}
	for _, tool := range tools {
		knownTools[tool] = true
	}
	allModels := map[string]struct{}{}
	for _, event := range events {
		allModels[event.Model] = struct{}{}
		if !knownTools[event.Tool] {
			knownTools[event.Tool] = true
			tools = append(tools, event.Tool)
		}
	}
	// 排序 (Other 放最后) — 去掉 Other 再加末尾, 跟 Python 端一致
	models := make([]string, 0, len(allModels))
	for m := range allModels {
		if m != "Other" {
			models = append(models, m)
		}
	}
	sort.Strings(models)
	models = append(models, "Other")

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

	for _, event := range events {
		dStr := time.Unix(event.Timestamp, 0).Format("2006-01-02")
		idx, ok := dateIdx[dStr]
		if !ok {
			continue
		}
		dailyTotals[idx] += event.TotalTokens
		toolData[event.Tool][idx] += event.TotalTokens
		model := event.Model
		if _, ok := modelData[model]; !ok {
			model = "Other"
		}
		modelData[model][idx] += event.TotalTokens
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
	return pickAssetURLForOS(payload, runtime.GOOS)
}

func pickAssetURLForOS(payload map[string]interface{}, targetOS string) string {
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
	// 应用内更新必须选当前平台的可安装资产。Windows 优先直装 EXE，
	// ZIP 只作为旧 release 的兼容回退；macOS 仍优先 DMG。
	suffixes := []string{".dmg", ".zip"}
	if targetOS == "windows" {
		suffixes = []string{"tokenmonitor.exe", ".exe", "tokenmonitor-win.zip", ".zip"}
	}
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
			return normalizeReleaseDownloadURL(v)
		}
	}
	return ""
}

func normalizeReleaseDownloadURL(value string) string {
	value = strings.TrimSpace(value)
	const apiPrefix = "https://api.gitcode.com/"
	if strings.HasPrefix(value, apiPrefix) && strings.Contains(value, "/releases/download/") {
		return "https://gitcode.com/" + strings.TrimPrefix(value, apiPrefix)
	}
	return value
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
	// html_url 是 Release 页面，不能在存在 assets 时抢先当成安装包。
	for _, key := range []string{"download_url", "downloadUrl"} {
		if v, ok := payload[key].(string); ok && v != "" {
			downloadURL = v
			break
		}
	}
	if downloadURL == "" {
		downloadURL = pickAssetURL(payload)
	}
	if downloadURL == "" {
		for _, key := range []string{"html_url", "htmlUrl"} {
			if v, ok := payload[key].(string); ok && v != "" {
				downloadURL = v
				break
			}
		}
	}
	return version, title, notes, normalizeReleaseDownloadURL(downloadURL), true
}

func checkUpdateRemote() map[string]interface{} {
	// 每次请求重读版本号 (对齐 Python 版 _read_app_version() 每次调用都重读)
	currentVer := readAppVersion()

	result := map[string]interface{}{
		"ok":               false,
		"current_version":  currentVer,
		"latest_version":   nil,
		"update_available": false,
		"feed_url":         feedURL,
		"http_status":      nil,
		"error":            nil,
		"raw_excerpt":      nil,
		"title":            nil,
		"download_url":     nil,
	}

	// 未配置更新源时直接返回错误 (对齐 Python 版)
	if feedURL == "" {
		result["error"] = "未配置更新源 (--update-feed-url 参数缺失)"
		return result
	}

	// v1.3.98: 走系统代理 (VPN), 不用默认 http.Client (不读 Windows 注册表代理)
	client := newProxyHTTPClient(8)
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
	// Windows 正式包通过 -X 注入版本。必须优先于外置 version.txt，避免
	// 自更新只替换 EXE 后仍被旧文件误报为旧版本。
	if runtime.GOOS == "windows" && strings.TrimSpace(appVersion) != "" {
		return strings.TrimSpace(appVersion)
	}
	// 开发模式和旧包兼容: 从同目录 version.txt 读取。
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

	// 文件可以永久保留；真正的存活状态由内核锁判断。必须先加锁再截断写 PID，
	// 否则第二个进程会在确认锁失败前破坏第一个进程记录的内容。
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Printf("[server] 无法打开单实例锁文件 %s: %v\n", lockPath, err)
		return false
	}

	// 尝试非阻塞独占锁
	if err := tryLockFile(f); err != nil {
		f.Close()
		return false
	}

	if err := f.Truncate(0); err != nil {
		f.Close()
		return false
	}
	_, _ = f.Seek(0, 0)
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

	var ccLogs, codexLogs, hermesLogs []LogEntry
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); ccLogs = scanCCSwitchLogs(startTimestamp) }()
	go func() { defer wg.Done(); codexLogs = scanCodexTokens(startTimestamp) }()
	go func() { defer wg.Done(); hermesLogs = scanHermesTokens(startTimestamp) }()
	var wbLogs []LogEntry
	go func() { defer wg.Done(); wbLogs = scanWorkBuddyTokens(startTimestamp) }()
	wg.Wait()

	allLogs := append(append(append(ccLogs, codexLogs...), hermesLogs...), wbLogs...)
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
	events := dedupEvents(append(append(append(
		scanCCSwitchLogs(startTimestamp),
		scanCodexTokens(startTimestamp)...),
		scanHermesTokens(startTimestamp)...),
		scanWorkBuddyTokens(startTimestamp)...))
	for _, event := range events {
		dStr := time.Unix(event.Timestamp, 0).Format("2006-01-02")
		dailyTokens[dStr] += event.TotalTokens
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

	var ccLogs, codexLogs, hermesLogs []LogEntry
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); ccLogs = scanCCSwitchLogs(startTimestamp) }()
	go func() { defer wg.Done(); codexLogs = scanCodexTokens(startTimestamp) }()
	go func() { defer wg.Done(); hermesLogs = scanHermesTokens(startTimestamp) }()
	var wbLogs []LogEntry
	go func() { defer wg.Done(); wbLogs = scanWorkBuddyTokens(startTimestamp) }()
	wg.Wait()

	allLogs := append(append(append(ccLogs, codexLogs...), hermesLogs...), wbLogs...)
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
	serverOnly := false
	autoStarted := false
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
		if args[i] == "--server-only" {
			// CI/后台模式: 只跑 HTTP server 不开 GUI (不需要桌面环境)
			serverOnly = true
		}
		if args[i] == autoStartFlag {
			autoStarted = true
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
		// v1.3.92: 解析 days query param (前端 /api/history?days=7 调用)
		days := 30
		if v := r.URL.Query().Get("days"); v != "" {
			if d, err := strconv.Atoi(v); err == nil && d > 0 && d <= 365 {
				days = d
			}
		}
		data := getHistoricalUsage(days)
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
	// 社区 Dashboard API
	http.HandleFunc("/api/community", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		writeJSON(w, 200, getCommunityStats())
	})
	http.HandleFunc("/api/community/optin", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		enabled := r.URL.Query().Get("enabled") != "false"
		setOptIn(enabled)
		if enabled {
			usage := getTodayUsage()
			go reportCommunityStats(&usage)
		}
		writeJSON(w, 200, map[string]interface{}{"ok": true, "opted_in": isOptedIn(), "user_id": getUserID()})
	})
	http.HandleFunc("/api/community/report", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		usage := getTodayUsage()
		result := reportCommunityStats(&usage)
		writeJSON(w, 200, result)
	})
	http.HandleFunc("/api/community/profile", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "status": "method_not_allowed", "message": "仅支持 POST"})
			return
		}
		origin := strings.ToLower(strings.TrimSpace(r.Header.Get("Origin")))
		if origin != "" && !strings.HasPrefix(origin, "http://127.0.0.1:") && !strings.HasPrefix(origin, "http://localhost:") {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{"ok": false, "status": "origin_forbidden", "message": "不允许跨站修改昵称"})
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]interface{}{"ok": false, "status": "invalid_content_type", "message": "请求必须使用 JSON"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var payload struct {
			DisplayName string `json:"display_name"`
		}
		if decoder.Decode(&payload) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "status": "name_invalid", "message": "昵称请求格式不正确"})
			return
		}
		result := updateCommunityProfile(payload.DisplayName)
		status := http.StatusOK
		if !result.OK {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, result)
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

	// v1.4.12: 社区统计自动上报 (opt-in 默认开启, 每 1 小时)
	go func() {
		time.Sleep(30 * time.Second)
		for {
			if isOptedIn() {
				usage := getTodayUsage()
				reportCommunityStats(&usage)
			}
			time.Sleep(1 * time.Hour)
		}
	}()

	// --server-only 模式: 只跑 HTTP server (CI/后台, 不需要桌面环境)
	if serverOnly {
		fmt.Printf("[+] Token Monitor (server-only) http://%s\n", addr)
		if err := server.Serve(ln); err != nil {
			fmt.Printf("[-] 服务器错误: %v\n", err)
		}
		return
	}

	// v1.3.95: HTTP server 在 goroutine 里跑, 主线程交给 startGUI (WebView2/托盘)
	go func() {
		if err := server.Serve(ln); err != nil {
			fmt.Printf("[-] 服务器错误: %v\n", err)
		}
	}()

	fmt.Printf("[+] Token Monitor 仪表盘已启动: http://%s\n", addr)
	fmt.Printf("[+] 更新源: %s\n", feedURL)

	// 等 server 就绪 (端口监听需要一点时间)
	time.Sleep(500 * time.Millisecond)

	// 启动 GUI (Windows: WebView2 内嵌, 其他平台: 阻塞)
	startGUI(port, feedURL, autoStarted)
}
