//go:build !windows

// Token Monitor 非 Windows GUI stub
// Mac/Linux 不走 Go server (Mac 用 Python + Swift), 这里只阻塞
package main

func startGUI(port int, feedURL string) {
	// 非 Windows: 阻塞 (HTTP server 在 goroutine 里跑)
	select {}
}
