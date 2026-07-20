package main

import (
	"sync"
	"testing"
	"time"
)

func TestCommunityReportLoopDoesNotDependOnCommunityPage(t *testing.T) {
	stop := make(chan struct{})
	usage := UsageResponse{Summary: map[string]interface{}{"total_tokens": int64(123)}}
	var mu sync.Mutex
	var calls int
	done := make(chan struct{})

	go runCommunityReportLoop(stop, 0, time.Hour,
		func() UsageResponse { return usage },
		func(received *UsageResponse) CommunityReportResult {
			mu.Lock()
			calls++
			if received.Summary["total_tokens"] != int64(123) {
				t.Errorf("unexpected usage snapshot: %+v", received)
			}
			mu.Unlock()
			close(done)
			return CommunityReportResult{OK: true}
		})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("community report did not run without a page request")
	}
	close(stop)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected one report, got %d", calls)
	}
}
