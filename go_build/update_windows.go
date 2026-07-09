//go:build windows

// Token Monitor Windows 自更新引擎
// v1.4.09: HEAD 测连通 + 下载卡死检测 + 自动切代理
package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/getlantern/systray"
)

const winReleaseURLTemplate = "https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v%s/TokenMonitor-win.zip"

var selfUpdateInProgress bool

func trySelfUpdate(port int) bool {
	return trySelfUpdateWithProgress(port)
}

// trySelfUpdateWithProgress 检查更新 + 下载(卡死检测+自动切代理) + 替换 + 重启
func trySelfUpdateWithProgress(port int) bool {
	if selfUpdateInProgress {
		return false
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/check-update", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var data struct {
		OK              bool   `json:"ok"`
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable bool   `json:"update_available"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return false
	}
	if !data.OK || !data.UpdateAvailable {
		return false
	}

	selfUpdateInProgress = true
	defer func() { selfUpdateInProgress = false }()
	guiLog("trySelfUpdate: v%s", data.LatestVersion)

	zipURL := fmt.Sprintf(winReleaseURLTemplate, data.LatestVersion)
	tmpZip := filepath.Join(os.TempDir(), "tm_update.zip")

	// 下载: 先试直连, 卡死自动切代理
	injectToast("正在下载更新包...", "#f59e0b")
	systray.SetTooltip("Token Monitor - 正在下载更新...")

	totalSize, err := downloadWithFallback(zipURL, tmpZip, data.LatestVersion)
	if err != nil {
		guiLog("trySelfUpdate: 下载最终失败: %v", err)
		injectToast("下载失败: "+err.Error(), "#f85149")
		systray.SetTooltip("Token Monitor - 下载失败")
		return false
	}
	guiLog("trySelfUpdate: 下载完成 %d bytes", totalSize)
	injectToast("下载完成, 正在解压...", "#f59e0b")
	systray.SetTooltip("Token Monitor - 解压中...")

	// 解压
	tmpDir := filepath.Join(os.TempDir(), "tm_update")
	os.RemoveAll(tmpDir)
	if err := unzipTo(tmpZip, tmpDir); err != nil {
		guiLog("trySelfUpdate: 解压失败: %v", err)
		return false
	}
	newExe := findFile(tmpDir, "TokenMonitor.exe")
	if newExe == "" {
		guiLog("trySelfUpdate: 解压后找不到 TokenMonitor.exe")
		return false
	}

	// 替换 exe
	injectToast("正在安装...", "#f59e0b")
	systray.SetTooltip("Token Monitor - 安装中...")
	currentExe, _ := os.Executable()
	currentDir := filepath.Dir(currentExe)
	newExePath := filepath.Join(currentDir, "TokenMonitor.exe.new")
	if err := copyFile(newExe, newExePath); err != nil {
		guiLog("trySelfUpdate: 拷新 exe 失败: %v", err)
		return false
	}

	// update.bat
	batPath := filepath.Join(currentDir, "update.bat")
	batContent := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak >nul
del "%s.old" 2>nul
ren "%s" "TokenMonitor.exe.old"
ren "%s" "TokenMonitor.exe"
start "" "%s"
del "%s.old" 2>nul
del "%s" 2>nul
`, currentExe, currentExe, newExePath, currentExe, currentExe, batPath)
	if err := os.WriteFile(batPath, []byte(batContent), 0644); err != nil {
		return false
	}

	guiLog("trySelfUpdate: 启动 update.bat, 退出")
	cmd := exec.Command("cmd", "/c", "start", "/min", batPath)
	cmd.Start()
	return true
}

// downloadWithFallback 先试直连, 10 秒无数据自动切代理
func downloadWithFallback(url, destPath, version string) (int64, error) {
	// 1. 先试直连 (HEAD 测连通, 不下载 body)
	guiLog("downloadWithFallback: 测速直连 HEAD...")
	directOK := testConnectivity(url, false)
	if directOK {
		guiLog("downloadWithFallback: 直连 HEAD OK, 尝试直连下载")
		n, err := downloadWithStallDetection(url, destPath, false, version)
		if err == nil {
			return n, nil
		}
		guiLog("downloadWithFallback: 直连下载失败/卡死: %v, 切代理", err)
		injectToast("直连下载失败, 切换到代理...", "#f59e0b")
	} else {
		guiLog("downloadWithFallback: 直连 HEAD 失败, 直接用代理")
	}

	// 2. 代理下载
	n, err := downloadWithStallDetection(url, destPath, true, version)
	if err != nil {
		return 0, fmt.Errorf("代理下载也失败: %w", err)
	}
	return n, nil
}

// testConnectivity HEAD 请求测连通性 (不下载 body)
func testConnectivity(url string, useProxy bool) bool {
	var client *http.Client
	if useProxy {
		client = newProxyHTTPClient(5)
	} else {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "TokenMonitor-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// downloadWithStallDetection 下载文件, 10 秒无新数据则判定卡死
func downloadWithStallDetection(url, destPath string, useProxy bool, version string) (int64, error) {
	var client *http.Client
	if useProxy {
		client = newProxyHTTPClient(300)
		guiLog("downloadWithStallDetection: 用代理下载")
	} else {
		client = &http.Client{Timeout: 5 * time.Minute}
		guiLog("downloadWithStallDetection: 用直连下载")
	}

	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	totalSize := resp.ContentLength
	out, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}

	var downloaded int64
	buf := make([]byte, 32*1024)
	lastDataTime := time.Now()
	lastReport := time.Now()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			downloaded += int64(n)
			lastDataTime = time.Now()

			// 每 2 秒报告进度
			if time.Since(lastReport) > 2*time.Second {
				pct := 0
				if totalSize > 0 {
					pct = int(downloaded * 100 / totalSize)
				}
				progressText := fmt.Sprintf("下载中... %d%% (%s / %s)",
					pct, formatBytes(downloaded), formatBytes(totalSize))
				injectToast(progressText, "#f59e0b")
				systray.SetTooltip(fmt.Sprintf("Token Monitor - 下载更新 %d%%", pct))
				// v1.4.10: 同时更新 About 页面进度条
				injectProgress(pct, progressText)
				lastReport = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			out.Close()
			return 0, err
		}
		// 卡死检测: 10 秒无新数据 → 中断
		if time.Since(lastDataTime) > 10*time.Second {
			out.Close()
			guiLog("downloadWithStallDetection: 10 秒无数据, 判定卡死 (downloaded=%d)", downloaded)
			return 0, fmt.Errorf("下载卡死 (10s 无数据, 已下载 %s)", formatBytes(downloaded))
		}
	}
	out.Close()
	guiLog("downloadWithStallDetection: 下载完成 %d bytes", downloaded)
	return downloaded, nil
}

func formatBytes(b int64) string {
	if b >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(b)/1024/1024)
	}
	if b >= 1024 {
		return fmt.Sprintf("%.0fKB", float64(b)/1024)
	}
	return fmt.Sprintf("%dB", b)
}

func unzipTo(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		path := filepath.Join(destDir, f.Name)
		if strings.Contains(f.Name, "..") {
			continue
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(path, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(path), 0755)
		out, err := os.Create(path)
		if err != nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			continue
		}
		io.Copy(out, rc)
		rc.Close()
		out.Close()
	}
	return nil
}

func findFile(dir, name string) string {
	var found string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == name {
			found = path
		}
		return nil
	})
	return found
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
