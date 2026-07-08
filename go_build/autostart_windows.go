//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const autoStartRegKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
const autoStartValueName = "TokenMonitor"

// isAutoStartEnabled 检查注册表 HKCU\...\Run 里有没有 TokenMonitor 条目
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
func enableAutoStart() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.SetStringValue(autoStartValueName, `"`+exePath+`"`)
}

// disableAutoStart 删注册表条目, 取消自启
func disableAutoStart() {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.DeleteValue(autoStartValueName)
}
