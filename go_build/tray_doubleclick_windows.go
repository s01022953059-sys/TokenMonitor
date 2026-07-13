//go:build windows

package main

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wmUserTrayCallback = 0x0401
	wmLButtonUp        = 0x0202
	wmLButtonDblClk    = 0x0203
)

var (
	trayWndProcMu  sync.Mutex
	trayOldWndProc uintptr
	trayNewWndProc = syscall.NewCallback(trayWindowProc)
)

// installTrayDoubleClickHandler 补充 getlantern/systray 未暴露的双击行为。
// 左键单击不弹菜单，右键仍由原托盘过程处理；双击直接回到首页。
func installTrayDoubleClickHandler() error {
	user32 := syscall.NewLazyDLL("user32.dll")
	findWindow := user32.NewProc("FindWindowW")
	setWindowLongPtr := user32.NewProc("SetWindowLongPtrW")
	className, _ := syscall.UTF16PtrFromString("SystrayClass")
	hwnd, _, _ := findWindow.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd == 0 {
		return fmt.Errorf("找不到托盘窗口")
	}
	previous, _, callErr := setWindowLongPtr.Call(hwnd, ^uintptr(3), trayNewWndProc)
	if previous == 0 {
		return fmt.Errorf("注册托盘双击失败: %v", callErr)
	}
	trayWndProcMu.Lock()
	trayOldWndProc = previous
	trayWndProcMu.Unlock()
	return nil
}

func trayWindowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if message == wmUserTrayCallback {
		switch lParam {
		case wmLButtonUp:
			// 吞掉单击，保留右键菜单作为托盘菜单入口。
			return 0
		case wmLButtonDblClk:
			go showDashboardHome()
			return 0
		}
	}

	trayWndProcMu.Lock()
	previous := trayOldWndProc
	trayWndProcMu.Unlock()
	if previous == 0 {
		return 0
	}
	callWindowProc := syscall.NewLazyDLL("user32.dll").NewProc("CallWindowProcW")
	result, _, _ := callWindowProc.Call(previous, hwnd, uintptr(message), wParam, lParam)
	return result
}
