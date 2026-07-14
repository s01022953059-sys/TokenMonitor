package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
