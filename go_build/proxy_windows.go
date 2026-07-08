//go:build windows

package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

// newProxyHTTPClient 创建一个走 Windows 系统代理 (VPN) 的 HTTP client
// Go 默认 http.Client 只读 HTTP_PROXY/HTTPS_PROXY 环境变量, 不读 Windows 注册表
// 用户在内网+VPN 环境, 系统代理设在注册表里, 必须手动读
func newProxyHTTPClient(timeoutSec int) *http.Client {
	proxyURL := getSystemProxy()
	transport := &http.Transport{}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	} else {
		// 没有系统代理, 回退到环境变量 (http.ProxyFromEnvironment)
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutSec) * time.Second,
	}
}

// getSystemProxy 从注册表读 Windows 系统代理设置
// HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings
func getSystemProxy() string {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()

	// ProxyEnable: 1=启用代理, 0=禁用
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable == 0 {
		return ""
	}

	// ProxyServer: 代理地址 (如 "127.0.0.1:7890" 或 "http=127.0.0.1:7890;https=127.0.0.1:7890")
	proxyServer, _, err := k.GetStringValue("ProxyServer")
	if err != nil || proxyServer == "" {
		return ""
	}

	// 处理 "http=xxx;https=xxx" 格式, 取 https 的 (GitCode API 是 https)
	if strings.Contains(proxyServer, ";") {
		for _, part := range strings.Split(proxyServer, ";") {
			if strings.HasPrefix(strings.ToLower(part), "https=") {
				proxyServer = strings.TrimPrefix(part, "https=")
				break
			}
			if strings.HasPrefix(strings.ToLower(part), "http=") {
				proxyServer = strings.TrimPrefix(part, "http=")
				// 不 break, 继续找 https=
			}
		}
	}

	// 确保 URL 格式正确
	if !strings.HasPrefix(proxyServer, "http") {
		proxyServer = "http://" + proxyServer
	}

	return proxyServer
}
