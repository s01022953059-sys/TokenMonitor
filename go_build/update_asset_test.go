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

// v1.5.14 更新失败事故: GitCode releases/latest 把源码归档 (type=source,
// archive/refs/heads/<tag>.zip) 列在 assets 前部, 该地址对 tag 发布必然
// 404/占位页。选择器必须跳过 source 归档, 选到真实 DMG 附件。
func TestPickAssetURLForOSSkipsSourceArchives(t *testing.T) {
	payload := map[string]interface{}{
		"tag_name": "v1.5.14",
		"assets": []interface{}{
			map[string]interface{}{
				"name":                 "v1.5.14.zip",
				"type":                 "source",
				"browser_download_url": "https://raw.gitcode.com/acme/TokenMonitor/archive/refs/heads/v1.5.14.zip",
			},
			map[string]interface{}{
				"name":                 "v1.5.14.tar.gz",
				"type":                 "source",
				"browser_download_url": "https://raw.gitcode.com/acme/TokenMonitor/archive/refs/heads/v1.5.14.tar.gz",
			},
			map[string]interface{}{
				"name":                 "Token Monitor.dmg",
				"type":                 "attach",
				"browser_download_url": "mac-dmg",
			},
		},
	}
	if got := pickAssetURLForOS(payload, "darwin"); got != "mac-dmg" {
		t.Fatalf("darwin asset = %q, want mac-dmg (source archives must be skipped)", got)
	}
	if got := pickAssetURLForOS(payload, "windows"); got != "" {
		t.Fatalf("windows asset = %q, want empty (no setup exe attachment)", got)
	}
}

// 全部候选都是 source 归档时必须返回空 (走 html_url 兜底), 不能装作有安装包。
func TestPickAssetURLForOSReturnsEmptyWhenOnlySourceArchives(t *testing.T) {
	payload := map[string]interface{}{
		"assets": []interface{}{
			map[string]interface{}{
				"name":                 "v1.5.14.zip",
				"type":                 "source",
				"browser_download_url": "https://raw.gitcode.com/acme/TokenMonitor/archive/refs/heads/v1.5.14.zip",
			},
		},
	}
	if got := pickAssetURLForOS(payload, "darwin"); got != "" {
		t.Fatalf("darwin asset = %q, want empty", got)
	}
}

// GitHub 风格 assets 没有 type 字段, 不能被误伤。
func TestPickAssetURLForOSAcceptsAssetsWithoutType(t *testing.T) {
	payload := map[string]interface{}{
		"assets": []interface{}{
			map[string]interface{}{"name": "app.zip", "browser_download_url": "zip-url"},
			map[string]interface{}{"name": "Token Monitor.dmg", "browser_download_url": "dmg-url"},
		},
	}
	if got := pickAssetURLForOS(payload, "darwin"); got != "dmg-url" {
		t.Fatalf("darwin asset = %q, want dmg-url", got)
	}
	if got := pickAssetURLForOS(payload, "windows"); got != "" {
		t.Fatalf("windows asset = %q, want empty (zip is not a windows installer)", got)
	}
}
