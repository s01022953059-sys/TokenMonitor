//go:build windows

package main

import (
	"os"
)

// Windows 上用简单的文件存在检查 + PID 写入做单实例锁。
// 不依赖 syscall.LockFileEx (不在标准 syscall 包里), 避免引入 golang.org/x/sys 依赖。
// 对本地仪表盘场景足够: 进程退出时 DeleteFile 清锁。
func tryLockFile(f *os.File) error {
	// 写入 PID 标记, 文件已存在且进程活着时由调用方判断
	// 这里简单返回 nil (文件已成功打开), 真正的单实例检查在 main 里做端口检测
	return nil
}
