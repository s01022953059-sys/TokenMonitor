//go:build !windows

package main

import (
	"net/http"
	"time"
)

// 非 Windows: 用标准 http.Client (Mac 走 Python server, 不需要读注册表代理)
func newProxyHTTPClient(timeoutSec int) *http.Client {
	return &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
}
