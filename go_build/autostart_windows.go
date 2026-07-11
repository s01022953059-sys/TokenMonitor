//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const autoStartRegKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
const autoStartValueName = "TokenMonitor"
const startupApprovedKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

var autoStartMu sync.Mutex

func readAutoStartCommand() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	value, _, err := k.GetStringValue(autoStartValueName)
	return value, err
}

func startupApprovedDisabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	value, _, err := k.GetBinaryValue(autoStartValueName)
	return err == nil && len(value) > 0 && value[0] == 0x03
}

// isAutoStartEnabled 只认可当前 EXE 的规范 Run 命令，并尊重任务管理器里的禁用状态。
func isAutoStartEnabled() bool {
	exePath, err := os.Executable()
	if err != nil {
		return false
	}
	command, err := readAutoStartCommand()
	if err != nil || !isExpectedAutoStartCommand(command, exePath) {
		return false
	}
	return !startupApprovedDisabled()
}

// migrateLegacyAutoStart 将旧版只有 EXE 路径的 Run 项迁移为单入口后台启动。
func migrateLegacyAutoStart() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	command, err := readAutoStartCommand()
	if err != nil || !isLegacyAutoStartCommand(command, exePath) {
		return
	}
	guiLog("migrateLegacyAutoStart: 检测到旧自启配置, 开始迁移")
	if err := enableAutoStart(); err != nil {
		guiLog("migrateLegacyAutoStart: 迁移失败: %v", err)
	}
}

// enableAutoStart 使用 HKCU Run 作为唯一自启入口，不需要管理员权限。
func enableAutoStart() error {
	autoStartMu.Lock()
	defer autoStartMu.Unlock()

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("读取 EXE 路径失败: %w", err)
	}
	guiLog("enableAutoStart: exePath=%s", exePath)

	// 清理旧版额外入口，避免登录时同时启动多个实例。
	cleanupLegacyAutoStartArtifacts()
	removeStartupApprovedValue()

	k, _, err := registry.CreateKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("打开 Run 注册表失败: %w", err)
	}
	defer k.Close()

	command := buildAutoStartCommand(exePath)
	if err := k.SetStringValue(autoStartValueName, command); err != nil {
		return fmt.Errorf("写入 Run 注册表失败: %w", err)
	}
	actual, _, err := k.GetStringValue(autoStartValueName)
	if err != nil || !isExpectedAutoStartCommand(actual, exePath) {
		return fmt.Errorf("Run 注册表写入后校验失败")
	}
	guiLog("enableAutoStart: 已写入并验证 %s", command)
	return nil
}

func disableAutoStart() error {
	autoStartMu.Lock()
	defer autoStartMu.Unlock()

	var firstErr error
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE)
	if err == nil {
		if err := k.DeleteValue(autoStartValueName); err != nil && err != registry.ErrNotExist {
			firstErr = err
		}
		k.Close()
	}
	removeStartupApprovedValue()
	cleanupLegacyAutoStartArtifacts()
	guiLog("disableAutoStart: 已清理全部自启入口")
	return firstErr
}

func removeStartupApprovedValue() {
	k, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.DeleteValue(autoStartValueName)
}

func cleanupLegacyAutoStartArtifacts() {
	if appData := os.Getenv("APPDATA"); appData != "" {
		shortcut := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "TokenMonitor.lnk")
		if err := os.Remove(shortcut); err == nil {
			guiLog("cleanupLegacyAutoStart: 已删除旧 Startup 快捷方式")
		}
	}

	cmd := exec.Command("schtasks", "/delete", "/tn", "TokenMonitor", "/f")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err == nil {
		guiLog("cleanupLegacyAutoStart: 已删除旧计划任务: %s", string(output))
	}
}
