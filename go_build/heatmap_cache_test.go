package main

import "testing"

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
