package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeDisplayName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		canonical string
		wantError bool
	}{
		{name: "Chinese", input: " 鹏帅_88 ", want: "鹏帅_88", canonical: "鹏帅_88"},
		{name: "NFKC", input: "Ｐｅｎｇ８８", want: "Peng88", canonical: "peng88"},
		{name: "pure digits", input: "12345", wantError: true},
		{name: "too short", input: "鹏", wantError: true},
		{name: "emoji", input: "鹏帅🔥", wantError: true},
		{name: "zero width", input: "鹏\u200b帅", wantError: true},
		{name: "HTML", input: "<b>鹏帅</b>", wantError: true},
		{name: "repeat", input: "aaaa鹏", wantError: true},
		{name: "user prefix", input: "User_TEST", wantError: true},
		{name: "protected skeleton", input: "Adm1n", wantError: true},
		{name: "protected underscores", input: "Token_M0nitor", wantError: true},
		{name: "contact", input: "微信abc", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, canonical, err := normalizeDisplayName(test.input, "")
			if test.wantError {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil || got != test.want || canonical != test.canonical {
				t.Fatalf("got=(%q,%q,%v), want=(%q,%q,nil)", got, canonical, err, test.want, test.canonical)
			}
		})
	}
}

func TestNormalizeDisplayNameUsesServerBlocklist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.txt")
	if err := os.WriteFile(path, []byte("风险词\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := normalizeDisplayName("测试风险词", path); err == nil {
		t.Fatal("blocked name was accepted")
	}
}

func testProfileDatabase(t *testing.T) *profileDatabase {
	t.Helper()
	database, err := openProfileDatabase(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestProfileUniquenessRateLimitAndReservation(t *testing.T) {
	database := testProfileDatabase(t)
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	if _, _, err := database.updateName(context.Background(), "User_FIRST1", "鹏帅", "鹏帅", now, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.updateName(context.Background(), "User_SECOND", "鹏帅", "鹏帅", now, func() error { return nil }); profileErrorCode(err) != "name_taken" {
		t.Fatalf("expected name_taken, got %v", err)
	}
	if _, _, err := database.updateName(context.Background(), "User_FIRST1", "鹏哥", "鹏哥", now.Add(time.Hour), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.updateName(context.Background(), "User_FIRST1", "鹏总", "鹏总", now.Add(2*time.Hour), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.updateName(context.Background(), "User_FIRST1", "鹏王", "鹏王", now.Add(3*time.Hour), func() error { return nil }); profileErrorCode(err) != "rename_rate_limited" {
		t.Fatalf("expected rename_rate_limited, got %v", err)
	}
	if _, _, err := database.updateName(context.Background(), "User_FIRST1", "鹏王", "鹏王", now.Add(25*time.Hour), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.updateName(context.Background(), "User_SECOND", "鹏帅", "鹏帅", now.Add(26*time.Hour), func() error { return nil }); profileErrorCode(err) != "name_reserved" {
		t.Fatalf("expected name_reserved, got %v", err)
	}
}

func TestProfileUploadFailureRollsBackReservation(t *testing.T) {
	database := testProfileDatabase(t)
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	_, _, err := database.updateName(context.Background(), "User_FIRST1", "鹏帅", "鹏帅", now, func() error { return errors.New("gitcode down") })
	if profileErrorCode(err) != "upload_failed" {
		t.Fatalf("expected upload_failed, got %v", err)
	}
	if _, _, err := database.updateName(context.Background(), "User_SECOND", "鹏帅", "鹏帅", now, func() error { return nil }); err != nil {
		t.Fatalf("rolled back name remained occupied: %v", err)
	}
}

func TestConcurrentNameClaimAllowsOneWinner(t *testing.T) {
	database := testProfileDatabase(t)
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, userID := range []string{"User_FIRST1", "User_SECOND"} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			<-start
			_, _, err := database.updateName(context.Background(), id, "并发昵称", "并发昵称", now, func() error { return nil })
			results <- err
		}(userID)
	}
	close(start)
	wait.Wait()
	close(results)
	successes := 0
	conflicts := 0
	for err := range results {
		if err == nil {
			successes++
		} else if profileErrorCode(err) == "name_taken" {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func profileErrorCode(err error) string {
	var typed *profileError
	if errors.As(err, &typed) {
		return typed.code
	}
	return ""
}

func performProfile(t *testing.T, handler *relayHandler, request profileRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(request)
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/profile", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, httpRequest)
	return recorder
}

func TestProfileEndpointAuthenticatesAndWritesDisplayName(t *testing.T) {
	secret := bytes.Repeat([]byte{9}, 32)
	hash := sha256.Sum256(secret)
	store := &fakeStore{existing: &reportDocument{ID: "User_TEST1", AuthHash: hex.EncodeToString(hash[:])}, sha: "sha"}
	handler := &relayHandler{store: store, profiles: testProfileDatabase(t), now: func() time.Time {
		return time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	}}
	response := performProfile(t, handler, profileRequest{
		ID: "User_TEST1", DeviceSecret: base64.RawURLEncoding.EncodeToString(secret), DisplayName: "鹏帅",
	})
	if response.Code != http.StatusOK || store.written == nil || store.written.DisplayName != "鹏帅" {
		t.Fatalf("status=%d written=%#v body=%s", response.Code, store.written, response.Body.String())
	}
}

func TestProfileEndpointRejectsWrongCredential(t *testing.T) {
	store := &fakeStore{existing: &reportDocument{ID: "User_TEST1", AuthHash: strings.Repeat("a", 64)}}
	handler := &relayHandler{store: store, profiles: testProfileDatabase(t), now: time.Now}
	response := performProfile(t, handler, profileRequest{
		ID: "User_TEST1", DeviceSecret: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), DisplayName: "鹏帅",
	})
	if response.Code != http.StatusForbidden || store.written != nil {
		t.Fatalf("status=%d written=%#v", response.Code, store.written)
	}
}
