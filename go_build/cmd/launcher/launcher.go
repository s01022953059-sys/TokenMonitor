//go:build windows

// Token Monitor Win Launcher
// 启动主服务进程 + 用 WebView2 内嵌 UI (不调起系统默认浏览器, 避免污染用户浏览器标签页)
//
// 流程:
//   1. 探活 127.0.0.1:15723 端口, 已占用说明主程序在跑, 直接建 webview 接上去
//   2. 端口空 → exec 启动 TokenMonitor.exe --no-browser
//   3. 轮询端口就绪 (最多 8 秒) → 建 webview 导航
//   4. webview 窗口关闭 → 不杀主进程, 后台继续
//   5. 双击 launcher 再次 → webview 重连(单实例靠端口检测, 不上锁)
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	webview "github.com/jchv/go-webview2"
)

const defaultPort = 15723
const launcherTitle = "Token Monitor"

func main() {
	port := defaultPort
	// 解析可选 --port 参数
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--port" && i+1 < len(os.Args) {
			fmt.Sscanf(os.Args[i+1], "%d", &port)
		}
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
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

	// 起 WebView2 窗口
	w := webview.New(false) // false = 不开 dev tools
	defer w.Destroy()
	w.SetTitle(launcherTitle)
	w.SetSize(1280, 800, webview.HintFixed)
	w.Navigate("http://" + addr)
	w.Run()
	// w.Run() 返回说明用户关了 webview 窗口
	// 不杀主进程 — 留后台, 下次双击 launcher 直接 webview 重连
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
