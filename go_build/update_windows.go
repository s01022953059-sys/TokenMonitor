//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getlantern/systray"
)

const winReleaseEXEURLTemplate = "https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v%s/TokenMonitor.exe"

var selfUpdateMu sync.Mutex
var selfUpdateInProgress bool

func trySelfUpdate(port int) bool {
	return trySelfUpdateWithProgress(port)
}

// trySelfUpdateWithProgress 直接下载并校验新的 PE 文件，再交给独立脚本替换。
// ZIP 仍作为手动安装包发布，但不再参与应用内更新。
func trySelfUpdateWithProgress(port int) bool {
	selfUpdateMu.Lock()
	if selfUpdateInProgress {
		selfUpdateMu.Unlock()
		injectUpdateStatus("更新正在进行中", "progress")
		return false
	}
	selfUpdateInProgress = true
	selfUpdateMu.Unlock()
	defer func() {
		selfUpdateMu.Lock()
		selfUpdateInProgress = false
		selfUpdateMu.Unlock()
	}()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/check-update", port))
	if err != nil {
		return failWindowsUpdate("无法连接本地更新服务: " + err.Error())
	}
	defer resp.Body.Close()
	var data struct {
		OK              bool   `json:"ok"`
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable bool   `json:"update_available"`
		DownloadURL     string `json:"download_url"`
		Error           string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return failWindowsUpdate("更新信息解析失败: " + err.Error())
	}
	if !data.OK {
		return failWindowsUpdate("检查更新失败: " + data.Error)
	}
	if !data.UpdateAvailable {
		injectUpdateStatus("当前已是最新版本", "success")
		return false
	}

	downloadURL := strings.TrimSpace(data.DownloadURL)
	if !strings.Contains(strings.ToLower(downloadURL), ".exe") {
		downloadURL = fmt.Sprintf(winReleaseEXEURLTemplate, data.LatestVersion)
	}
	guiLog("trySelfUpdate: v%s url=%s", data.LatestVersion, downloadURL)
	injectProgress(0, "准备下载 v"+data.LatestVersion)
	systray.SetTooltip("Token Monitor - 正在下载 v" + data.LatestVersion)

	tmpExe := filepath.Join(os.TempDir(), "TokenMonitor-update-"+data.LatestVersion+".exe")
	_ = os.Remove(tmpExe)
	if _, err := downloadWithFallback(downloadURL, tmpExe); err != nil {
		_ = os.Remove(tmpExe)
		return failWindowsUpdate("下载失败: " + err.Error())
	}
	if err := validateWindowsExecutable(tmpExe); err != nil {
		_ = os.Remove(tmpExe)
		return failWindowsUpdate("下载文件校验失败: " + err.Error())
	}
	injectProgress(100, "下载完成，正在准备安装")

	currentExe, err := os.Executable()
	if err != nil {
		return failWindowsUpdate("无法读取当前程序路径: " + err.Error())
	}
	currentExe, _ = filepath.Abs(currentExe)
	stagedExe := currentExe + ".new"
	_ = os.Remove(stagedExe)
	if err := copyFile(tmpExe, stagedExe); err != nil {
		return failWindowsUpdate("无法写入安装目录: " + err.Error())
	}
	_ = os.Remove(tmpExe)
	if err := validateWindowsExecutable(stagedExe); err != nil {
		_ = os.Remove(stagedExe)
		return failWindowsUpdate("安装前校验失败: " + err.Error())
	}

	scriptPath := filepath.Join(os.TempDir(), "TokenMonitor-update-"+data.LatestVersion+".cmd")
	versionFile := filepath.Join(filepath.Dir(currentExe), "version.txt")
	script := buildWindowsUpdateScript(currentExe, stagedExe, versionFile)
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		return failWindowsUpdate("无法创建更新脚本: " + err.Error())
	}

	cmd := exec.Command("cmd.exe", "/C", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		return failWindowsUpdate("无法启动安装程序: " + err.Error())
	}
	guiLog("trySelfUpdate: helper started pid=%d", cmd.Process.Pid)
	injectProgress(100, "安装中，应用即将重启")
	systray.SetTooltip("Token Monitor - 正在安装更新")
	return true
}

func failWindowsUpdate(message string) bool {
	guiLog("windows update failed: %s", message)
	injectUpdateStatus(message, "error")
	systray.SetTooltip("Token Monitor - 更新失败")
	return false
}

// downloadWithFallback 先直连，失败后使用项目已有的代理客户端。
func downloadWithFallback(url, destPath string) (int64, error) {
	n, err := downloadWithIdleTimeout(url, destPath, false)
	if err == nil {
		return n, nil
	}
	guiLog("direct update download failed: %v; retry with proxy", err)
	injectUpdateStatus("直连失败，正在切换代理重试", "progress")
	_ = os.Remove(destPath)
	return downloadWithIdleTimeout(url, destPath, true)
}

func downloadWithIdleTimeout(url, destPath string, useProxy bool) (int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "TokenMonitor-updater")

	client := &http.Client{Timeout: 10 * time.Minute}
	if useProxy {
		client = newProxyHTTPClient(600)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	var lastProgress atomic.Int64
	lastProgress.Store(time.Now().UnixNano())
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				last := time.Unix(0, lastProgress.Load())
				if time.Since(last) > 15*time.Second {
					cancel()
					return
				}
			case <-done:
				return
			}
		}
	}()

	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 64*1024)
	lastReport := time.Time{}
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return downloaded, err
			}
			downloaded += int64(n)
			lastProgress.Store(time.Now().UnixNano())
			if time.Since(lastReport) >= 500*time.Millisecond {
				pct := 0
				if total > 0 {
					pct = int(downloaded * 100 / total)
				}
				injectProgress(pct, fmt.Sprintf("下载中 %d%% (%s / %s)", pct, formatBytes(downloaded), formatBytes(total)))
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return downloaded, fmt.Errorf("15 秒未收到数据")
			}
			return downloaded, readErr
		}
	}
	if downloaded == 0 {
		return 0, fmt.Errorf("下载内容为空")
	}
	return downloaded, nil
}

func formatBytes(b int64) string {
	if b < 0 {
		return "未知"
	}
	if b >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(b)/1024/1024)
	}
	if b >= 1024 {
		return fmt.Sprintf("%.0fKB", float64(b)/1024)
	}
	return fmt.Sprintf("%dB", b)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
