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
)

// startGUI 启动系统托盘 + WebView2, 阻塞主线程
func startGUI(port int, feedURL string) {
	systray.Run(func() {
		onTrayReady(port, feedURL)
	}, func() {
		onTrayExit()
	})
}

func onTrayReady(port int, feedURL string) {
	// 托盘图标 + tooltip
	systray.SetIcon(trayIconBytes)
	systray.SetTitle("")
	systray.SetTooltip("Token Monitor")

	// 菜单
	mShow := systray.AddMenuItem("显示仪表盘", "打开/聚焦 Token Monitor 窗口")
	systray.AddSeparator()
	mCheckUpdate := systray.AddMenuItem("检查更新", "检查是否有新版本")
	mAutoStart := systray.AddMenuItem("开机自启", "开机时自动启动 Token Monitor")
	// 初始状态: 读注册表判断是否已设自启
	if isAutoStartEnabled() {
		mAutoStart.Check()
	}
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "关闭 Token Monitor")

	// WebView2 在 locked OS thread 上跑 (Windows GUI 需要消息循环绑定线程)
	guiWg.Add(1)
	go func() {
		defer guiWg.Done()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		for {
			select {
			case <-showChan:
				// 收到"显示"信号, 创建 WebView2 窗口
			case <-exitChan:
				return
			}

			windowMu.Lock()
			if windowUp {
				windowMu.Unlock()
				continue // 窗口已开, 忽略
			}
			windowUp = true
			windowMu.Unlock()

			w := webview.New(false)
			w.SetTitle("Token Monitor")
			w.SetSize(1280, 800, webview.HintNone)

			// 存全局引用, 给 injectToast 用
			windowMu.Lock()
			currentWv = w
			windowMu.Unlock()

			// 先加载暗色 loading 页, 避免白屏/黑边闪烁
			w.SetHtml(`<!html><body style="background:#0e1116;margin:0;display:flex;align-items:center;justify-content:center;height:100vh;font-family:sans-serif;color:#58a6ff;"><div>Token Monitor 加载中...</div></body></html>`)

			// 500ms 后导航到真实仪表盘 (给 HTTP server goroutine 时间就绪)
			go func() {
				time.Sleep(500 * time.Millisecond)
				w.Dispatch(func() {
					w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))
				})
			}()

			// 检查更新 (goroutine, 有新版注入 JS toast)
			updateDone := make(chan struct{})
			go func() {
				startUpdateCheckLoop(w, port)
				close(updateDone)
			}()

			w.Run() // 阻塞, 用户关窗口返回
			w.Destroy()

			<-updateDone // 等检查更新 goroutine 退出

			windowMu.Lock()
			windowUp = false
			currentWv = nil
			windowMu.Unlock()

			// 关窗口后不退出, 回到循环等下次"显示"或"退出"
			select {
			case <-exitChan:
				return
			default:
			}
		}
	}()

	// 初始显示窗口
	showChan <- struct{}{}

	// 后台定时检查更新, 有新版给托盘菜单加红点 + 改标题
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
				select {
				case showChan <- struct{}{}:
				default: // channel 满, 跳过
				}
			case <-mCheckUpdate.ClickedCh:
				// 手动点"检查更新": 先检查 (跟 About 页面一样调 /api/check-update)
				// 有新版 → toast 提示 + 后台下载替换重启
				// 失败 → toast 显示错误
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
				close(exitChan)
				systray.Quit()
				return
			}
		}
	}()
}

// checkAndUpdateTray 检查更新, 有新版给托盘菜单加红点 + 改 tooltip
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
		// 有新版: 菜单加红点 + tooltip 提示
		mCheckUpdate.SetTitle("● 检查更新 (有新版 v" + data.LatestVersion + ")")
		systray.SetTooltip("Token Monitor - 有新版本 v" + data.LatestVersion + ", 点击检查更新")
	} else {
		// 无新版: 恢复正常
		mCheckUpdate.SetTitle("检查更新")
		systray.SetTooltip("Token Monitor")
	}
}

// doTrayCheckUpdate 用户点托盘"检查更新"时调用
// 1. 调 /api/check-update (server 端走系统代理访问 GitCode)
// 2. 有新版 → 在主窗口注入 toast "正在下载更新..." → 调 trySelfUpdate 下载替换重启
// 3. 无新版 → toast "已是最新版本"
// 4. 失败 → toast 显示错误信息 (不再静默)
func doTrayCheckUpdate(port int) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/check-update", port))
	if err != nil {
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
		injectToast("检查更新失败: 解析响应失败", "#f85149")
		return
	}
	if !data.OK {
		injectToast("检查更新失败: "+data.Error, "#f85149")
		return
	}
	if !data.UpdateAvailable {
		injectToast("✓ 已是最新版本 (v"+data.LatestVersion+")", "#3fb950")
		return
	}
	// 有新版: 先弹 toast "正在下载", 然后下载替换重启
	injectToast("发现新版本 v"+data.LatestVersion+", 正在下载更新...", "#f59e0b")
	if trySelfUpdate(port) {
		injectToast("下载完成, 正在重启...", "#f59e0b")
		time.Sleep(2 * time.Second)
		systray.Quit()
	} else {
		injectToast("下载失败, 请检查网络或稍后重试", "#f85149")
	}
}

// injectToast 在 WebView2 主窗口注入一个 toast 通知
func injectToast(msg string, color string) {
	windowMu.Lock()
	wv := currentWv
	windowMu.Unlock()
	if wv == nil {
		return // 窗口没开, 无法注入
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

func onTrayExit() {
	// systray.Quit() 返回后, 主线程解除阻塞
	// 等 WebView2 goroutine 退出
	guiWg.Wait()
}

// ─── 检查更新 + 静默自更新 ───

func startUpdateCheckLoop(w webview.WebView, port int) {
	time.Sleep(10 * time.Second)

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		// trySelfUpdate: 检查 + 下载 + 替换 + 重启
		// 返回 true = 更新已触发, 进程应退出
		if trySelfUpdate(port) {
			// 显示 toast 告知用户
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
