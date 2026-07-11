package main

import "testing"

func TestBuildAutoStartCommand(t *testing.T) {
	exe := `C:\Program Files\Token Monitor\TokenMonitor.exe`
	want := `"C:\Program Files\Token Monitor\TokenMonitor.exe" --autostart`
	if got := buildAutoStartCommand(exe); got != want {
		t.Fatalf("buildAutoStartCommand() = %q, want %q", got, want)
	}
}

func TestAutoStartCommandMatching(t *testing.T) {
	exe := `C:\Tools\TokenMonitor.exe`
	if !isExpectedAutoStartCommand(`"C:\Tools\TokenMonitor.exe" --autostart`, exe) {
		t.Fatal("expected canonical command to match")
	}
	if !isLegacyAutoStartCommand(`"C:\Tools\TokenMonitor.exe"`, exe) {
		t.Fatal("expected legacy command to match")
	}
	if isExpectedAutoStartCommand(`"C:\Old\TokenMonitor.exe" --autostart`, exe) {
		t.Fatal("stale executable path must not be treated as enabled")
	}
}
