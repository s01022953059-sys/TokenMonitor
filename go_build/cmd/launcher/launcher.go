//go:build windows

// Token Monitor Win Launcher
// 启动主服务进程 + 用 WebView2 内嵌 UI (不调起系统默认浏览器, 避免污染用户浏览器标签页)
// 额外起一个小"版本检查"窗口, 启动时自动查 /api/check-update, 有新版本提示用户去下载
//
// 流程:
//   1. 探活 127.0.0.1:15723 端口, 已占用说明主程序在跑, 直接建 webview 接上去
//   2. 端口空 → exec 启动 TokenMonitor.exe --no-browser
//   3. 轮询端口就绪 (最多 8 秒) → 起主 webview 导航到仪表盘
//   4. 并行起小"检查更新" webview, 启动时调 /api/check-update
//      * 当前最新: 显示版本号 + "已是最新"
//      * 有新版本: 显示版本号 + "立即去下载" 按钮 (打开 GitCode release 页)
//   5. 用户关任一 webview → 不杀主进程, 服务后台继续
//   6. 双击 launcher 再次 → 端口复用, 重新起 webview
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	webview "github.com/jchv/go-webview2"
)

const (
	defaultPort    = 15723
	launcherTitle  = "Token Monitor"
	updateTitle    = "Token Monitor - 检查更新"
	releaseBaseURL = "https://gitcode.com/baggiopeng/TokenMonitor/releases/tag/"
)

type checkResult struct {
	ok             bool
	currentVersion string
	latestVersion  string
	updateAvail    bool
	downloadURL    string
	html           string // 准备好的展示 HTML
}

// main 启动 webview 窗口, 拉主服务, 拉 check-update
func main() {
	port := defaultPort
	// 解析可选 --port 参数
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--port" && i+1 < len(os.Args) {
			fmt.Sscanf(os.Args[i+1], "%d", &port)
		}
	}

	addr := fmt.Sprintf("127.0.0.1:" + fmt.Sprintf("%d", port))
	if !isPortOpen(addr) {
		// 主程序没在跑, 启动它
		exe, err := findServerExe()
		if err != nil {
			showErrorAndExit("找不到主程序: " + err.Error())
			return
		}
		fmt.Printf("[launcher] 启动主服务: %s\n", exe)
		cmd := exec.Command(exe, "--no-browser", "--port", fmt.Sprintf("%d", port))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			showErrorAndExit("启动主程序失败: " + err.Error())
			return
		}
		// detach: 不 Wait, 让主进程独立跑
		go func() { _ = cmd.Wait() }()

		// 等端口就绪 (最多 8 秒)
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if isPortOpen(addr) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !isPortOpen(addr) {
			showErrorAndExit("主程序 8 秒内未就绪, 请查看控制台输出")
			return
		}
	} else {
		fmt.Printf("[launcher] 检测到主程序已在运行, 直接打开 UI\n")
	}

	// 并行起"检查更新"小 webview (用户可关掉, 不阻塞主 webview)
	go runUpdateWindow(addr)

	// 起主 webview
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(launcherTitle)
	w.SetSize(1280, 800, webview.HintFixed)
	w.Navigate("http://" + addr)
	w.Run()
	// w.Run() 返回说明用户关了 webview 窗口
	// 不杀主进程 — 留后台, 下次双击 launcher 直接 webview 重连
}

// runUpdateWindow 起"检查更新"小 webview, 拉 /api/check-update 渲染结果
func runUpdateWindow(addr string) {
	defer func() {
		// webview2 在子线程 panic 不能炸 launcher 主流程
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[launcher] 更新窗口 panic: %v\n", r)
		}
	}()

	// 后台调 check-update, 拿到结果再渲染
	resultCh := make(chan checkResult, 1)
	go func() {
		resultCh <- fetchCheckUpdate(addr)
	}()

	// 先起空 webview (loading), 等结果
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(updateTitle)
	w.SetSize(440, 220, webview.HintFixed)
	w.SetHtml(`<html><body style="font-family:sans-serif;padding:24px;color:#333;">
		<h3 style="margin:0 0 8px;">Token Monitor 检查更新</h3>
		<p style="color:#888;">正在连接主服务...</p>
	</body></html>`)

	// Bind "openRelease" 函数: 从 JS 调用 Go 打开 GitCode release 页
	w.Bind("openRelease", func() {
		// 等结果后, 从 result 取 url 打开
		// 用 channel 阻塞等结果
		var url string
		select {
		case r := <-resultCh:
			url = r.downloadURL
		case <-time.After(3 * time.Second):
			url = releaseBaseURL // 兜底
		}
		openInBrowser(url)
	})

	// 等 check-update 完成后, 重新 SetHtml 渲染结果
	go func() {
		result := <-resultCh
		w.Dispatch(func() {
			w.SetHtml(result.html)
		})
	}()

	w.Run()
}

// fetchCheckUpdate 调主服务的 /api/check-update, 准备展示 HTML
func fetchCheckUpdate(addr string) checkResult {
	url := "http://" + addr + "/api/check-update"
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return checkResult{ok: false, html: errorHTML("无法连接主服务: " + err.Error())}
	}
	defer resp.Body.Close()

	var data struct {
		OK              bool   `json:"ok"`
		CurrentVersion  string `json:"current_version"`
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable bool   `json:"update_available"`
		DownloadURL     string `json:"download_url"`
		Title           string `json:"title"`
		Error           string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return checkResult{ok: false, html: errorHTML("解析响应失败: " + err.Error())}
	}

	result := checkResult{
		ok:             data.OK,
		currentVersion: data.CurrentVersion,
		latestVersion:  data.LatestVersion,
		updateAvail:    data.UpdateAvailable,
		downloadURL:    data.DownloadURL,
	}
	if !data.OK && data.Error != "" {
		result.html = errorHTML(data.Error)
		return result
	}

	// 渲染展示 HTML
	if data.UpdateAvailable {
		result.html = fmt.Sprintf(`<html><body style="font-family:sans-serif;padding:24px;color:#333;">
			<h3 style="margin:0 0 8px;color:#d97706;">⚠ 发现新版本</h3>
			<p style="margin:0 0 4px;">当前版本: <b>%s</b></p>
			<p style="margin:0 0 16px;">最新版本: <b style="color:#10b981;">v%s</b></p>
			<button onclick="openRelease()" style="padding:10px 20px;background:#10b981;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:14px;">立即去下载</button>
			<p style="margin-top:12px;color:#888;font-size:11px;">会打开 GitCode release 页面, 下载后请手动替换本目录的 EXE</p>
		</body></html>`, data.CurrentVersion, data.LatestVersion)
	} else {
		result.html = fmt.Sprintf(`<html><body style="font-family:sans-serif;padding:24px;color:#333;">
			<h3 style="margin:0 0 8px;color:#10b981;">✓ 已是最新版本</h3>
			<p style="margin:0 0 4px;">当前版本: <b>v%s</b></p>
			<p style="margin:0;color:#888;font-size:11px;">此窗口可关闭, launcher 检测到新版本时会自动提示</p>
		</body></html>`, data.CurrentVersion)
	}
	return result
}

func errorHTML(msg string) string {
	// JS escape 一下, 避免消息里含 < > 等破坏 HTML
	escaped := strings.ReplaceAll(strings.ReplaceAll(msg, "<", "&lt;"), ">", "&gt;")
	return fmt.Sprintf(`<html><body style="font-family:sans-serif;padding:24px;color:#333;">
		<h3 style="margin:0 0 8px;color:#c00;">检查更新失败</h3>
		<pre style="background:#fef2f2;padding:12px;border-radius:6px;color:#991b1b;white-space:pre-wrap;">%s</pre>
		<p style="color:#888;font-size:11px;">此窗口可关闭, 不影响主仪表盘</p>
	</body></html>`, escaped)
}

// openInBrowser 用默认浏览器打开 URL (在 webview 上下文中 exec 是真开系统浏览器)
func openInBrowser(url string) {
	exec.Command("cmd", "/c", "start", "", url).Start()
}

func isPortOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// findServerExe 找主程序 TokenMonitor.exe, 优先 launcher 同目录
func findServerExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	candidate := filepath.Join(dir, "TokenMonitor.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("在 %s 找不到 TokenMonitor.exe", dir)
}

// showErrorAndExit 用 webview 弹错误 (GUI 模式下没法用 fmt.Println 提示)
func showErrorAndExit(msg string) {
	fmt.Fprintln(os.Stderr, "[launcher] 错误: "+msg)
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Token Monitor - 启动失败")
	w.SetSize(500, 200, webview.HintFixed)
	w.SetHtml("<html><body style='font-family:sans-serif;padding:20px;color:#c00;'><h3>启动失败</h3><pre>" + msg + "</pre></body></html>")
	w.Run()
	os.Exit(1)
}
