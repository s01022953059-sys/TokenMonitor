//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows/registry"
	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const autoStartRegKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
const autoStartValueName = "TokenMonitor"
const startupApprovedKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

// 防止 enable/disable 并发调用导致 COM 状态混乱
var autoStartMu sync.Mutex

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
func enableAutoStart() {
	autoStartMu.Lock()
	defer autoStartMu.Unlock()

	// panic recovery, 防止 COM 调用崩溃杀掉整个进程
	defer func() {
		if r := recover(); r != nil {
			guiLog("enableAutoStart PANIC: %v", r)
		}
	}()

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

	// 2. 写 StartupApproved
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

	// 3. 创建 Startup 文件夹快捷方式
	startupDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	lnkPath := filepath.Join(startupDir, "TokenMonitor.lnk")
	if err := createShortcut(lnkPath, exePath); err != nil {
		guiLog("enableAutoStart: 创建快捷方式失败: %v", err)
	} else {
		guiLog("enableAutoStart: Startup 快捷方式创建成功: %s", lnkPath)
	}

	// 4. v1.4.11: 创建 Task Scheduler 任务 (Win11 最可靠, 不受 SmartScreen 拦截)
	// schtasks /create /tn "TokenMonitor" /tr "\"exe_path\"" /sc ONLOGON /rl LIMITED /f
	taskCmd := exec.Command("schtasks", "/create",
		"/tn", "TokenMonitor",
		"/tr", `"`+exePath+`"`,
		"/sc", "ONLOGON",
		"/rl", "LIMITED",
		"/f")
	if output, err := taskCmd.CombinedOutput(); err != nil {
		guiLog("enableAutoStart: Task Scheduler 创建失败: %v, output=%s", err, string(output))
	} else {
		guiLog("enableAutoStart: Task Scheduler 创建成功")
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
	autoStartMu.Lock()
	defer autoStartMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			guiLog("disableAutoStart PANIC: %v", r)
		}
	}()

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

	// 4. 删 Task Scheduler 任务
	delTask := exec.Command("schtasks", "/delete", "/tn", "TokenMonitor", "/f")
	if output, err := delTask.CombinedOutput(); err != nil {
		guiLog("disableAutoStart: Task Scheduler 删除失败 (可能不存在): %v", err)
	} else {
		guiLog("disableAutoStart: Task Scheduler 已删除, output=%s", string(output))
	}

	fmt.Printf("[autostart] 已取消开机自启\n")
}

// createShortcut 创建 .lnk 快捷方式
// v1.4.07: 修复快速切换崩溃
// - 不用 MustCallMethod (会 panic), 改用 CallMethod + 错误检查
// - COM 初始化用 RPC_E_CHANGED_MODE 容忍 (已初始化时不报错)
// - 不调 CoUninitialize (避免重复反初始化导致后续 COM 调用崩溃)
func createShortcut(lnkPath, targetPath string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("COM panic: %v", r)
		}
	}()

	// CoInitializeEx 容忍已初始化的情况 (返回 S_FALSE 不算错误)
	// 不调 CoUninitialize — 避免快速切换时重复反初始化崩溃
	hr := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	// S_OK(0) = 首次初始化成功, S_FALSE(1) = 已初始化, RPC_E_CHANGED_MODE = 线程模式冲突
	_ = hr // 忽略返回值, 不影响后续调用

	clsid, err := ole.CLSIDFromProgID("WScript.Shell")
	if err != nil {
		return fmt.Errorf("CLSIDFromProgID: %w", err)
	}
	unknown, err := ole.CreateInstance(clsid, nil)
	if err != nil {
		return fmt.Errorf("CreateInstance: %w", err)
	}
	defer unknown.Release()

	dispatch, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("QueryInterface: %w", err)
	}
	defer dispatch.Release()

	// 用 CallMethod (返回 error) 而不是 MustCallMethod (会 panic)
	ds, err := oleutil.CallMethod(dispatch, "CreateShortcut", lnkPath)
	if err != nil {
		return fmt.Errorf("CreateShortcut: %w", err)
	}
	shortcut := ds.ToIDispatch()
	defer shortcut.Release()

	if _, err := oleutil.CallMethod(shortcut, "Item", "TargetPath", targetPath); err != nil {
		return fmt.Errorf("Item TargetPath: %w", err)
	}
	workDir := filepath.Dir(targetPath)
	if _, err := oleutil.CallMethod(shortcut, "Item", "WorkingDirectory", workDir); err != nil {
		return fmt.Errorf("Item WorkingDirectory: %w", err)
	}
	if _, err := oleutil.CallMethod(shortcut, "Item", "WindowStyle", 1); err != nil {
		return fmt.Errorf("Item WindowStyle: %w", err)
	}
	if _, err := oleutil.CallMethod(shortcut, "Save"); err != nil {
		return fmt.Errorf("Save: %w", err)
	}

	return nil
}
