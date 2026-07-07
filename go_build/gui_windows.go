//go:build windows

// Token Monitor Windows GUI
// v1.3.95: 单 exe = HTTP server + WebView2 + 系统托盘 + 自更新
// 系统托盘: 🔥 图标 + 右键菜单 (显示仪表盘 / 检查更新 / 退出)
// 关窗口 → 隐藏到托盘 (不退出进程)
package main

import (
	_ "embed"
	"fmt"
	"runtime"
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
			w.SetSize(1280, 800, webview.HintFixed)
			w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))

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

	// 菜单事件
	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				select {
				case showChan <- struct{}{}:
				default: // channel 满, 跳过
				}
			case <-mQuit.ClickedCh:
				close(exitChan)
				systray.Quit()
				return
			}
		}
	}()
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
