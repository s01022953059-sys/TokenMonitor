//go:build windows

// Token Monitor Windows GUI
// v1.4.03: 窗口只创建一次, 关窗口=隐藏, 托盘"显示"=ShowWindow
// 不再 Destroy+重新 New webview (COM apartment 绑定线程, 重建会失败)
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
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"github.com/getlantern/systray"
)

//go:embed icon.ico
var trayIconBytes []byte

var (
	exitChan  = make(chan struct{})
	guiWg     sync.WaitGroup
	currentWv webview.WebView
	windowReady bool
	windowMu  sync.Mutex
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
		guiLog("onTrayExit, forceCloseWindow")
		forceCloseWindow()
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

	// v1.4.03: 窗口只创建一次, 在独占线程上跑消息循环
	// 关窗口 = WM_CLOSE 被拦截 → SW_HIDE (窗口对象不销毁)
	// 托盘"显示" = PostMessage(WM_USER_SHOW) → SW_RESTORE
	guiWg.Add(1)
	go func() {
		defer guiWg.Done()
		defer func() {
			if r := recover(); r != nil {
				guiLog("PANIC in webview goroutine: %v", r)
			}
		}()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		guiLog("creating webview (only once)")
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
			guiLog("webview.New returned nil")
			systray.SetTooltip("Token Monitor - WebView2 初始化失败")
			return
		}
		guiLog("webview created OK")

		w.SetTitle("Token Monitor")
		w.SetSize(1280, 800, webview.HintNone)
		// v1.4.08: 根据屏幕分辨率调整窗口大小 + 居中, 防止越界
		fitWindowToScreen(uintptr(w.Window()))

		// JS 桥
		updatePort := port
		w.Bind("triggerWinUpdate", func() string {
			guiLog("JS bridge: triggerWinUpdate")
			go doTrayCheckUpdate(updatePort)
			return "ok"
		})

		windowMu.Lock()
		currentWv = w
		windowReady = true
		windowMu.Unlock()

		// 安装 WM_CLOSE 拦截 (关窗口按钮 → 隐藏)
		setupWindowHide(w.Window())

		// loading 页
		w.SetHtml(`<!html><body style="background:#0e1116;margin:0;display:flex;align-items:center;justify-content:center;height:100vh;font-family:sans-serif;color:#58a6ff;"><div>Token Monitor 加载中...</div></body></html>`)

		// 500ms 后导航
		go func() {
			time.Sleep(500 * time.Millisecond)
			w.Dispatch(func() {
				w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))
			})
		}()

		// 检查更新循环
		go startUpdateCheckLoop(w, port)

		guiLog("calling w.Run() (message loop, blocks forever until forceClose)")
		w.Run()
		guiLog("w.Run() returned")
	}()

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
				// 如果窗口已创建 → ShowWindow; 否则等创建
				windowMu.Lock()
				ready := windowReady
				windowMu.Unlock()
				if ready {
					guiLog("showHiddenWindow called")
					showHiddenWindow()
				} else {
					guiLog("window not ready yet, skip")
				}
			case <-mCheckUpdate.ClickedCh:
				guiLog("menu: 检查更新 clicked")
				go doTrayCheckUpdate(port)
			case <-mAutoStart.ClickedCh:
				if isAutoStartEnabled() {
					disableAutoStart()
					mAutoStart.Uncheck()
					injectToast("已取消开机自启", "#6b7280")
				} else {
					enableAutoStart()
					mAutoStart.Check()
					injectToast("✓ 已设置开机自启", "#3fb950")
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

// injectProgress 更新 About 页面的进度条 (Mac Swift + Win Go 共用 __tmSetUpdateProgress)
func injectProgress(pct int, text string) {
	windowMu.Lock()
	wv := currentWv
	windowMu.Unlock()
	if wv == nil {
		return
	}
	safeText := strings.ReplaceAll(text, `'`, `\'`)
	js := fmt.Sprintf(`window.__tmSetUpdateProgress && window.__tmSetUpdateProgress(%d, '%s');`, pct, safeText)
	wv.Dispatch(func() {
		wv.Eval(js)
	})
}

func injectToast(msg string, color string) {
	windowMu.Lock()
	wv := currentWv
	windowMu.Unlock()
	if wv == nil {
		guiLog("injectToast: window not ready, skip (msg=%s)", msg)
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

// 引用 unsafe 包 (w.Window() 返回 unsafe.Pointer)
var _ unsafe.Pointer
