package main

import "testing"

func TestBuildUsageBreakdownsIncludesToolModelMatrix(t *testing.T) {
	logs := []LogEntry{
		{Tool: "Codex", Model: "gpt-5.5", TotalTokens: 100, InputTokens: 80, InputCached: 40},
		{Tool: "Codex", Model: "glm-5.2", TotalTokens: 200, InputTokens: 160, InputCached: 120},
		{Tool: "Claude", Model: "gpt-5.5", TotalTokens: 50, InputTokens: 40, InputCached: 0},
	}
	byTool, byModel, byModelRequests, byModelInput, byModelCached, byToolModel := buildUsageBreakdowns(logs)

	// 工具→模型交叉矩阵保持不变
	if got := byToolModel["Codex"]["gpt-5.5"]; got != 100 {
		t.Fatalf("Codex/gpt-5.5 = %d, want 100", got)
	}
	if got := byToolModel["Codex"]["glm-5.2"]; got != 200 {
		t.Fatalf("Codex/glm-5.2 = %d, want 200", got)
	}
	if got := byToolModel["Claude"]["gpt-5.5"]; got != 50 {
		t.Fatalf("Claude/gpt-5.5 = %d, want 50", got)
	}

	// 每工具请求数 (v1.4.82 新增, 供首页工具行展示"调用 N 次")
	if got := byTool["Codex"].Requests; got != 2 {
		t.Fatalf("Codex.Requests = %d, want 2", got)
	}
	if got := byTool["Claude"].Requests; got != 1 {
		t.Fatalf("Claude.Requests = %d, want 1", got)
	}

	// 每模型请求数
	if got := byModelRequests["gpt-5.5"]; got != 2 {
		t.Fatalf("byModelRequests[gpt-5.5] = %d, want 2", got)
	}

	// 每模型 input/cached 累计 (供首页模型行算缓存命中率与平均上下文)
	if got := byModelInput["gpt-5.5"]; got != 120 {
		t.Fatalf("byModelInput[gpt-5.5] = %d, want 120", got)
	}
	if got := byModelCached["gpt-5.5"]; got != 40 {
		t.Fatalf("byModelCached[gpt-5.5] = %d, want 40", got)
	}
	if got := byModelInput["glm-5.2"]; got != 160 {
		t.Fatalf("byModelInput[glm-5.2] = %d, want 160", got)
	}
	if got := byModelCached["glm-5.2"]; got != 120 {
		t.Fatalf("byModelCached[glm-5.2] = %d, want 120", got)
	}

	// byModel 总 token 不受影响
	if got := byModel["gpt-5.5"]; got != 150 {
		t.Fatalf("byModel[gpt-5.5] = %d, want 150", got)
	}
}

func TestBuildUsageBreakdownsRequestCountPerTool(t *testing.T) {
	logs := []LogEntry{
		{Tool: "ZCode", Model: "glm-5.2", TotalTokens: 10, InputTokens: 8, InputCached: 4},
		{Tool: "ZCode", Model: "glm-5.2", TotalTokens: 20, InputTokens: 16, InputCached: 8},
		{Tool: "ZCode", Model: "glm-5.2", TotalTokens: 30, InputTokens: 24, InputCached: 12},
	}
	byTool, _, byModelRequests, byModelInput, byModelCached, _ := buildUsageBreakdowns(logs)

	// 同一工具 3 条请求, Requests 必须累加到 3
	if got := byTool["ZCode"].Requests; got != 3 {
		t.Fatalf("ZCode.Requests = %d, want 3", got)
	}
	if got := byModelRequests["glm-5.2"]; got != 3 {
		t.Fatalf("byModelRequests[glm-5.2] = %d, want 3", got)
	}
	// 平均上下文 = byModelInput / byModelRequests = 48 / 3 = 16
	if got := byModelInput["glm-5.2"]; got != 48 {
		t.Fatalf("byModelInput[glm-5.2] = %d, want 48", got)
	}
	if got := byModelCached["glm-5.2"]; got != 24 {
		t.Fatalf("byModelCached[glm-5.2] = %d, want 24", got)
	}
}
