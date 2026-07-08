//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const autoStartRegKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
const autoStartValueName = "TokenMonitor"
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

// enableAutoStart 写注册表 Run + StartupApproved + Startup 文件夹快捷方式
// 三管齐下确保 Win11 开机自启生效
func enableAutoStart() {
	exePath, err := os.Executable()
	if err != nil {
		guiLog("enableAutoStart: os.Executable failed: %v", err)
		return
	}
	guiLog("enableAutoStart: exePath=%s", exePath)

	// 1. 写 Run 注册表项
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

	// 2. 写 StartupApproved (Win11 必需, 否则任务管理器可能标记为禁用)
	saKey, _, err := registry.CreateKey(registry.CURRENT_USER, startupApprovedKey, registry.SET_VALUE)
	if err == nil {
		enabled := []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		if err := saKey.SetBinaryValue(autoStartValueName, enabled); err != nil {
			guiLog("enableAutoStart: SetBinaryValue StartupApproved failed: %v", err)
		} else {
			guiLog("enableAutoStart: StartupApproved 写入成功 (03=启用)")
		}
		saKey.Close()
	}

	// 3. 创建 Startup 文件夹快捷方式 (最可靠的方式, Win11 不会拦截)
	startupDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	lnkPath := filepath.Join(startupDir, "TokenMonitor.lnk")
	if err := createShortcut(lnkPath, exePath); err != nil {
		guiLog("enableAutoStart: 创建快捷方式失败: %v", err)
	} else {
		guiLog("enableAutoStart: Startup 快捷方式创建成功: %s", lnkPath)
	}

	// 4. 读回验证
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

// disableAutoStart 删除所有自启痕迹
func disableAutoStart() {
	// 1. 删 Run 项
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE)
	if err == nil {
		k.DeleteValue(autoStartValueName)
		k.Close()
		guiLog("disableAutoStart: Run 项已删除")
	}

	// 2. 删 StartupApproved
	saKey, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedKey, registry.SET_VALUE)
	if err == nil {
		saKey.DeleteValue(autoStartValueName)
		saKey.Close()
		guiLog("disableAutoStart: StartupApproved 已删除")
	}

	// 3. 删 Startup 快捷方式
	startupDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	lnkPath := filepath.Join(startupDir, "TokenMonitor.lnk")
	if err := os.Remove(lnkPath); err == nil {
		guiLog("disableAutoStart: Startup 快捷方式已删除")
	}

	fmt.Printf("[autostart] 已取消开机自启\n")
}

// createShortcut 创建 .lnk 快捷方式 (用 COM WScript.Shell)
func createShortcut(lnkPath, targetPath string) error {
	ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	defer ole.CoUninitialize()

	clsid, err := ole.CLSIDFromProgID("WScript.Shell")
	if err != nil {
		return fmt.Errorf("CLSIDFromProgID: %w", err)
	}
	unknown, err := ole.CreateInstance(clsid, nil)
	if err != nil {
		return fmt.Errorf("CreateInstance: %w", err)
	}
	defer unknown.Release()

	// IUnknown → IDispatch
	dispatch, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("QueryInterface: %w", err)
	}
	defer dispatch.Release()

	ds := oleutil.MustCallMethod(dispatch, "CreateShortcut", lnkPath)
	shortcut := ds.ToIDispatch()
	defer shortcut.Release()

	oleutil.MustCallMethod(shortcut, "Item", "TargetPath", targetPath)
	workDir := filepath.Dir(targetPath)
	oleutil.MustCallMethod(shortcut, "Item", "WorkingDirectory", workDir)
	oleutil.MustCallMethod(shortcut, "Item", "WindowStyle", 1)
	oleutil.MustCallMethod(shortcut, "Save")

	return nil
}

// 确保 strings 被引用 (避免 import 但未使用)
var _ = strings.ToLower
