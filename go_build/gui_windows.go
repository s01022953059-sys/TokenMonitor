//go:build windows

// Token Monitor Windows GUI (WebView2 内嵌仪表盘 + 检查更新 toast)
// v1.3.95: 合并 launcher 到主程序, 单 exe 单窗口
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	webview "github.com/jchv/go-webview2"
)

// startGUI 启动 WebView2 主窗口 (Windows), 阻塞主线程
// HTTP server 在 goroutine 里跑, webview2 在主线程
func startGUI(port int, feedURL string) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Token Monitor")
	w.SetSize(1280, 800, webview.HintFixed)
	w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))

	// 定时检查更新 (goroutine), 有新版在主窗口注入 JS toast
	go startUpdateCheckLoop(w, port)

	// w.Run() 阻塞, 用户关窗口返回
	// Step 2 会改成关窗口隐藏到托盘, 这里先退出
	w.Run()
}

// startUpdateCheckLoop 每 30 分钟检查更新, 有新版注入 JS toast 到主窗口
func startUpdateCheckLoop(w webview.WebView, port int) {
	// 启动后 10 秒先检查一次 (网络可能还没就绪)
	time.Sleep(10 * time.Second)
	checkOnce(w, port)

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		checkOnce(w, port)
	}
}

func checkOnce(w webview.WebView, port int) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/check-update", port))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var data struct {
		OK              bool   `json:"ok"`
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable bool   `json:"update_available"`
		DownloadURL     string `json:"download_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}
	if !data.OK || !data.UpdateAvailable {
		return
	}
	// 注入 JS toast 到主窗口
	// 不再开第二个 webview, 而是在仪表盘右上角弹一个可点击的 div
	url := strings.ReplaceAll(data.DownloadURL, `"`, `\"`)
	js := fmt.Sprintf(`
		(function() {
			if (window.__tmUpdateToast) return;
			var t = document.createElement('div');
			t.id = 'tm-update-toast';
			t.style.cssText = 'position:fixed;top:20px;right:20px;background:#10b981;color:#fff;padding:12px 20px;border-radius:8px;font-size:14px;cursor:pointer;z-index:99999;box-shadow:0 4px 12px rgba(0,0,0,0.3);font-family:sans-serif;';
			t.innerHTML = '🔥 有新版本 v%s, 点击下载更新';
			t.onclick = function() { window.open('%s', '_blank'); t.remove(); window.__tmUpdateToast = false; };
			document.body.appendChild(t);
			window.__tmUpdateToast = true;
			setTimeout(function() { if (t.parentNode) { t.remove(); window.__tmUpdateToast = false; } }, 30000);
		})();
	`, data.LatestVersion, url)
	w.Dispatch(func() {
		w.Eval(js)
	})
}
