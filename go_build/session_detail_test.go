package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func resetSessionDetailCacheForTest() {
	sessionDetailCacheMu.Lock()
	defer sessionDetailCacheMu.Unlock()
	sessionDetailCache = map[sessionDetailCacheKey][]SessionMessage{}
	sessionDetailCacheOrder = nil
	sessionDetailInflight = map[sessionDetailCacheKey]chan struct{}{}
}

func writeSessionDetailFixture(t *testing.T, home, sessionID string) string {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "07", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+sessionID+".jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(map[string]interface{}{"type": "event_msg", "payload": map[string]string{"blob": strings.Repeat("x", 2*1024*1024)}}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 30; index++ {
		if err := encoder.Encode(map[string]interface{}{
			"type": "response_item",
			"payload": map[string]interface{}{
				"role":    "user",
				"content": []map[string]string{{"text": "message"}},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSessionDetailSkipsLargeRowsAndReusesSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetSessionDetailCacheForTest()
	sessionID := "session-cache"
	writeSessionDetailFixture(t, home, sessionID)

	originalParser := sessionDetailParser
	defer func() { sessionDetailParser = originalParser }()
	var calls int32
	sessionDetailParser = func(path string, limit int) ([]SessionMessage, error) {
		atomic.AddInt32(&calls, 1)
		return parseSessionMessages(path, limit)
	}

	first := getSessionDetail(sessionID, 1, 20)
	second := getSessionDetail(sessionID, 2, 20)
	if first.Total != 30 || len(first.Messages) != 20 || len(second.Messages) != 10 {
		t.Fatalf("unexpected pagination: first=%+v second=%+v", first, second)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected one parse across pages, got %d", got)
	}
}

func TestSessionDetailCoalescesConcurrentFirstRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetSessionDetailCacheForTest()
	sessionID := "session-concurrent"
	writeSessionDetailFixture(t, home, sessionID)

	originalParser := sessionDetailParser
	defer func() { sessionDetailParser = originalParser }()
	var calls int32
	sessionDetailParser = func(path string, limit int) ([]SessionMessage, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return parseSessionMessages(path, limit)
	}

	var wg sync.WaitGroup
	for index := 0; index < 3; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if detail := getSessionDetail(sessionID, 1, 20); detail.Total != 30 {
				t.Errorf("unexpected total: %d", detail.Total)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected one concurrent parse, got %d", got)
	}
}

func TestWorkBuddySessionDetailReadsMessageContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".workbuddy", "projects", "project", "session-detail.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, row := range []map[string]interface{}{
		{"type": "message", "timestamp": float64(1800000000000), "role": "user", "content": []map[string]string{{"text": "hello"}}},
		{"type": "message", "timestamp": float64(1800000001000), "role": "assistant", "content": []map[string]string{{"text": "world"}}},
	} {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	detail := getSessionDetail("session-detail", 1, 1, "WorkBuddy")
	if detail.DetailSource != "workbuddy" || detail.Total != 2 || len(detail.Messages) != 1 || detail.Messages[0].Text != "hello" {
		t.Fatalf("unexpected WorkBuddy detail: %+v", detail)
	}
}

func TestClaudeSessionDetailReadsNativeJSONLContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetSessionDetailCacheForTest()
	path := filepath.Join(home, ".claude", "projects", "project", "session-claude.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, row := range []map[string]interface{}{
		{"type": "user", "timestamp": "2026-07-18T12:00:00.000Z", "message": map[string]interface{}{"role": "user", "content": []map[string]string{{"type": "text", "text": "hello"}}}},
		{"type": "assistant", "timestamp": "2026-07-18T12:00:01.000Z", "message": map[string]interface{}{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "world"}}}},
	} {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	detail := getSessionDetail("session-claude", 1, 1, "Claude")
	if detail.DetailSource != "claude" || detail.Total != 2 || len(detail.Messages) != 1 || detail.Messages[0].Text != "hello" {
		t.Fatalf("unexpected Claude detail: %+v", detail)
	}
}

func TestClaudeSessionDetailFallsBackWhenNativeLogIsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetSessionDetailCacheForTest()
	detail := getSessionDetail("missing-claude", 1, 20, "Claude")
	if detail.DetailSource != "claude_proxy" || detail.Total != 0 || len(detail.Messages) != 0 {
		t.Fatalf("unexpected Claude fallback: %+v", detail)
	}
}

func TestClaudeSessionDetailMatchesNativeLogByRequestTimestamp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetSessionDetailCacheForTest()
	path := filepath.Join(home, ".claude", "projects", "project", "native-session-id.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, row := range []map[string]interface{}{
		{"type": "user", "timestamp": "2026-07-19T12:00:00.000Z", "sessionId": "native-session-id", "message": map[string]interface{}{"role": "user", "content": "show me the result"}},
		{"type": "assistant", "timestamp": "2026-07-19T12:00:02.000Z", "sessionId": "native-session-id", "message": map[string]interface{}{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "here is the result"}}}},
	} {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	detail := getSessionDetailWithTimestamp("proxy-request-session-id", 1, 20, "1784462401", "Claude")
	if detail.DetailSource != "claude" || len(detail.Messages) != 2 || detail.Messages[0].Text != "show me the result" || detail.Messages[1].Text != "here is the result" {
		t.Fatalf("unexpected Claude timestamp match: %+v", detail)
	}
}

// writeZCodeDBFixture 在 t.TempDir()/.zcode/cli/db/db.sqlite 处创建一个真实 SQLite db,
// 灌入 session / message / part 三张表 (字段对齐 ~/.zcode/cli/db/db.sqlite) 并返回 db 路径。
func writeZCodeDBFixture(t *testing.T, home string, sessions []zcodeFixtureSession) string {
	t.Helper()
	dir := filepath.Join(home, ".zcode", "cli", "db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "db.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE session (
			id text primary key,
			project_id text not null,
			slug text not null,
			directory text not null,
			title text not null,
			version text not null,
			time_created integer not null,
			time_updated integer not null
		)`,
		`CREATE TABLE message (
			id text primary key,
			session_id text not null,
			time_created integer not null,
			time_updated integer not null,
			data text not null
		)`,
		`CREATE TABLE part (
			id text primary key,
			message_id text not null,
			session_id text not null,
			time_created integer not null,
			time_updated integer not null,
			data text not null
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table failed: %v\n%s", err, stmt)
		}
	}
	for _, s := range sessions {
		if _, err := db.Exec(
			`INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, "proj", s.ID, "/tmp", "title", "v1", s.CreatedMs, s.CreatedMs,
		); err != nil {
			t.Fatalf("insert session: %v", err)
		}
		for _, m := range s.Messages {
			data, _ := json.Marshal(map[string]interface{}{"role": m.Role, "time": map[string]int64{"created": m.CreatedMs}})
			if _, err := db.Exec(
				`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
				m.ID, s.ID, m.CreatedMs, m.CreatedMs, string(data),
			); err != nil {
				t.Fatalf("insert message: %v", err)
			}
			for idx, part := range m.Parts {
				pid := m.ID + "-p" + string(rune('0'+idx))
				data, _ := json.Marshal(map[string]interface{}{"type": "text", "text": part.Text})
				if _, err := db.Exec(
					`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
					pid, m.ID, s.ID, m.CreatedMs+int64(idx), m.CreatedMs+int64(idx), string(data),
				); err != nil {
					t.Fatalf("insert part: %v", err)
				}
			}
		}
	}
	return dbPath
}

type zcodeFixtureSession struct {
	ID         string
	CreatedMs  int64
	Messages   []zcodeFixtureMessage
}

type zcodeFixtureMessage struct {
	ID         string
	Role       string
	CreatedMs  int64
	Parts      []zcodeFixturePart
}

type zcodeFixturePart struct {
	Text string
}

func TestZCodeSessionDetailReadsMessageContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeZCodeDBFixture(t, home, []zcodeFixtureSession{{
		ID:        "sess-test-001",
		CreatedMs: 1780000000000,
		Messages: []zcodeFixtureMessage{
			{ID: "msg-u-1", Role: "user", CreatedMs: 1780000001000, Parts: []zcodeFixturePart{{Text: "hello zcode"}}},
			{ID: "msg-a-1", Role: "assistant", CreatedMs: 1780000002000, Parts: []zcodeFixturePart{{Text: "world zcode"}}},
		},
	}})
	detail := getSessionDetail("sess-test-001", 1, 20, "ZCode")
	if detail.DetailSource != "zcode" {
		t.Fatalf("expected detail_source zcode, got %q", detail.DetailSource)
	}
	if detail.Total != 2 || len(detail.Messages) != 2 {
		t.Fatalf("expected 2 messages, got total=%d len=%d (%+v)", detail.Total, len(detail.Messages), detail.Messages)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Text != "hello zcode" {
		t.Fatalf("unexpected first message: %+v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "assistant" || detail.Messages[1].Text != "world zcode" {
		t.Fatalf("unexpected second message: %+v", detail.Messages[1])
	}
}

func TestZCodeSessionDetailEmptyWhenSessionMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeZCodeDBFixture(t, home, []zcodeFixtureSession{{ID: "other-session", CreatedMs: 1780000000000}})
	detail := getSessionDetail("missing-session-id", 1, 20, "ZCode")
	if detail.DetailSource != "zcode" {
		t.Fatalf("expected detail_source zcode, got %q", detail.DetailSource)
	}
	if detail.Total != 0 || len(detail.Messages) != 0 {
		t.Fatalf("expected empty, got total=%d len=%d", detail.Total, len(detail.Messages))
	}
}

func TestZCodeSessionDetailDBMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// 不建 db.sqlite, 验证不崩。
	detail := getSessionDetail("any-id", 1, 20, "ZCode")
	if detail.DetailSource != "zcode" {
		t.Fatalf("expected detail_source zcode, got %q", detail.DetailSource)
	}
	if detail.Total != 0 || len(detail.Messages) != 0 {
		t.Fatalf("expected empty, got total=%d len=%d", detail.Total, len(detail.Messages))
	}
}
