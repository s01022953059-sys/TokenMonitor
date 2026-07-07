//go:build windows

// Token Monitor Windows 自更新引擎
// v1.3.95: 静默下载新版本 zip → 解压 → 替换 exe → 重启
// 流程:
//   1. 定时检查 /api/check-update, 有新版 → 后台下 TokenMonitor-win.zip
//   2. 解压到 %TEMP%/tm_update/, 拿到新 TokenMonitor.exe
//   3. 拷新 exe 为 TokenMonitor.exe.new (同目录)
//   4. 写 update.bat: 等旧 exe 退出 → ren 旧→.old → ren .new→exe → start 新 → del .old
//   5. 启动 update.bat (detached), 当前进程退出
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
)

const winReleaseURLTemplate = "https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v%s/TokenMonitor-win.zip"

// selfUpdateState 防止重复触发
var selfUpdateInProgress bool

// trySelfUpdate 检查更新, 有新版静默下载 + 替换 + 重启
// 返回 true 如果触发了更新 (调用方应退出进程)
func trySelfUpdate(port int) bool {
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
	fmt.Printf("[update] 检测到新版本 v%s, 开始静默下载...\n", data.LatestVersion)

	// 1. 下载 zip
	zipURL := fmt.Sprintf(winReleaseURLTemplate, data.LatestVersion)
	tmpZip := filepath.Join(os.TempDir(), "tm_update.zip")
	if err := downloadFile(zipURL, tmpZip); err != nil {
		fmt.Printf("[update] 下载失败: %v\n", err)
		selfUpdateInProgress = false
		return false
	}
	fmt.Printf("[update] 下载完成, 解压中...\n")

	// 2. 解压, 找 TokenMonitor.exe
	tmpDir := filepath.Join(os.TempDir(), "tm_update")
	os.RemoveAll(tmpDir)
	if err := unzipTo(tmpZip, tmpDir); err != nil {
		fmt.Printf("[update] 解压失败: %v\n", err)
		selfUpdateInProgress = false
		return false
	}
	newExe := findFile(tmpDir, "TokenMonitor.exe")
	if newExe == "" {
		fmt.Printf("[update] 解压后找不到 TokenMonitor.exe\n")
		selfUpdateInProgress = false
		return false
	}

	// 3. 拷新 exe 到当前 exe 旁边, 命名 .new
	currentExe, _ := os.Executable()
	currentDir := filepath.Dir(currentExe)
	newExePath := filepath.Join(currentDir, "TokenMonitor.exe.new")
	if err := copyFile(newExe, newExePath); err != nil {
		fmt.Printf("[update] 拷新 exe 失败: %v\n", err)
		selfUpdateInProgress = false
		return false
	}

	// 4. 写 update.bat
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
		fmt.Printf("[update] 写 update.bat 失败: %v\n", err)
		selfUpdateInProgress = false
		return false
	}

	// 5. 启动 update.bat (detached), 当前进程退出
	fmt.Printf("[update] 启动更新脚本, 进程即将退出...\n")
	cmd := exec.Command("cmd", "/c", "start", "/min", batPath)
	cmd.Start()

	return true
}

// downloadFile 下载 URL 到本地文件
func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// unzipTo 解压 zip 到目录
func unzipTo(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		path := filepath.Join(destDir, f.Name)
		if strings.Contains(f.Name, "..") {
			continue // 防 zip slip
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

// findFile 在目录里递归找指定文件名
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

// copyFile 拷贝文件
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
