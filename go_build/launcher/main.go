package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const CREATE_NO_WINDOW = 0x08000000

func main() {
	exePath, _ := os.Executable()
	dir := filepath.Dir(exePath)
	target := filepath.Join(dir, "TokenMonitor.exe")

	cmd := exec.Command(target)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: CREATE_NO_WINDOW,
	}
	cmd.Start()
}
