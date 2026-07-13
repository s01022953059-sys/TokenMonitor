package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSliceHeatmapUsesRequestedRange(t *testing.T) {
	days := make([]HeatmapDay, heatmapCacheDays)
	for index := range days {
		days[index] = HeatmapDay{Date: "day", Tokens: int64(index)}
	}
	data := HeatmapResponse{Days: days, MaxValue: heatmapCacheDays - 1}

	annual := sliceHeatmap(data, 365)
	monthly := sliceHeatmap(data, 30)
	if len(annual.Days) != 365 {
		t.Fatalf("annual range = %d, want 365", len(annual.Days))
	}
	if len(monthly.Days) != 30 {
		t.Fatalf("monthly range = %d, want 30", len(monthly.Days))
	}
	if monthly.MaxValue != 364 {
		t.Fatalf("monthly max = %d, want 364", monthly.MaxValue)
	}
}

func TestPaginatedSessionListKeepsOneEmptyPage(t *testing.T) {
	empty := paginatedSessionList([]SessionEntry{}, nil, 1, 20)
	if empty.Page != 1 || empty.PageSize != 20 || empty.TotalPages != 1 || len(empty.Sessions) != 0 {
		t.Fatalf("unexpected empty page: %+v", empty)
	}
	rows := []SessionEntry{{Time: "1"}, {Time: "2"}}
	paged := paginatedSessionList(rows, nil, 2, 1)
	if paged.Total != 2 || paged.TotalPages != 2 || len(paged.Sessions) != 1 || paged.Sessions[0].Time != "2" {
		t.Fatalf("unexpected paged result: %+v", paged)
	}
}

func TestEmptyHeatmapReturnsTheRequestedDateGridImmediately(t *testing.T) {
	heatmap := emptyHeatmap(365)
	if heatmap.CacheState != "warming" || len(heatmap.Days) != 365 {
		t.Fatalf("unexpected cold heatmap: %+v", heatmap)
	}
	if heatmap.StartDate != heatmap.Days[0].Date || heatmap.EndDate != heatmap.Days[364].Date {
		t.Fatalf("date range does not match rows: %+v", heatmap)
	}
}

func TestColdCacheReturnsBeforeBackgroundScanFinishes(t *testing.T) {
	oldPath := heatmapCachePathOverride
	oldBuilder := heatmapSnapshotBuilder
	heatmapCachePathOverride = filepath.Join(t.TempDir(), "heatmap.json")
	heatmapCacheMu.Lock()
	heatmapRefreshRunning = false
	heatmapCacheMu.Unlock()
	heatmapSnapshotBuilder = func(days int) HeatmapResponse {
		time.Sleep(200 * time.Millisecond)
		return emptyHeatmap(days)
	}
	t.Cleanup(func() {
		heatmapCachePathOverride = oldPath
		heatmapSnapshotBuilder = oldBuilder
		heatmapCacheMu.Lock()
		heatmapRefreshRunning = false
		heatmapCacheMu.Unlock()
	})

	started := time.Now()
	result := getCachedHeatmap(365)
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("cold cache blocked foreground for %s", elapsed)
	}
	if result.CacheState != "warming" || len(result.Days) != 365 {
		t.Fatalf("unexpected foreground response: %+v", result)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		heatmapCacheMu.Lock()
		running := heatmapRefreshRunning
		heatmapCacheMu.Unlock()
		if !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ready := getCachedHeatmap(30)
	if ready.CacheState != "ready" || len(ready.Days) != 30 {
		t.Fatalf("background snapshot was not ready: %+v", ready)
	}
}
