package main

import "testing"

func TestBuildUsageBreakdownsIncludesToolModelMatrix(t *testing.T) {
	logs := []LogEntry{
		{Tool: "Codex", Model: "gpt-5.5", TotalTokens: 100},
		{Tool: "Codex", Model: "glm-5.2", TotalTokens: 200},
		{Tool: "Claude", Model: "gpt-5.5", TotalTokens: 50},
	}

	_, _, _, byToolModel := buildUsageBreakdowns(logs)
	if got := byToolModel["Codex"]["gpt-5.5"]; got != 100 {
		t.Fatalf("Codex/gpt-5.5 = %d, want 100", got)
	}
	if got := byToolModel["Codex"]["glm-5.2"]; got != 200 {
		t.Fatalf("Codex/glm-5.2 = %d, want 200", got)
	}
	if got := byToolModel["Claude"]["gpt-5.5"]; got != 50 {
		t.Fatalf("Claude/gpt-5.5 = %d, want 50", got)
	}
}
