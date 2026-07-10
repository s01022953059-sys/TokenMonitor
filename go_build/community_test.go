package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCommunityInt64(t *testing.T) {
	cases := []struct {
		name  string
		value interface{}
		want  int64
	}{
		{name: "int64", value: int64(42_000_000), want: 42_000_000},
		{name: "int", value: 123, want: 123},
		{name: "float64", value: float64(456), want: 456},
		{name: "json number", value: json.Number("789"), want: 789},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := communityInt64(tc.value); got != tc.want {
				t.Fatalf("communityInt64(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

func TestCommunityWriteMethod(t *testing.T) {
	if got := communityWriteMethod(""); got != http.MethodPost {
		t.Fatalf("new report method = %s, want POST", got)
	}
	if got := communityWriteMethod("existing-sha"); got != http.MethodPut {
		t.Fatalf("existing report method = %s, want PUT", got)
	}
}
