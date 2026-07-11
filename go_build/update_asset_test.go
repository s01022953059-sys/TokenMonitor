package main

import "testing"

func TestPickAssetURLForOS(t *testing.T) {
	payload := map[string]interface{}{
		"assets": []interface{}{
			map[string]interface{}{"name": "Token Monitor.dmg", "browser_download_url": "mac"},
			map[string]interface{}{"name": "TokenMonitor-win.zip", "browser_download_url": "zip"},
			map[string]interface{}{"name": "TokenMonitor.exe", "browser_download_url": "exe"},
		},
	}
	if got := pickAssetURLForOS(payload, "windows"); got != "exe" {
		t.Fatalf("windows asset = %q, want exe", got)
	}
	if got := pickAssetURLForOS(payload, "darwin"); got != "mac" {
		t.Fatalf("darwin asset = %q, want mac", got)
	}
}

func TestPickAssetURLForOSFallsBackToLegacyWindowsZip(t *testing.T) {
	payload := map[string]interface{}{
		"assets": []interface{}{
			map[string]interface{}{"name": "Token Monitor.dmg", "browser_download_url": "mac"},
			map[string]interface{}{"name": "TokenMonitor-win.zip", "browser_download_url": "zip"},
		},
	}
	if got := pickAssetURLForOS(payload, "windows"); got != "zip" {
		t.Fatalf("legacy windows asset = %q, want zip", got)
	}
}
