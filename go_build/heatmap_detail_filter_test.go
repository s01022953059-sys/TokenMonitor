package main

import "testing"

func TestHeatmapDetailFilterRecomputesResult(t *testing.T) {
	sessions := []SessionEntry{
		{Time: "07-16 09:00:00", Tool: "Codex", Model: "gpt-5.6-sol", TotalTokens: 100, InputCached: 40},
		{Time: "07-16 10:30:00", Tool: "Claude", Model: "claude-sonnet", TotalTokens: 200},
		{Time: "07-16 11:00:00", Tool: "Codex", Model: "gpt-5.6-sol", TotalTokens: 300, InputCached: 100},
	}
	filtered := filterHeatmapDetailSessions(sessions, "Codex", "", "09:30", "11:30")
	if len(filtered) != 1 || filtered[0].TotalTokens != 300 {
		t.Fatalf("unexpected filtered sessions: %#v", filtered)
	}
	result := paginatedSessionList(filtered, map[string]interface{}{"total_tokens": int64(300), "call_count": 1}, 1, 1)
	if result.Total != 1 || result.TotalPages != 1 || len(result.Sessions) != 1 {
		t.Fatalf("unexpected pagination result: %#v", result)
	}
}

func TestHeatmapDetailFilterOptionsUseCompleteSnapshot(t *testing.T) {
	options := heatmapDetailFilterOptions([]SessionEntry{
		{Tool: "Codex", Model: "gpt-5.6-sol"},
		{Tool: "Claude", Model: "claude-sonnet"},
	})
	if len(options["tools"]) != 2 || options["tools"][0] != "Claude" || options["tools"][1] != "Codex" {
		t.Fatalf("unexpected tool options: %#v", options)
	}
}
