package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestTodayWindowSupportsLocalAndUTCBucketing(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("CST", 8*60*60)
	t.Cleanup(func() { time.Local = originalLocal })
	now := time.Date(2026, 7, 13, 3, 30, 0, 0, time.Local)

	utcStart, utcDate, utcLabel := todayWindow(true, now)
	localStart, localDate, localLabel := todayWindow(false, now)

	if utcStart != time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Unix() || utcDate != "2026-07-12" || utcLabel != "UTC+0" {
		t.Fatalf("unexpected UTC window: %d %s %s", utcStart, utcDate, utcLabel)
	}
	if localStart != time.Date(2026, 7, 13, 0, 0, 0, 0, time.Local).Unix() || localDate != "2026-07-13" || localLabel != "CST" {
		t.Fatalf("unexpected local window: %d %s %s", localStart, localDate, localLabel)
	}
}

func TestCCTokenBreakdownCacheSemantics(t *testing.T) {
	input, total, cached, uncached := ccTokenBreakdown("claude", 100, 30, 500, 20)
	if input != 620 || total != 650 || cached != 500 || uncached != 120 {
		t.Fatalf("unexpected Anthropic breakdown: %d %d %d %d", input, total, cached, uncached)
	}
	input, total, cached, uncached = ccTokenBreakdown("codex", 1000, 50, 600, 0)
	if input != 1000 || total != 1050 || cached != 600 || uncached != 400 {
		t.Fatalf("unexpected OpenAI breakdown: %d %d %d %d", input, total, cached, uncached)
	}
}

func TestCodexSessionKeepsEventModelAfterProviderSwitch(t *testing.T) {
	providers := map[string]string{"current-provider": "gpt-5.5"}
	if got := resolveCCSwitchModel("_codex_session", "gpt-5.6-sol", providers); got != "gpt-5.6-sol" {
		t.Fatalf("historical session model was rewritten: %q", got)
	}
	if got := resolveCCSwitchModel("current-provider", "gpt-declared", providers); got != "gpt-5.5" {
		t.Fatalf("concrete provider model was not resolved: %q", got)
	}
}

func TestScanWorkBuddyProjectsReadsPerRequestUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".workbuddy", "projects", "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	item := map[string]interface{}{
		"type": "message", "timestamp": int64(1800000000000), "role": "assistant",
		"providerData": map[string]interface{}{
			"model": "MiniMax-M3",
			"usage": map[string]interface{}{
				"inputTokens": 100, "outputTokens": 20, "totalTokens": 120,
				"inputTokensDetails": []map[string]int{{"cached_tokens": 60}},
			},
		},
	}
	body, _ := json.Marshal(item)
	if err := os.WriteFile(filepath.Join(dir, "session-test.jsonl"), append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	events := scanWorkBuddyTokens(1700000000)
	if len(events) != 1 {
		t.Fatalf("expected one WorkBuddy event, got %d", len(events))
	}
	event := events[0]
	if event.Model != "minimax-m3" || event.TotalTokens != 120 || event.InputCached != 60 || event.SessionID != "session-test" {
		t.Fatalf("unexpected WorkBuddy event: %+v", event)
	}
}

func TestScanHermesUsesEndTimeAndCacheWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".hermes", "state.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT, started_at REAL, ended_at REAL, model TEXT,
		input_tokens INTEGER, output_tokens INTEGER,
		cache_read_tokens INTEGER, cache_write_tokens INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions VALUES ('s1', ?, ?, 'glm-test', 100, 20, 300, 40)`, int64(1700000000), int64(1800000000)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	events := scanHermesTokens(1799999000)
	if len(events) != 1 {
		t.Fatalf("expected one Hermes event, got %d", len(events))
	}
	event := events[0]
	if event.Timestamp != 1800000000 || event.TotalTokens != 460 || event.InputCached != 300 || event.InputUncached != 140 {
		t.Fatalf("unexpected Hermes event: %+v", event)
	}
}
