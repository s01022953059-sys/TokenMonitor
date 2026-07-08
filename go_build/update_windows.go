//go:build windows

// Token Monitor Windows 自更新引擎
// v1.4.02: 测速选直连/代理 + 下载进度显示
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

// trySelfUpdate 原始版本 (不显示进度, 给后台定时调用)
func trySelfUpdate(port int) bool {
	return trySelfUpdateWithProgress(port)
}

// trySelfUpdateWithProgress 检查更新 + 测速 + 下载(进度) + 替换 + 重启
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
	guiLog("trySelfUpdate: v%s, 开始测速", data.LatestVersion)

	// 1. 测速: 直连 vs 代理, 选快的
	zipURL := fmt.Sprintf(winReleaseURLTemplate, data.LatestVersion)
	client := pickFastClient(zipURL)

	// 2. 下载 (带进度)
	tmpZip := filepath.Join(os.TempDir(), "tm_update.zip")
	guiLog("trySelfUpdate: 下载 %s", zipURL)
	injectToast("正在下载更新包...", "#f59e0b")

	downloadResp, err := client.Get(zipURL)
	if err != nil {
		guiLog("trySelfUpdate: 下载失败: %v", err)
		return false
	}
	defer downloadResp.Body.Close()
	if downloadResp.StatusCode != 200 {
		guiLog("trySelfUpdate: 下载 HTTP %d", downloadResp.StatusCode)
		return false
	}

	totalSize := downloadResp.ContentLength
	out, err := os.Create(tmpZip)
	if err != nil {
		return false
	}

	// 进度 reader
	lastReport := time.Now()
	var downloaded int64
	buf := make([]byte, 32*1024)
	for {
		n, err := downloadResp.Body.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			downloaded += int64(n)
			// 每 2 秒报告一次进度
			if time.Since(lastReport) > 2*time.Second {
				pct := 0
				if totalSize > 0 {
					pct = int(downloaded * 100 / totalSize)
				}
				injectToast(fmt.Sprintf("下载中... %d%% (%s / %s)",
					pct, formatBytes(downloaded), formatBytes(totalSize)), "#f59e0b")
				systray.SetTooltip(fmt.Sprintf("Token Monitor - 下载更新 %d%%", pct))
				lastReport = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			out.Close()
			guiLog("trySelfUpdate: 下载读取失败: %v", err)
			return false
		}
	}
	out.Close()
	guiLog("trySelfUpdate: 下载完成 %d bytes", downloaded)

	// 3. 解压
	injectToast("正在解压...", "#f59e0b")
	systray.SetTooltip("Token Monitor - 解压中...")
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

	// 4. 替换 exe
	injectToast("正在安装...", "#f59e0b")
	systray.SetTooltip("Token Monitor - 安装中...")
	currentExe, _ := os.Executable()
	currentDir := filepath.Dir(currentExe)
	newExePath := filepath.Join(currentDir, "TokenMonitor.exe.new")
	if err := copyFile(newExe, newExePath); err != nil {
		guiLog("trySelfUpdate: 拷新 exe 失败: %v", err)
		return false
	}

	// 5. 写 update.bat + 启动
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

// pickFastClient 测速: 直连 vs 代理, 选快的
// GitCode CDN 直连可能被墙, 代理可能慢, 取 3 秒内能连上的
func pickFastClient(url string) *http.Client {
	// 先试直连 (3 秒超时)
	guiLog("pickFastClient: 测速直连...")
	directClient := &http.Client{Timeout: 3 * time.Second}
	start := time.Now()
	resp, err := directClient.Get(url)
	if err == nil && resp.StatusCode == 200 {
		directLatency := time.Since(start)
		resp.Body.Close()
		guiLog("pickFastClient: 直连 OK (%v), 用直连", directLatency)
		return &http.Client{Timeout: 5 * time.Minute}
	}
	if resp != nil {
		resp.Body.Close()
	}
	guiLog("pickFastClient: 直连失败 (%v), 用代理", err)

	// 直连失败, 用代理
	return newProxyHTTPClient(300)
}

// formatBytes 格式化字节数
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
