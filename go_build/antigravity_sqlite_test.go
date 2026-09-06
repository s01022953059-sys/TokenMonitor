package main

// v1.5.20: Antigravity (antigravity-tools 代理库) 数据源回归测试。
// 对齐 macOS scanner.py scan_antigravity_tokens 行为 (tests/test_new_sources.py
// 的 AntigravityScannerTests 是同口径的 Python 版):
//   - 数据源: ~/.antigravity_tools/token_stats.db -> token_usage 表
//   - timestamp 是 unix 秒, 零换算
//   - warmup / 0-token 保活记录跳过
//   - cached_tokens 视为 input 子集 (Gemini 风格), clamp 到 input
//   - total = input + output (DB 的 total_tokens 列仅参与 0-token 过滤)
//   - model 过 normalizeModelName, tool 恒为 "Antigravity"

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// makeAntigravitySQLite: 在 t.TempDir()/.antigravity_tools/token_stats.db
// 创建带 token_usage 表的 fixture, rows 每行:
//
//	(timestamp, account_email, model, input, output, total, cached)
//
// HOME 指向临时目录, 顺带隔离其他所有数据源 (与 minimax_sqlite_test.go 同模式)。
func makeAntigravitySQLite(t *testing.T, rows [][7]interface{}) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbDir := filepath.Join(home, ".antigravity_tools")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "token_stats.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE token_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp INTEGER NOT NULL,
			account_email TEXT NOT NULL,
			model TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		_, err = db.Exec(`
			INSERT INTO token_usage
				(timestamp, account_email, model, input_tokens, output_tokens, total_tokens, cached_tokens)
			VALUES (?,?,?,?,?,?,?)`, r[0], r[1], r[2], r[3], r[4], r[5], r[6])
		if err != nil {
			t.Fatal(err)
		}
	}
	return dbPath
}

func TestScanAntigravitySkipsWarmupRows(t *testing.T) {
	now := time.Now().Unix()
	makeAntigravitySQLite(t, [][7]interface{}{
		{now - 300, "a@b.c", "gemini-3-pro-high", int64(1000), int64(200), int64(1200), int64(300)},
		{now - 200, "a@b.c", "gemini-3.6-flash-medium", int64(0), int64(0), int64(0), int64(0)}, // warmup
		{now - 100, "a@b.c", "gemini-pro-agent", int64(0), int64(0), int64(0), int64(0)},        // warmup
	})
	logs := scanAntigravityTokens(now - 3600)
	if len(logs) != 1 {
		t.Fatalf("warmup 0-token 行应被过滤, 期望 1 条, 实际 %d 条", len(logs))
	}
	if logs[0].Tool != "Antigravity" {
		t.Errorf("tool 应为 Antigravity, 实际 %q", logs[0].Tool)
	}
	if logs[0].TotalTokens != 1200 {
		t.Errorf("total 应为 input+output=1200, 实际 %d", logs[0].TotalTokens)
	}
}

func TestScanAntigravityUnixSecondWindow(t *testing.T) {
	now := time.Now().Unix()
	makeAntigravitySQLite(t, [][7]interface{}{
		{now - 7200, "a@b.c", "gemini-3-pro-high", int64(10), int64(5), int64(15), int64(0)}, // 窗口外
		{now - 3600, "a@b.c", "gemini-3-pro-high", int64(20), int64(5), int64(25), int64(0)}, // 窗口内
	})
	logs := scanAntigravityTokens(now - 5400)
	if len(logs) != 1 {
		t.Fatalf("start 窗口过滤失败, 期望 1 条, 实际 %d 条", len(logs))
	}
	// timestamp 是 unix 秒, 必须原样透传 (不是毫秒量级)
	if logs[0].Timestamp != now-3600 {
		t.Errorf("timestamp 应为 unix 秒原值 %d, 实际 %d", now-3600, logs[0].Timestamp)
	}
}

func TestScanAntigravityCachedClampedAndModelNormalized(t *testing.T) {
	now := time.Now().Unix()
	makeAntigravitySQLite(t, [][7]interface{}{
		// cached(900) > input(500): clamp 到 500, uncached=0
		{now - 300, "a@b.c", "Claude-Opus-4-6-Thinking", int64(500), int64(100), int64(600), int64(900)},
		// 正常: cached(300) ⊆ input(1000), 模型名带日期后缀应被剥掉
		{now - 200, "a@b.c", "Gemini-3-Pro-High-2026-01-01", int64(1000), int64(200), int64(1200), int64(300)},
	})
	logs := scanAntigravityTokens(now - 3600)
	if len(logs) != 2 {
		t.Fatalf("期望 2 条, 实际 %d 条", len(logs))
	}
	// ORDER BY timestamp ASC: 第一条是 cached clamp 行
	if logs[0].InputCached != 500 || logs[0].InputUncached != 0 {
		t.Errorf("cached 应 clamp 到 input: cached=%d uncached=%d", logs[0].InputCached, logs[0].InputUncached)
	}
	if logs[0].Model != "claude-opus-4-6-thinking" {
		t.Errorf("model 应归一化为小写, 实际 %q", logs[0].Model)
	}
	if logs[1].Model != "gemini-3-pro-high" {
		t.Errorf("model 日期后缀应被剥掉, 实际 %q", logs[1].Model)
	}
	if logs[1].InputCached != 300 || logs[1].InputUncached != 700 || logs[1].InputTokens != 1000 {
		t.Errorf("缓存拆分错误: cached=%d uncached=%d input=%d",
			logs[1].InputCached, logs[1].InputUncached, logs[1].InputTokens)
	}
}

func TestScanAntigravityMissingDBReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if logs := scanAntigravityTokens(time.Now().Unix() - 3600); logs != nil {
		t.Errorf("DB 不存在时应返回 nil, 实际 %d 条", len(logs))
	}
}

func TestScanAntigravityMissingTableReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbDir := filepath.Join(home, ".antigravity_tools")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "token_stats.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 建一个无关表, 不建 token_usage (模拟代理旧版本 schema)
	if _, err := db.Exec(`CREATE TABLE accounts (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if logs := scanAntigravityTokens(time.Now().Unix() - 3600); logs != nil {
		t.Errorf("token_usage 表缺失时应静默返回 nil, 实际 %d 条", len(logs))
	}
}

// 聚合级: getHistoricalUsage 的拼接链与默认 tools 列表必须包含 Antigravity,
// 否则出现"首页有 Antigravity、历史趋势没有"的口径分裂。
func TestGetHistoricalUsageIncludesAntigravity(t *testing.T) {
	now := time.Now().Unix()
	makeAntigravitySQLite(t, [][7]interface{}{
		{now - 300, "a@b.c", "gemini-3-pro-high", int64(1000), int64(200), int64(1200), int64(300)},
		{now - 200, "a@b.c", "gemini-3.6-flash-medium", int64(0), int64(0), int64(0), int64(0)}, // warmup 不计
	})
	hist := getHistoricalUsage(1)
	var byTool map[string][]int64
	if err := json.Unmarshal(hist.ByTool, &byTool); err != nil {
		t.Fatal(err)
	}
	series, ok := byTool["Antigravity"]
	if !ok {
		t.Fatalf("history by_tool 缺少 Antigravity 序列, 实际 keys: %v", keysOf(byTool))
	}
	if len(series) != 1 || series[0] != 1200 {
		t.Errorf("Antigravity 当日应为 1200, 实际 %v", series)
	}
	if hist.Values[len(hist.Values)-1] != 1200 {
		t.Errorf("当日总量应为 1200, 实际 %d", hist.Values[len(hist.Values)-1])
	}
}

// 聚合级: 今日用量 / 会话列表 / 热力图 三处也必须包含 Antigravity 事件。
func TestTodaySessionHeatmapIncludeAntigravity(t *testing.T) {
	now := time.Now().Unix()
	makeAntigravitySQLite(t, [][7]interface{}{
		{now - 300, "a@b.c", "gemini-3-pro-high", int64(1000), int64(200), int64(1200), int64(300)},
	})

	today := getTodayUsage()
	stats, ok := today.ByTool["Antigravity"]
	if !ok {
		t.Fatalf("today by_tool 缺少 Antigravity, 实际 keys: %v", keysOfPtr(today.ByTool))
	}
	if stats.TotalTokens != 1200 {
		t.Errorf("today Antigravity 应为 1200, 实际 %d", stats.TotalTokens)
	}

	sessions := getSessionList(1, 1, 50)
	found := false
	for _, s := range sessions.Sessions {
		if s.Tool == "Antigravity" && s.TotalTokens == 1200 {
			found = true
		}
	}
	if !found {
		t.Error("session list 缺少 Antigravity 事件")
	}

	heatmap := getHeatmapData(1)
	if len(heatmap.Days) != 1 || heatmap.Days[0].Tokens != 1200 {
		t.Errorf("heatmap 当日应为 1200, 实际 %+v", heatmap.Days)
	}
}

func keysOf(m map[string][]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfPtr(m map[string]*ToolStats) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
