package main

import "testing"

func TestPickAssetURLForOS(t *testing.T) {
	payload := map[string]interface{}{
		"assets": []interface{}{
			map[string]interface{}{"name": "Token Monitor.dmg", "browser_download_url": "mac"},
			map[string]interface{}{"name": "TokenMonitor.exe", "browser_download_url": "legacy"},
			map[string]interface{}{"name": "TokenMonitor-Setup.exe", "browser_download_url": "setup"},
		},
	}
	if got := pickAssetURLForOS(payload, "windows"); got != "setup" {
		t.Fatalf("windows asset = %q, want setup", got)
	}
	if got := pickAssetURLForOS(payload, "darwin"); got != "mac" {
		t.Fatalf("darwin asset = %q, want mac", got)
	}
}

func TestPickAssetURLForOSRejectsLegacyWindowsAssets(t *testing.T) {
	payload := map[string]interface{}{
		"assets": []interface{}{
			map[string]interface{}{"name": "Token Monitor.dmg", "browser_download_url": "mac"},
			map[string]interface{}{"name": "TokenMonitor.exe", "browser_download_url": "legacy"},
		},
	}
	if got := pickAssetURLForOS(payload, "windows"); got != "" {
		t.Fatalf("legacy windows asset = %q, want empty", got)
	}
}

func TestNormalizeReleaseDownloadURL(t *testing.T) {
	got := normalizeReleaseDownloadURL("https://api.gitcode.com/acme/app/releases/download/v1.2.3/TokenMonitor.exe")
	want := "https://gitcode.com/acme/app/releases/download/v1.2.3/TokenMonitor.exe"
	if got != want {
		t.Fatalf("normalized URL = %q, want %q", got, want)
	}
	apiURL := "https://api.gitcode.com/api/v5/repos/acme/app/releases/latest"
	if got := normalizeReleaseDownloadURL(apiURL); got != apiURL {
		t.Fatalf("feed API URL changed to %q", got)
	}
}
