package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestScanCodexTokensReadsOfficialDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".codex", "logs_2.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE logs (
		id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, ts_nanos INTEGER NOT NULL,
		target TEXT NOT NULL, feedback_log_body TEXT, thread_id TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	envelope := map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id": "resp_test", "completed_at": int64(1800000000), "model": "gpt-test",
			"usage": map[string]interface{}{
				"input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
				"input_tokens_details": map[string]interface{}{"cached_tokens": 60},
			},
		},
	}
	body, _ := json.Marshal(envelope)
	if _, err = db.Exec(`INSERT INTO logs(ts, ts_nanos, target, feedback_log_body, thread_id)
		VALUES (?, 0, 'codex_api::sse::responses', ?, 'thread-test')`,
		int64(1800000000), "SSE event: "+string(body)); err != nil {
		t.Fatal(err)
	}

	events := scanCodexTokens(1799999000)
	if len(events) != 1 {
		t.Fatalf("expected one Codex event, got %d", len(events))
	}
	if events[0].Tool != "Codex" || events[0].Model != "gpt-test" || events[0].TotalTokens != 120 || events[0].InputCached != 60 {
		t.Fatalf("unexpected event: %+v", events[0])
	}
}

func TestDedupEventsPrefersFirstSourceAcrossBucketBoundary(t *testing.T) {
	ccEvent := LogEntry{Timestamp: 101, TotalTokens: 120, Model: "glm-provider"}
	codexEvent := LogEntry{Timestamp: 102, TotalTokens: 120, Model: "gpt-declared"}
	events := dedupEvents([]LogEntry{ccEvent, codexEvent})
	if len(events) != 1 || events[0].Model != "glm-provider" {
		t.Fatalf("expected cc-switch event to win, got %+v", events)
	}
}

func TestScanCodexRolloutsSkipsRepeatedCumulativeUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-duplicate.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(file)
	cumulative := map[string]int{"input_tokens": 100, "cached_input_tokens": 60, "output_tokens": 20, "total_tokens": 120}
	rows := []map[string]interface{}{
		{"timestamp": "2027-01-15T08:00:01Z", "type": "event_msg", "payload": map[string]interface{}{"type": "token_count", "info": map[string]interface{}{"total_token_usage": cumulative, "last_token_usage": cumulative}}},
		{"timestamp": "2027-01-15T08:00:10Z", "type": "event_msg", "payload": map[string]interface{}{"type": "token_count", "info": map[string]interface{}{"total_token_usage": cumulative, "last_token_usage": map[string]int{"total_tokens": 999}}}},
	}
	for _, row := range rows {
		body, _ := json.Marshal(row)
		fmt.Fprintln(w, string(body))
	}
	w.Flush()
	file.Close()

	events := scanCodexRollouts(1700000000)
	if len(events) != 1 || events[0].TotalTokens != 120 {
		t.Fatalf("expected one non-duplicate event, got %+v", events)
	}
}

func TestScanCodexRolloutsContinuesAfterLargeJSONLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-large.jsonl")
	large := map[string]interface{}{
		"timestamp": "2027-01-15T08:00:00Z", "type": "response_item",
		"payload": map[string]interface{}{"content": strings.Repeat("x", 5*1024*1024)},
	}
	token := map[string]interface{}{
		"timestamp": "2027-01-15T08:00:01Z", "type": "event_msg",
		"payload": map[string]interface{}{"type": "token_count", "info": map[string]interface{}{
			"total_token_usage": map[string]int{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120},
			"last_token_usage":  map[string]int{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120},
		}},
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []map[string]interface{}{large, token} {
		body, _ := json.Marshal(row)
		if _, err := fmt.Fprintln(file, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()

	events := scanCodexRollouts(1700000000)
	if len(events) != 1 || events[0].TotalTokens != 120 {
		t.Fatalf("expected token event after large line, got %+v", events)
	}
}
