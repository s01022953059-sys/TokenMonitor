//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
)

const (
	GWL_STYLE         = ^uintptr(15) // -16 as uintptr
	WS_OVERLAPPED     = 0x00000000
	WS_CAPTION        = 0x00C00000
	WS_SYSMENU        = 0x00080000
	WS_THICKFRAME     = 0x00040000
	WS_MINIMIZEBOX    = 0x00020000
	WS_MAXIMIZEBOX    = 0x00010000
	SWP_NOMOVE        = 0x0002
	SWP_NOSIZE        = 0x0001
	SWP_NOZORDER      = 0x0004
	SWP_FRAMECHANGED  = 0x0020
)

// thinWindowBorder 去掉窗口粗边框 (WS_THICKFRAME) + 最大化按钮,
// 保留标题栏 + 关闭按钮 + 最小化按钮, 让窗口边框变薄
func thinWindowBorder(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	h := uintptr(hwnd)
	// 读当前样式
	style, _, _ := procGetWindowLongPtrW.Call(h, uintptr(GWL_STYLE))
	// 去掉 WS_THICKFRAME + WS_MAXIMIZEBOX, 保留其余
	newStyle := style &^ WS_THICKFRAME &^ WS_MAXIMIZEBOX
	procSetWindowLongPtrW.Call(h, uintptr(GWL_STYLE), newStyle)
	// 刷新窗口框架 (SWP_FRAMECHANGED + 不移动 + 不改大小)
	procSetWindowPos.Call(h, 0, 0, 0, 0, 0,
		uintptr(SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_FRAMECHANGED))
}
