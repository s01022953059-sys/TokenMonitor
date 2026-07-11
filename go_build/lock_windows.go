//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

var singletonOverlapped windows.Overlapped

// Windows 必须持有真实的文件锁。仅写 PID 存在竞态，自启迁移期间尤其容易
// 同时拉起多个实例并各自占用不同端口。
func tryLockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&singletonOverlapped,
	)
}
