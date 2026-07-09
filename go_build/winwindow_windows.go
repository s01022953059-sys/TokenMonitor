//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	pUser32                       = syscall.NewLazyDLL("user32.dll")
	pSetWindowLongPtrW            = pUser32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW            = pUser32.NewProc("GetWindowLongPtrW")
	pCallWindowProcW              = pUser32.NewProc("CallWindowProcW")
	pShowWindow                   = pUser32.NewProc("ShowWindow")
	pSetForegroundWindow          = pUser32.NewProc("SetForegroundWindow")
	pPostMessageW                 = pUser32.NewProc("PostMessageW")
	pIsWindowVisible              = pUser32.NewProc("IsWindowVisible")
	pGetSystemMetrics             = pUser32.NewProc("GetSystemMetrics")
	pMoveWindow                   = pUser32.NewProc("MoveWindow")
)

const (
	GWLP_WNDPROC      = ^uintptr(3) // -4 as uintptr
	WM_CLOSE          = 0x0010
	WM_USER_SHOW      = 0x0401       // 自定义消息: 显示窗口
	SW_HIDE           = 0
	SW_SHOW           = 5
	SW_RESTORE        = 9
	SM_CXSCREEN       = 0            // 屏幕宽
	SM_CYSCREEN       = 1            // 屏幕高
)

var (
	originalWndProc uintptr // 原始窗口过程
	hiddenHwnd      uintptr // 当前窗口 HWND (隐藏后保留)
)

// hideWindowProc 拦截 WM_CLOSE: 不关闭, 改为隐藏
// WM_USER_SHOW: 显示窗口
func hideWindowProc(hwnd, msg, wp, lp uintptr) uintptr {
	if msg == WM_CLOSE {
		// 隐藏窗口而不是关闭
		pShowWindow.Call(hwnd, uintptr(SW_HIDE))
		return 0 // 阻止默认关闭
	}
	if msg == WM_USER_SHOW {
		// 显示并前置
		pShowWindow.Call(hwnd, uintptr(SW_RESTORE))
		pSetForegroundWindow.Call(hwnd)
		return 0
	}
	// 其余消息交给原始窗口过程
	ret, _, _ := pCallWindowProcW.Call(originalWndProc, hwnd, msg, wp, lp)
	return ret
}

// setupWindowHide 给窗口安装 WM_CLOSE 拦截 (关窗口按钮 → 隐藏)
// hwnd 是 webview.Window() 返回的 unsafe.Pointer
func setupWindowHide(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	h := uintptr(hwnd)
	hiddenHwnd = h
	// 保存原始窗口过程, 安装自定义
	orig, _, _ := pGetWindowLongPtrW.Call(h, uintptr(GWLP_WNDPROC))
	originalWndProc = orig
	pSetWindowLongPtrW.Call(h, uintptr(GWLP_WNDPROC),
		syscall.NewCallback(hideWindowProc))
}

// showHiddenWindow 显示已隐藏的窗口
func showHiddenWindow() bool {
	if hiddenHwnd == 0 {
		return false
	}
	// 检查窗口是否还存在
	vis, _, _ := pIsWindowVisible.Call(hiddenHwnd)
	if vis != 0 {
		// 已可见, 前置
		pSetForegroundWindow.Call(hiddenHwnd)
		return true
	}
	// 发 WM_USER_SHOW 让窗口过程处理
	pPostMessageW.Call(hiddenHwnd, uintptr(WM_USER_SHOW), 0, 0)
	return true
}

// forceCloseWindow 强制关闭窗口 (退出时用, 不拦截 WM_CLOSE)
func forceCloseWindow() {
	if hiddenHwnd == 0 {
		return
	}
	// 先恢复原始窗口过程 (取消拦截)
	if originalWndProc != 0 {
		pSetWindowLongPtrW.Call(hiddenHwnd, uintptr(GWLP_WNDPROC), originalWndProc)
	}
	// 发 WM_CLOSE 关闭
	pPostMessageW.Call(hiddenHwnd, uintptr(WM_CLOSE), 0, 0)
}

// fitWindowToScreen 根据屏幕分辨率调整窗口大小 + 居中
// v1.4.08: 修复初始化窗口越界 (1280x800 在小屏幕上超出)
func fitWindowToScreen(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	// 获取屏幕分辨率
	screenW, _, _ := pGetSystemMetrics.Call(uintptr(SM_CXSCREEN))
	screenH, _, _ := pGetSystemMetrics.Call(uintptr(SM_CYSCREEN))

	// 窗口不超过屏幕 90%, 最小 900x600
	maxW := int(float64(screenW) * 0.9)
	maxH := int(float64(screenH) * 0.9)
	winW := 1280
	winH := 800
	if winW > maxW {
		winW = maxW
	}
	if winH > maxH {
		winH = maxH
	}
	if winW < 900 {
		winW = 900
	}
	if winH < 600 {
		winH = 600
	}

	// 居中
	posX := (int(screenW) - winW) / 2
	posY := (int(screenH) - winH) / 2
	if posX < 0 {
		posX = 0
	}
	if posY < 0 {
		posY = 0
	}

	// MoveWindow(hwnd, x, y, w, h, repaint)
	pMoveWindow.Call(hwnd, uintptr(posX), uintptr(posY), uintptr(winW), uintptr(winH), 1)
	guiLog("fitWindowToScreen: screen=%dx%d window=%dx%d pos=(%d,%d)", screenW, screenH, winW, winH, posX, posY)
}
