//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const autoStartRegKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
const autoStartValueName = "TokenMonitor"
// Win11 的 StartupApproved 会覆盖 Run 项, 必须同时写这里才能生效
const startupApprovedKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

// isAutoStartEnabled 检查注册表 Run 里有没有 TokenMonitor 条目
func isAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autoStartValueName)
	return err == nil
}

// enableAutoStart 写注册表, 开机自启
// Win11 需要同时写 Run + StartupApproved (否则任务管理器可能标记为禁用)
func enableAutoStart() {
	exePath, err := os.Executable()
	if err != nil {
		guiLog("enableAutoStart: os.Executable failed: %v", err)
		return
	}
	guiLog("enableAutoStart: exePath=%s", exePath)

	// 1. 写 Run 项
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE)
	if err != nil {
		guiLog("enableAutoStart: CreateKey Run failed: %v", err)
		return
	}
	if err := k.SetStringValue(autoStartValueName, `"`+exePath+`"`); err != nil {
		guiLog("enableAutoStart: SetStringValue failed: %v", err)
		k.Close()
		return
	}
	k.Close()
	guiLog("enableAutoStart: Run 项写入成功")

	// 2. 写 StartupApproved 项 (Win11 必需)
	// 值是 12 字节二进制: 前 4 字节 03=启用, 02=禁用, 后 8 字节全 0
	saKey, _, err := registry.CreateKey(registry.CURRENT_USER, startupApprovedKey, registry.SET_VALUE)
	if err != nil {
		guiLog("enableAutoStart: CreateKey StartupApproved failed: %v (非致命, Win10 不需要)", err)
		return
	}
	defer saKey.Close()
	// 03 00 00 00 00 00 00 00 00 00 00 00 = 启用
	enabled := []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := saKey.SetBinaryValue(autoStartValueName, enabled); err != nil {
		guiLog("enableAutoStart: SetBinaryValue StartupApproved failed: %v", err)
	} else {
		guiLog("enableAutoStart: StartupApproved 写入成功 (03=启用)")
	}

	// 3. 读回验证
	k2, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.QUERY_VALUE)
	if err == nil {
		val, _, err := k2.GetStringValue(autoStartValueName)
		k2.Close()
		if err == nil {
			guiLog("enableAutoStart: 验证读回值=%s", val)
		}
	}

	fmt.Printf("[autostart] 已启用开机自启: %s\n", exePath)
}

// disableAutoStart 删注册表条目, 取消自启
func disableAutoStart() {
	// 1. 删 Run 项
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	k.DeleteValue(autoStartValueName)
	k.Close()
	guiLog("disableAutoStart: Run 项已删除")

	// 2. 删 StartupApproved 项
	saKey, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedKey, registry.SET_VALUE)
	if err == nil {
		saKey.DeleteValue(autoStartValueName)
		saKey.Close()
		guiLog("disableAutoStart: StartupApproved 已删除")
	}

	fmt.Printf("[autostart] 已取消开机自启\n")
}
