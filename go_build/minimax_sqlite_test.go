package main

// v1.5.13: Windows 端 MiniMax Code 数据源升级回归测试。
// 对齐 macOS scanner.py v1.5.11/1.5.12 行为:
//   - SQLite 主源: ~/.minimax/v2/sqlite/runtime-state.sqlite -> local_runtime_token_usage
//   - mvs_ 前缀 session_id 过滤
//   - 毫秒 ts 转秒
//   - total = input + output, reasoning 不计入
//   - cache_read + cache_write -> input_cached
//   - JSONL 兜底 + 跨源 turn_id 去重
//   - normalizeModelName 剥 custom_provider: / custom-local: 前缀

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// makeMiniMaxSQLite: 在 t.TempDir()/.minimax/v2/sqlite/runtime-state.sqlite
// 创建带 local_runtime_token_usage 表的 fixture, rows 每行:
//   (turn_id, session_id, model, ts_ms, input, output, reasoning, cache_read, cache_write)
func makeMiniMaxSQLite(t *testing.T, rows [][9]interface{}) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbDir := filepath.Join(home, ".minimax", "v2", "sqlite")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "runtime-state.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE local_runtime_token_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			agent_name TEXT,
			framework_type TEXT,
			turn_id TEXT,
			model TEXT,
			ts INTEGER NOT NULL,
			input_tokens INTEGER NOT NULL,
			output_tokens INTEGER NOT NULL,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			cache_write_tokens INTEGER NOT NULL DEFAULT 0,
			cost_usd REAL,
			raw TEXT
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		_, err := db.Exec(
			`INSERT INTO local_runtime_token_usage
			   (turn_id, session_id, model, ts, input_tokens, output_tokens,
			    reasoning_tokens, cache_read_tokens, cache_write_tokens)
			   VALUES (?,?,?,?,?,?,?,?,?)`,
			row[0], row[1], row[2], row[3], row[4], row[5], row[6], row[7], row[8],
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// --- SQLite 主源测试 ---

func TestScanMiniMaxSQLiteMillisecondTimestampConvertedToSeconds(t *testing.T) {
	makeMiniMaxSQLite(t, [][9]interface{}{
		{"turn-1", "mvs_aaa", "custom_provider:zhipu-maas/glm-5.2",
			int64(1_800_000_000_000), int64(100), int64(20), int64(0), int64(0), int64(0)},
	})
	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Timestamp != 1_800_000_000 {
		t.Errorf("expected timestamp %d, got %d", 1_800_000_000, events[0].Timestamp)
	}
	if events[0].Tool != "MiniMax Code" {
		t.Errorf("expected tool 'MiniMax Code', got %q", events[0].Tool)
	}
	if events[0].TurnID != "turn-1" {
		t.Errorf("expected turn_id 'turn-1', got %q", events[0].TurnID)
	}
}

func TestScanMiniMaxSQLiteTotalIsInputPlusOutput(t *testing.T) {
	makeMiniMaxSQLite(t, [][9]interface{}{
		{"t1", "mvs_aaa", "glm-5.2",
			int64(1_800_000_000_000), int64(100), int64(20), int64(999), int64(0), int64(0)},
	})
	e := scanMiniMaxTokens(1_700_000_000)
	if len(e) != 1 {
		t.Fatalf("expected 1 event, got %d", len(e))
	}
	if e[0].TotalTokens != 120 {
		t.Errorf("reasoning_tokens (999) should NOT be counted; got total=%d, want 120",
			e[0].TotalTokens)
	}
	if e[0].InputUncached != 100 || e[0].OutputTokens != 20 || e[0].InputCached != 0 {
		t.Errorf("unexpected breakdown: uncached=%d out=%d cached=%d",
			e[0].InputUncached, e[0].OutputTokens, e[0].InputCached)
	}
}

func TestScanMiniMaxSQLiteCacheSplitIntoInputCached(t *testing.T) {
	makeMiniMaxSQLite(t, [][9]interface{}{
		{"t1", "mvs_aaa", "glm-5.2",
			int64(1_800_000_000_000), int64(100), int64(20), int64(0), int64(50), int64(30)},
	})
	e := scanMiniMaxTokens(1_700_000_000)
	if len(e) != 1 {
		t.Fatalf("expected 1 event, got %d", len(e))
	}
	if e[0].InputCached != 80 {
		t.Errorf("cache_read+cache_write = %d, want 80", e[0].InputCached)
	}
	if e[0].InputTokens != 180 {
		t.Errorf("input_tokens = %d, want 180 (100 uncached + 80 cached)", e[0].InputTokens)
	}
	if e[0].TotalTokens != 120 {
		t.Errorf("total_tokens = %d, want 120 (input_uncached + output)", e[0].TotalTokens)
	}
}

func TestScanMiniMaxSQLiteSessionIDMvsPrefixFilter(t *testing.T) {
	makeMiniMaxSQLite(t, [][9]interface{}{
		{"t1", "mvs_aaaaaa", "glm-5.2", int64(1_800_000_000_000), int64(100), int64(20), int64(0), int64(0), int64(0)},
		{"t2", "mvs_bbbbbb", "glm-5.2", int64(1_800_000_010_000), int64(100), int64(20), int64(0), int64(0), int64(0)},
		{"t3", "other_xxx", "glm-5.2", int64(1_800_000_020_000), int64(999), int64(99), int64(0), int64(0), int64(0)},
		{"t4", "px_yyyy", "glm-5.2", int64(1_800_000_030_000), int64(999), int64(99), int64(0), int64(0), int64(0)},
	})
	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (mvs_ only), got %d", len(events))
	}
	gotIDs := map[string]bool{}
	for _, e := range events {
		gotIDs[e.SessionID] = true
	}
	if !gotIDs["mvs_aaaaaa"] || !gotIDs["mvs_bbbbbb"] {
		t.Errorf("expected mvs_aaaaaa & mvs_bbbbbb, got %v", gotIDs)
	}
	if gotIDs["other_xxx"] || gotIDs["px_yyyy"] {
		t.Errorf("non-mvs_ sessions leaked through: %v", gotIDs)
	}
}

func TestScanMiniMaxSQLiteSkipsZeroTotalRows(t *testing.T) {
	makeMiniMaxSQLite(t, [][9]interface{}{
		{"t1", "mvs_aaa", "glm-5.2", int64(1_800_000_000_000), int64(0), int64(0), int64(0), int64(50), int64(30)},
		{"t2", "mvs_bbb", "glm-5.2", int64(1_800_000_001_000), int64(100), int64(20), int64(0), int64(0), int64(0)},
	})
	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 1 {
		t.Fatalf("expected 1 event (zero-total skipped), got %d", len(events))
	}
	if events[0].SessionID != "mvs_bbb" {
		t.Errorf("wrong event kept: %s", events[0].SessionID)
	}
}

func TestScanMiniMaxSQLiteSkipsRowsOutsideTimeWindow(t *testing.T) {
	makeMiniMaxSQLite(t, [][9]interface{}{
		{"t1", "mvs_aaa", "glm-5.2", int64(1_600_000_000_000), int64(100), int64(20), int64(0), int64(0), int64(0)}, // 太早
		{"t2", "mvs_bbb", "glm-5.2", int64(1_800_000_000_000), int64(100), int64(20), int64(0), int64(0), int64(0)}, // 命中
	})
	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 1 || events[0].SessionID != "mvs_bbb" {
		t.Fatalf("expected only mvs_bbb, got %+v", events)
	}
}

func TestScanMiniMaxSQLiteMissingDBReturnsEmpty(t *testing.T) {
	// HOME 是空目录, 没有 .minimax/, scanMiniMaxTokens 应返回 nil/空
	home := t.TempDir()
	t.Setenv("HOME", home)
	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 0 {
		t.Errorf("expected 0 events when db missing, got %d", len(events))
	}
}

func TestScanMiniMaxSQLiteMissingTableReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbDir := filepath.Join(home, ".minimax", "v2", "sqlite")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dbDir, "runtime-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	// 创建一个无关表, 不创建 local_runtime_token_usage
	if _, err := db.Exec(`CREATE TABLE other_stuff (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 0 {
		t.Errorf("expected 0 events when table missing, got %d", len(events))
	}
}

func TestScanMiniMaxSQLiteMissingRequiredColumnReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbDir := filepath.Join(home, ".minimax", "v2", "sqlite")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dbDir, "runtime-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	// 表存在但缺必需列 (input_tokens)
	if _, err := db.Exec(`
		CREATE TABLE local_runtime_token_usage (
			id INTEGER, session_id TEXT, ts INTEGER, output_tokens INTEGER
		)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 0 {
		t.Errorf("expected 0 events when required column missing, got %d", len(events))
	}
}

// --- 跨源去重 (mergeMiniMaxSources) 测试 ---

func TestMergeMiniMaxSourcesDedupesByTurnID(t *testing.T) {
	sqliteLogs := []LogEntry{{
		TurnID: "t1", SessionID: "mvs_aaa", Timestamp: 1_800_000_000, TotalTokens: 150,
	}}
	jsonlLogs := []LogEntry{
		{TurnID: "t1", SessionID: "mvs_aaa", Timestamp: 1_800_000_000, TotalTokens: 999},
		{TurnID: "t2", SessionID: "mvs_bbb", Timestamp: 1_800_000_010, TotalTokens: 230},
	}
	merged := mergeMiniMaxSources(sqliteLogs, jsonlLogs)
	if len(merged) != 2 {
		t.Fatalf("expected 2 (t1 from sqlite + t2 from jsonl), got %d", len(merged))
	}
	// SQLite 的 t1 必须保留 (不是 JSONL 的 999 那条)
	t1 := LogEntry{}
	for _, e := range merged {
		if e.TurnID == "t1" {
			t1 = e
		}
	}
	if t1.TotalTokens != 150 {
		t.Errorf("SQLite t1 should win with total=150, got %d", t1.TotalTokens)
	}
}

func TestMergeMiniMaxSourcesFallsBackToSessionTS(t *testing.T) {
	// 没有 turn_id 的旧 JSONL 事件用 (session_id, timestamp) 兜底去重
	sqliteLogs := []LogEntry{{
		TurnID: "t1", SessionID: "mvs_aaa", Timestamp: 1_800_000_000, TotalTokens: 150,
	}}
	jsonlLogs := []LogEntry{
		{TurnID: "", SessionID: "mvs_aaa", Timestamp: 1_800_000_000, TotalTokens: 999}, // 撞 sqlite, 应被丢
		{TurnID: "", SessionID: "mvs_bbb", Timestamp: 1_800_000_000, TotalTokens: 230}, // 不同 session, 应保留
	}
	merged := mergeMiniMaxSources(sqliteLogs, jsonlLogs)
	if len(merged) != 2 {
		t.Fatalf("expected 2 (sqlite + jsonl mvs_bbb), got %d", len(merged))
	}
	// mvs_aaa 应是 sqlite 的 150
	var aaa *LogEntry
	for i := range merged {
		if merged[i].SessionID == "mvs_aaa" {
			aaa = &merged[i]
		}
	}
	if aaa == nil || aaa.TotalTokens != 150 {
		t.Errorf("sqlite mvs_aaa should win with total=150, got %+v", aaa)
	}
}

func TestMergeMiniMaxSourcesEmptyInputs(t *testing.T) {
	if got := mergeMiniMaxSources(nil, nil); len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
	if got := mergeMiniMaxSources([]LogEntry{{TurnID: "t1"}}, nil); len(got) != 1 {
		t.Errorf("sqlite-only should pass through, got %d", len(got))
	}
	if got := mergeMiniMaxSources(nil, []LogEntry{{TurnID: "t1"}}); len(got) != 1 {
		t.Errorf("jsonl-only should pass through, got %d", len(got))
	}
}

// --- JSONL 兜底回归测试 (v1 兼容) ---

func TestScanMiniMaxJSONLFallbackISORead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// 故意不创建 SQLite (只放 JSONL), 走 v1 兜底
	sessionsDir := filepath.Join(home, ".pi", "agent", "sessions", "proj")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	item := map[string]interface{}{
		"type":      "message",
		"timestamp": "2026-05-26T10:00:01.000Z",
		"message": map[string]interface{}{
			"role":  "assistant",
			"model": "gpt-5.5",
			"usage": map[string]interface{}{
				"input":       100,
				"output":      20,
				"totalTokens": 120,
			},
		},
	}
	body, _ := json.Marshal(item)
	path := filepath.Join(sessionsDir, "s1.jsonl")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 1 {
		t.Fatalf("expected 1 event from JSONL fallback, got %d", len(events))
	}
	if events[0].Tool != "MiniMax Code" || events[0].Model != "gpt-5.5" {
		t.Errorf("unexpected event: %+v", events[0])
	}
	if events[0].TotalTokens != 120 {
		t.Errorf("total_tokens = %d, want 120", events[0].TotalTokens)
	}
}

func TestScanMiniMaxJSONLFallbackAnthropicCacheSplit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionsDir := filepath.Join(home, ".pi", "agent", "sessions", "proj")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	item := map[string]interface{}{
		"type":      "message",
		"timestamp": "2026-05-26T10:00:01.000Z",
		"message": map[string]interface{}{
			"role":  "assistant",
			"model": "custom_provider:zhipu-maas/glm-5.2",
			"usage": map[string]interface{}{
				"input":       100,
				"output":      20,
				"cacheRead":   50,
				"cacheWrite":  30,
				"totalTokens": 200,
			},
		},
	}
	body, _ := json.Marshal(item)
	path := filepath.Join(sessionsDir, "s1.jsonl")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	events := scanMiniMaxTokens(1_700_000_000)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Model != "glm-5.2" {
		t.Errorf("JSONL 也应剥 custom_provider: 前缀, got %q", events[0].Model)
	}
	if events[0].InputCached != 80 {
		t.Errorf("cache_read+cache_write = %d, want 80", events[0].InputCached)
	}
}

// --- normalizeModelName 表驱动测试 ---

func TestNormalizeModelNameStripsCustomProviderPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"custom_provider:zhipu-maas/glm-5.2", "glm-5.2"},
		{"custom_provider:opencode-go/kimi-k3", "kimi-k3"},
		{"custom_provider:opencode-go/deepseek-v4-flash", "deepseek-v4-flash"},
		{"custom-local:MiniMax-M3", "minimax-m3"},
		{"CUSTOM_PROVIDER:ZhIPu-MAAS/GLM-5.2", "glm-5.2"}, // 大小写归一化在前
		{"custom_provider:somemodel", "somemodel"},       // 防御: 无 / 分隔
		// 不动的
		{"glm-5.2", "glm-5.2"},
		{"gpt-5.6", "gpt-5.6"},
		{"qwen3.6-plus", "qwen3.6-plus"},
		{"qwen3.6-Plus", "qwen3.6-plus"},
		{"qwen3.6-plus-2026-04-02", "qwen3.6-plus"},
		{"qwen3.6-plus-vl", "qwen3.6-plus-vl"},
		{"minimax-m3-free", "minimax-m3"},
		// 空 / 兜底
		{"", "Other"},
	}
	for _, c := range cases {
		got := normalizeModelName(c.in)
		if got != c.want {
			t.Errorf("normalizeModelName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- 集成: SQLite + JSONL 同 turn 双写, SQLite 优先 ---

func TestScanMiniMaxSQLiteWinsOverJSONLForSameTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// 1) SQLite: turn_id=t1, total=150
	dbDir := filepath.Join(home, ".minimax", "v2", "sqlite")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dbDir, "runtime-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`
		CREATE TABLE local_runtime_token_usage (
			id INTEGER, session_id TEXT, turn_id TEXT, model TEXT, ts INTEGER,
			input_tokens INTEGER, output_tokens INTEGER, reasoning_tokens INTEGER,
			cache_read_tokens INTEGER, cache_write_tokens INTEGER
		)`)
	_, _ = db.Exec(
		`INSERT INTO local_runtime_token_usage VALUES
		   (1, 'mvs_aaa', 't1', 'custom_provider:glm-5.2',
		    1800000000000, 100, 50, 0, 0, 0)`)
	db.Close()

	// 2) JSONL: 同一 turn, totalTokens 一致, 但 model 不同 (伪装漏算)
	sessionsDir := filepath.Join(home, ".pi", "agent", "sessions", "proj")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	isoTime := time.Unix(1_800_000_000, 0).UTC().Format(time.RFC3339Nano)
	item := map[string]interface{}{
		"type": "message", "timestamp": isoTime,
		"message": map[string]interface{}{
			"role": "assistant", "model": "different-model",
			"usage": map[string]interface{}{
				"input":       100,
				"output":      50,
				"totalTokens": 150,
			},
		},
	}
	body, _ := json.Marshal(item)
	if err := os.WriteFile(filepath.Join(sessionsDir, "s1.jsonl"), append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	events := scanMiniMaxTokens(1_700_000_000)
	// SQLite 的 turn_id=t1 应保留 (model=glm-5.2, 剥了前缀),
	// JSONL 那条因 turn_id 撞上被丢, 但 _dedupEvents 还会按 time+total 二次去重,
	// 最终只能剩 1 条且是 SQLite 的。
	if len(events) != 1 {
		t.Fatalf("expected 1 event after dedup, got %d: %+v", len(events), events)
	}
	if events[0].Model != "glm-5.2" {
		t.Errorf("SQLite should win with model 'glm-5.2', got %q", events[0].Model)
	}
	if events[0].TotalTokens != 150 {
		t.Errorf("total = %d, want 150", events[0].TotalTokens)
	}
}

// 防止以后误删对 sessions 路径的依赖, 至少确认调用 minimaxSessionsPath 不 panic
func TestMiniMaxSessionsPathIsStable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := minimaxSessionsPath()
	want := filepath.Join(home, ".pi", "agent", "sessions")
	if !strings.HasSuffix(got, want) && got != want {
		t.Errorf("minimaxSessionsPath = %q, want suffix %q", got, want)
	}
}

func TestMiniMaxDBPathIsStable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := minimaxDBPath()
	want := filepath.Join(home, ".minimax", "v2", "sqlite", "runtime-state.sqlite")
	if got != want {
		t.Errorf("minimaxDBPath = %q, want %q", got, want)
	}
}