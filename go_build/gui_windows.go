//go:build windows

// Token Monitor Windows GUI
// v1.4.02: 单 exe = HTTP server + WebView2 + 系统托盘 + 自更新
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	webview "github.com/jchv/go-webview2"
	"github.com/getlantern/systray"
)

//go:embed icon.ico
var trayIconBytes []byte

var (
	exitChan  = make(chan struct{})
	guiWg     sync.WaitGroup
	windowMu  sync.Mutex
	windowUp  bool
	currentWv webview.WebView
	guiPort   int
)

func guiLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	f, err := os.OpenFile(os.TempDir()+"/token_monitor_gui.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.WriteString(time.Now().Format("15:04:05 ") + msg + "\n")
		f.Close()
	}
}

func startGUI(port int, feedURL string) {
	guiPort = port
	guiLog("startGUI port=%d", port)
	systray.Run(func() {
		onTrayReady(port, feedURL)
	}, func() {
		guiLog("onTrayExit")
		guiWg.Wait()
	})
}

func onTrayReady(port int, feedURL string) {
	guiLog("onTrayReady")
	systray.SetIcon(trayIconBytes)
	systray.SetTitle("")
	systray.SetTooltip("Token Monitor")

	mShow := systray.AddMenuItem("显示仪表盘", "打开 Token Monitor 窗口")
	systray.AddSeparator()
	mCheckUpdate := systray.AddMenuItem("检查更新", "检查是否有新版本")
	mAutoStart := systray.AddMenuItem("开机自启", "开机时自动启动")
	if isAutoStartEnabled() {
		mAutoStart.Check()
	}
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "关闭 Token Monitor")

	// 初始显示
	go showWebView(port)

	// 后台定时检查更新
	go func() {
		time.Sleep(10 * time.Second)
		checkAndUpdateTray(port, mCheckUpdate)
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				checkAndUpdateTray(port, mCheckUpdate)
			case <-exitChan:
				return
			}
		}
	}()

	// 菜单事件
	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				guiLog("menu: 显示仪表盘 clicked")
				go showWebView(port)
			case <-mCheckUpdate.ClickedCh:
				guiLog("menu: 检查更新 clicked")
				go doTrayCheckUpdate(port)
			case <-mAutoStart.ClickedCh:
				if isAutoStartEnabled() {
					disableAutoStart()
					mAutoStart.Uncheck()
				} else {
					enableAutoStart()
					mAutoStart.Check()
				}
			case <-mQuit.ClickedCh:
				guiLog("menu: 退出 clicked")
				close(exitChan)
				systray.Quit()
				return
			}
		}
	}()
}

// showWebView 在新 goroutine + 新 OS thread 上创建 WebView2 窗口
// v1.4.02: 每次都用新 thread, 避免 Destroy 后同线程重新 New 失败
func showWebView(port int) {
	windowMu.Lock()
	if windowUp {
		windowMu.Unlock()
		guiLog("showWebView: already up, skip")
		return
	}
	windowUp = true
	windowMu.Unlock()

	guiWg.Add(1)
	go func() {
		defer guiWg.Done()
		// 每次都 LockOSThread, 确保WebView2 在独占线程上创建+销毁
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		// panic recovery
		defer func() {
			if r := recover(); r != nil {
				guiLog("PANIC in showWebView: %v", r)
				systray.SetTooltip("Token Monitor - 窗口创建失败")
			}
			windowMu.Lock()
			windowUp = false
			currentWv = nil
			windowMu.Unlock()
		}()

		guiLog("showWebView: creating webview")
		var w webview.WebView
		func() {
			defer func() {
				if r := recover(); r != nil {
					guiLog("PANIC in webview.New: %v", r)
				}
			}()
			w = webview.New(false)
		}()
		if w == nil {
			guiLog("showWebView: webview.New returned nil")
			return
		}
		guiLog("showWebView: webview created OK")

		w.SetTitle("Token Monitor")
		w.SetSize(1280, 800, webview.HintNone)

		// JS 桥: 前端"立即更新"调 triggerWinUpdate
		updatePort := port
		w.Bind("triggerWinUpdate", func() string {
			guiLog("JS bridge: triggerWinUpdate called")
			go doTrayCheckUpdate(updatePort)
			return "ok"
		})

		windowMu.Lock()
		currentWv = w
		windowMu.Unlock()

		// 暗色 loading 页
		w.SetHtml(`<!html><body style="background:#0e1116;margin:0;display:flex;align-items:center;justify-content:center;height:100vh;font-family:sans-serif;color:#58a6ff;"><div>Token Monitor 加载中...</div></body></html>`)

		// 500ms 后导航到仪表盘
		go func() {
			time.Sleep(500 * time.Millisecond)
			w.Dispatch(func() {
				w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))
			})
		}()

		// 检查更新循环 (随窗口生命周期)
		updateDone := make(chan struct{})
		go func() {
			startUpdateCheckLoop(w, port)
			close(updateDone)
		}()

		guiLog("showWebView: calling w.Run()")
		w.Run()
		guiLog("showWebView: w.Run() returned, destroying")
		w.Destroy()
		<-updateDone

		guiLog("showWebView: window closed, goroutine exiting")
	}()
}

// ─── 检查更新 + 静默自更新 ───

func startUpdateCheckLoop(w webview.WebView, port int) {
	time.Sleep(10 * time.Second)
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		if trySelfUpdate(port) {
			w.Dispatch(func() {
				w.Eval(`(function(){var t=document.createElement('div');t.style.cssText='position:fixed;top:20px;right:20px;background:#f59e0b;color:#fff;padding:12px 20px;border-radius:8px;font-size:14px;z-index:99999;';t.innerHTML='更新完成, 正在重启...';document.body.appendChild(t);})();`)
			})
			time.Sleep(2 * time.Second)
			systray.Quit()
			return
		}
		select {
		case <-ticker.C:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func checkAndUpdateTray(port int, mCheckUpdate *systray.MenuItem) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/check-update", port))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var data struct {
		OK              bool   `json:"ok"`
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable bool   `json:"update_available"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}
	if data.OK && data.UpdateAvailable {
		mCheckUpdate.SetTitle("● 检查更新 (有新版 v" + data.LatestVersion + ")")
		systray.SetTooltip("Token Monitor - 有新版本 v" + data.LatestVersion + ", 点击检查更新")
	} else {
		mCheckUpdate.SetTitle("检查更新")
		systray.SetTooltip("Token Monitor")
	}
}

func doTrayCheckUpdate(port int) {
	guiLog("doTrayCheckUpdate start")
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/check-update", port))
	if err != nil {
		guiLog("doTrayCheckUpdate: http.Get failed: %v", err)
		systray.SetTooltip("Token Monitor - 检查更新失败: 无法连接服务")
		injectToast("检查更新失败: 无法连接本地服务", "#f85149")
		return
	}
	defer resp.Body.Close()
	var data struct {
		OK              bool   `json:"ok"`
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable bool   `json:"update_available"`
		Error           string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		systray.SetTooltip("Token Monitor - 检查更新失败: 解析错误")
		injectToast("检查更新失败: 解析响应失败", "#f85149")
		return
	}
	guiLog("doTrayCheckUpdate: ok=%v updateAvailable=%v error=%s", data.OK, data.UpdateAvailable, data.Error)
	if !data.OK {
		msg := "检查更新失败: " + data.Error
		systray.SetTooltip("Token Monitor - " + msg)
		injectToast(msg, "#f85149")
		return
	}
	if !data.UpdateAvailable {
		systray.SetTooltip("Token Monitor - 已是最新版本 v" + data.LatestVersion)
		injectToast("✓ 已是最新版本 (v"+data.LatestVersion+")", "#3fb950")
		return
	}
	// 有新版 → 下载 + 进度
	injectToast("发现新版本 v"+data.LatestVersion+", 正在下载...", "#f59e0b")
	systray.SetTooltip("Token Monitor - 正在下载 v" + data.LatestVersion + "...")

	if trySelfUpdateWithProgress(port) {
		injectToast("下载完成, 正在安装...", "#f59e0b")
		systray.SetTooltip("Token Monitor - 安装中...")
		time.Sleep(2 * time.Second)
		systray.Quit()
	} else {
		injectToast("下载失败, 请检查网络或稍后重试", "#f85149")
		systray.SetTooltip("Token Monitor - 下载失败, 请稍后重试")
	}
}

func injectToast(msg string, color string) {
	windowMu.Lock()
	wv := currentWv
	windowMu.Unlock()
	if wv == nil {
		guiLog("injectToast: window closed, skip (msg=%s)", msg)
		return
	}
	safeMsg := strings.ReplaceAll(msg, `'`, `\'`)
	js := fmt.Sprintf(`
		(function() {
			var t = document.createElement('div');
			t.style.cssText = 'position:fixed;top:20px;right:20px;background:%s;color:#fff;padding:12px 20px;border-radius:8px;font-size:14px;z-index:99999;box-shadow:0 4px 12px rgba(0,0,0,0.3);font-family:sans-serif;max-width:400px;';
			t.innerHTML = '%s';
			document.body.appendChild(t);
			setTimeout(function() { if (t.parentNode) t.remove(); }, 8000);
		})();
	`, color, safeMsg)
	wv.Dispatch(func() {
		wv.Eval(js)
	})
}
