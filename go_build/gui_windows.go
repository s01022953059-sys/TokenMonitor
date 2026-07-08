//go:build windows

// Token Monitor Windows GUI
// v1.3.95: 单 exe = HTTP server + WebView2 + 系统托盘 + 自更新
// 系统托盘: 🔥 图标 + 右键菜单 (显示仪表盘 / 检查更新 / 退出)
// 关窗口 → 隐藏到托盘 (不退出进程)
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

// GUI 状态
var (
	showChan  = make(chan struct{}, 1)
	exitChan  = make(chan struct{})
	guiWg     sync.WaitGroup
	windowMu  sync.Mutex
	windowUp  bool
	currentWv webview.WebView // 当前 WebView2 引用, 给 injectToast 用
	guiPort   int              // 给 doTrayCheckUpdate 用
)

// guiLog 写日志到 %TEMP%/token_monitor_gui.log (调试托盘/窗口问题)
func guiLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	f, err := os.OpenFile(os.TempDir()+"/token_monitor_gui.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.WriteString(time.Now().Format("15:04:05 ") + msg + "\n")
		f.Close()
	}
}

// startGUI 启动系统托盘 + WebView2, 阻塞主线程
func startGUI(port int, feedURL string) {
	guiPort = port
	guiLog("startGUI port=%d feedURL=%s", port, feedURL)
	systray.Run(func() {
		onTrayReady(port, feedURL)
	}, func() {
		onTrayExit()
	})
}

func onTrayReady(port int, feedURL string) {
	guiLog("onTrayReady")
	// 托盘图标 + tooltip
	systray.SetIcon(trayIconBytes)
	systray.SetTitle("")
	systray.SetTooltip("Token Monitor")

	// 菜单
	mShow := systray.AddMenuItem("显示仪表盘", "打开/聚焦 Token Monitor 窗口")
	systray.AddSeparator()
	mCheckUpdate := systray.AddMenuItem("检查更新", "检查是否有新版本")
	mAutoStart := systray.AddMenuItem("开机自启", "开机时自动启动 Token Monitor")
	if isAutoStartEnabled() {
		mAutoStart.Check()
	}
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "关闭 Token Monitor")

	// WebView2 在 locked OS thread 上跑 (Windows GUI 需要消息循环绑定线程)
	guiWg.Add(1)
	go func() {
		defer guiWg.Done()
		// v1.3.100: 加 panic recovery, WebView2 初始化失败不会杀 goroutine
		defer func() {
			if r := recover(); r != nil {
				guiLog("PANIC in webview goroutine: %v", r)
				systray.SetTooltip("Token Monitor - 窗口初始化失败, 请检查 WebView2 运行时")
			}
		}()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		for {
			select {
			case <-showChan:
				guiLog("showChan received, creating window")
			case <-exitChan:
				guiLog("exitChan received, webview goroutine exiting")
				return
			}

			windowMu.Lock()
			if windowUp {
				windowMu.Unlock()
				guiLog("window already up, skip")
				continue
			}
			windowUp = true
			windowMu.Unlock()

			guiLog("calling webview.New()")
			var w webview.WebView
			func() {
				defer func() {
					if r := recover(); r != nil {
						guiLog("PANIC in webview.New: %v", r)
						windowMu.Lock()
						windowUp = false
						currentWv = nil
						windowMu.Unlock()
						systray.SetTooltip("Token Monitor - WebView2 初始化失败")
					}
				}()
				w = webview.New(false)
			}()
			if w == nil {
				guiLog("webview.New returned nil")
				windowMu.Lock()
				windowUp = false
				windowMu.Unlock()
				continue
			}
			guiLog("webview created OK")

			w.SetTitle("Token Monitor")
			w.SetSize(1280, 800, webview.HintNone)

			// v1.4.01: 注册 JS 桥, 让前端"立即更新"按钮能调 Win 自更新
			// 前端 JS: if (window.triggerWinUpdate) { window.triggerWinUpdate(); }
			updatePort := port
			w.Bind("triggerWinUpdate", func() string {
				guiLog("JS bridge: triggerWinUpdate called")
				go doTrayCheckUpdate(updatePort)
				return "ok"
			})

			windowMu.Lock()
			currentWv = w
			windowMu.Unlock()

			w.SetHtml(`<!html><body style="background:#0e1116;margin:0;display:flex;align-items:center;justify-content:center;height:100vh;font-family:sans-serif;color:#58a6ff;"><div>Token Monitor 加载中...</div></body></html>`)

			go func() {
				time.Sleep(500 * time.Millisecond)
				w.Dispatch(func() {
					w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))
				})
			}()

			updateDone := make(chan struct{})
			go func() {
				startUpdateCheckLoop(w, port)
				close(updateDone)
			}()

			guiLog("calling w.Run()")
			w.Run()
			guiLog("w.Run() returned, destroying")
			w.Destroy()

			<-updateDone

			windowMu.Lock()
			windowUp = false
			currentWv = nil
			windowMu.Unlock()

			select {
			case <-exitChan:
				return
			default:
			}
		}
	}()

	// 初始显示窗口
	guiLog("sending initial showChan")
	showChan <- struct{}{}

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
				// 阻塞式 send (不用 default 丢弃), 在 goroutine 里发避免卡死
				go func() {
					showChan <- struct{}{}
				}()
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

func onTrayExit() {
	guiLog("onTrayExit")
	guiWg.Wait()
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
			continue
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

// doTrayCheckUpdate 用户点托盘"检查更新"时调用
// 1. 调 /api/check-update (server 端走系统代理访问 GitCode)
// 2. 有新版 → toast "正在下载" → trySelfUpdate 下载替换重启
// 3. 无新版 → toast "已是最新版本"
// 4. 失败 → toast + systray tooltip 双重反馈 (窗口关了也能看到)
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
		guiLog("doTrayCheckUpdate: decode failed: %v", err)
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
	// 有新版
	injectToast("发现新版本 v"+data.LatestVersion+", 正在下载更新...", "#f59e0b")
	systray.SetTooltip("Token Monitor - 正在下载 v" + data.LatestVersion + "...")
	if trySelfUpdate(port) {
		injectToast("下载完成, 正在重启...", "#f59e0b")
		systray.SetTooltip("Token Monitor - 更新完成, 正在重启...")
		time.Sleep(2 * time.Second)
		systray.Quit()
	} else {
		injectToast("下载失败, 请检查网络或稍后重试", "#f85149")
		systray.SetTooltip("Token Monitor - 下载失败, 请稍后重试")
	}
}

// injectToast 在 WebView2 主窗口注入一个 toast 通知
// 如果窗口没开 (currentWv == nil), 静默跳过 (调用方应用 systray.SetTooltip 兜底)
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
