//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

//go:embed payload/TokenMonitor.exe
var appPayload []byte

var version = "dev"

const (
	appName       = "Token Monitor"
	appExeName    = "TokenMonitor.exe"
	uninstallName = "Uninstall.exe"
)

func main() {
	if hasArg("--uninstall") {
		uninstall()
		return
	}
	if err := install(); err != nil {
		messageBox("安装失败", err.Error(), 0x10)
		os.Exit(1)
	}
	if !hasArg("--update") {
		messageBox("安装完成", "Token Monitor 已安装并启动。", 0x40)
	}
}

func install() error {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return fmt.Errorf("无法读取 LOCALAPPDATA")
	}
	installDir := filepath.Join(localAppData, "Programs", appName)
	appPath := filepath.Join(installDir, appExeName)
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return fmt.Errorf("创建安装目录失败: %w", err)
	}

	stopApp()
	tmpPath := appPath + ".new"
	if err := os.WriteFile(tmpPath, appPayload, 0755); err != nil {
		return fmt.Errorf("写入程序失败: %w", err)
	}
	_ = os.Remove(appPath)
	if err := os.Rename(tmpPath, appPath); err != nil {
		return fmt.Errorf("替换程序失败: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("读取安装器路径失败: %w", err)
	}
	uninstaller := filepath.Join(installDir, uninstallName)
	if err := copyFile(self, uninstaller); err != nil {
		return fmt.Errorf("创建卸载程序失败: %w", err)
	}
	if err := createStartMenuShortcut(appPath); err != nil {
		return fmt.Errorf("创建开始菜单快捷方式失败: %w", err)
	}
	if err := registerUninstaller(installDir, appPath, uninstaller); err != nil {
		return fmt.Errorf("注册卸载程序失败: %w", err)
	}

	cmd := exec.Command(appPath)
	cmd.SysProcAttr = hiddenProcess()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动应用失败: %w", err)
	}
	return nil
}

func uninstall() {
	localAppData := os.Getenv("LOCALAPPDATA")
	installDir := filepath.Join(localAppData, "Programs", appName)
	stopApp()
	removeStartMenuShortcut()
	runHidden("reg.exe", "delete", "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\TokenMonitor", "/f")

	script := fmt.Sprintf("ping 127.0.0.1 -n 3 >nul & rmdir /s /q \"%s\"", installDir)
	cmd := exec.Command("cmd.exe", "/C", script)
	cmd.SysProcAttr = hiddenProcess()
	_ = cmd.Start()
	messageBox("卸载完成", "Token Monitor 已从此电脑移除。", 0x40)
}

func stopApp() {
	runHidden("taskkill.exe", "/F", "/IM", appExeName)
	time.Sleep(400 * time.Millisecond)
}

func registerUninstaller(installDir, appPath, uninstaller string) error {
	key := "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\TokenMonitor"
	values := [][]string{
		{"add", key, "/v", "DisplayName", "/t", "REG_SZ", "/d", appName, "/f"},
		{"add", key, "/v", "DisplayVersion", "/t", "REG_SZ", "/d", version, "/f"},
		{"add", key, "/v", "Publisher", "/t", "REG_SZ", "/d", "Token Monitor", "/f"},
		{"add", key, "/v", "InstallLocation", "/t", "REG_SZ", "/d", installDir, "/f"},
		{"add", key, "/v", "DisplayIcon", "/t", "REG_SZ", "/d", appPath, "/f"},
		{"add", key, "/v", "UninstallString", "/t", "REG_SZ", "/d", "\"" + uninstaller + "\" --uninstall", "/f"},
		{"add", key, "/v", "NoModify", "/t", "REG_DWORD", "/d", "1", "/f"},
		{"add", key, "/v", "NoRepair", "/t", "REG_DWORD", "/d", "1", "/f"},
	}
	for _, args := range values {
		if err := runHidden("reg.exe", args...); err != nil {
			return err
		}
	}
	return nil
}

func createStartMenuShortcut(appPath string) error {
	programs := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs")
	shortcut := filepath.Join(programs, appName+".lnk")
	script := fmt.Sprintf(
		"$s=(New-Object -COM WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.IconLocation='%s,0';$s.Save()",
		psQuote(shortcut), psQuote(appPath), psQuote(filepath.Dir(appPath)), psQuote(appPath),
	)
	return runHidden("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
}

func removeStartMenuShortcut() {
	shortcut := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", appName+".lnk")
	_ = os.Remove(shortcut)
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0755)
}

func runHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = hiddenProcess()
	return cmd.Run()
}

func hiddenProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func hasArg(want string) bool {
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, want) {
			return true
		}
	}
	return false
}

func psQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func messageBox(title, message string, flags uintptr) {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	messagePtr, _ := syscall.UTF16PtrFromString(message)
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(messagePtr)), uintptr(unsafe.Pointer(titlePtr)), flags)
}
