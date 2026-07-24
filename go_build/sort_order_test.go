package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestHistoryByToolOtherAlwaysLast 验证 /api/history 的 by_tool JSON key 顺序:
// Other 必须在末尾, 而非字母序位置。Go map 序列化按字母序, 需手动保序。
func TestHistoryByToolOtherAlwaysLast(t *testing.T) {
	tools := []string{"Hermes", "Codex", "ZCode", "MiniMax Code", "Claude", "OpenCode", "WorkBuddy", "Other"}
	data := map[string][]int64{}
	for _, t := range tools {
		data[t] = []int64{100, 200}
	}

	raw, err := orderedMapJSON(tools, func(k string) (interface{}, error) {
		return data[k], nil
	})
	if err != nil {
		t.Fatalf("orderedMapJSON error: %v", err)
	}

	// 用 json.Decoder 的 Token API 验证 key 顺序
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		t.Fatalf("expected '{', got %v %v", tok, err)
	}
	var keyOrder []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("token error: %v", err)
		}
		keyOrder = append(keyOrder, k.(string))
		// skip value
		var val interface{}
		dec.Decode(&val)
	}
	if len(keyOrder) != len(tools) {
		t.Fatalf("key count mismatch: %d vs %d", len(keyOrder), len(tools))
	}
	if keyOrder[len(keyOrder)-1] != "Other" {
		t.Fatalf("Other is not last in by_tool: %v", keyOrder)
	}
	// 前面的 key 应与 tools 切片顺序一致
	for i, k := range keyOrder {
		if k != tools[i] {
			t.Fatalf("key order mismatch at %d: got %s want %s", i, k, tools[i])
		}
	}
}

// TestHistoryByModelOtherAlwaysLast 同上, 验证 by_model。
func TestHistoryByModelOtherAlwaysLast(t *testing.T) {
	models := []string{"gpt-5.6", "glm-5.2", "minimax-m3", "Other"}
	data := map[string][]int64{}
	for _, m := range models {
		data[m] = []int64{10, 20}
	}

	raw, err := orderedMapJSON(models, func(k string) (interface{}, error) {
		return data[k], nil
	})
	if err != nil {
		t.Fatalf("orderedMapJSON error: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.Token() // {
	var keyOrder []string
	for dec.More() {
		k, _ := dec.Token()
		keyOrder = append(keyOrder, k.(string))
		var val interface{}
		dec.Decode(&val)
	}
	if keyOrder[len(keyOrder)-1] != "Other" {
		t.Fatalf("Other is not last in by_model: %v", keyOrder)
	}
}

// TestDedupEventsStable 验证去重排序是稳定的: 相同时间戳的事件保持原始顺序。
func TestDedupEventsStable(t *testing.T) {
	baseTs := int64(1800000000)
	// 两条相同时间戳不同 token (不会被去重), 验证顺序保持
	events := []LogEntry{
		{Timestamp: baseTs, Tool: "ZCode", Model: "glm-5.2", TotalTokens: 100, SessionID: "first"},
		{Timestamp: baseTs, Tool: "Codex", Model: "gpt-5.6", TotalTokens: 200, SessionID: "second"},
	}
	deduped := dedupEvents(events)
	if len(deduped) != 2 {
		t.Fatalf("expected 2 events, got %d", len(deduped))
	}
	// 稳定排序: 相同 timestamp 时 first 应在前
	if deduped[0].SessionID != "first" || deduped[1].SessionID != "second" {
		t.Fatalf("stable order not preserved: %s, %s", deduped[0].SessionID, deduped[1].SessionID)
	}
}
