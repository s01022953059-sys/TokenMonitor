import Cocoa
import WebKit

class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var window: NSWindow!
    var webView: WKWebView!
    var statusItem: NSStatusItem!

    func applicationDidFinishLaunching(_ notification: Notification) {
        // 尝试写入开机自启动配置
        setupAutostart()
        
        // 尝试自动启动本地 python 统计服务
        startLocalServer()
        
        // 初始化右上角系统状态栏项
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.autosaveName = "TokenMonitorStatusItem"
        if let button = statusItem.button {
            button.title = "🔥--"
            
            // 创建状态栏下拉菜单
            let menu = NSMenu()
            menu.addItem(NSMenuItem(title: "显示大屏", action: #selector(showMainWindow), keyEquivalent: "s"))
            menu.addItem(NSMenuItem.separator())
            menu.addItem(NSMenuItem(title: "退出应用", action: #selector(quitApp), keyEquivalent: "q"))
            statusItem.menu = menu
        }
        
        // 启动状态栏 Token 实时刷新定时器 (每 5 秒一次)
        startTokenUpdateTimer()
        
        let screenRect = NSScreen.main?.frame ?? NSRect(x: 0, y: 0, width: 960, height: 600)
        let windowWidth: CGFloat = 1000
        let windowHeight: CGFloat = 720
        let rect = NSRect(
            x: (screenRect.width - windowWidth) / 2,
            y: (screenRect.height - windowHeight) / 2,
            width: windowWidth,
            height: windowHeight
        )
        
        window = NSWindow(
            contentRect: rect,
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.title = "AI Token Monitor"
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.isMovableByWindowBackground = true
        window.delegate = self
        
        // 窗口暗黑色背景色，对齐网页主题
        window.backgroundColor = NSColor(red: 0.02, green: 0.02, blue: 0.04, alpha: 1.0)
        
        let config = WKWebViewConfiguration()
        // 允许本地 file:// 页面发起对 localhost 的跨域 fetch 请求
        config.preferences.setValue(true, forKey: "allowFileAccessFromFileURLs")
        
        webView = WKWebView(frame: .zero, configuration: config)
        webView.translatesAutoresizingMaskIntoConstraints = false
        
        window.contentView?.addSubview(webView)
        
        // 使用 AutoLayout 约束，顶部预留出 28px 的原生标题栏拖拽区域
        NSLayoutConstraint.activate([
            webView.leadingAnchor.constraint(equalTo: window.contentView!.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: window.contentView!.trailingAnchor),
            webView.bottomAnchor.constraint(equalTo: window.contentView!.bottomAnchor),
            webView.topAnchor.constraint(equalTo: window.contentView!.topAnchor, constant: 28)
        ])
        
        // 立即展示窗口，完全不卡主线程
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        
        // 直接从 .app/Contents/Resources 加载大屏 HTML，不等待 Python 服务就绪
        // 这使得 UI 在 <200ms 内完整呈现，数据由 JS 侧异步重试填充
        let resourceDir = URL(fileURLWithPath: Bundle.main.resourcePath ?? ".")
        let htmlFile = resourceDir.appendingPathComponent("index.html")
        webView.loadFileURL(htmlFile, allowingReadAccessTo: resourceDir)
    }

    func startLocalServer() {
        let resourceDir = Bundle.main.resourcePath ?? "."
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/bash")
        process.arguments = ["\(resourceDir)/start.sh", "start"]
        
        // 静默运行，不污染当前日志
        let devNull = FileHandle.nullDevice
        process.standardOutput = devNull
        process.standardError = devNull
        
        try? process.run()
    }

    @objc func showMainWindow() {
        NSApp.setActivationPolicy(.regular)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
    
    @objc func quitApp() {
        NSApplication.shared.terminate(nil)
    }
    
    func startTokenUpdateTimer() {
        Timer.scheduledTimer(withTimeInterval: 5.0, repeats: true) { [weak self] _ in
            self?.updateStatusBarToken()
        }
    }
    
    func updateStatusBarToken() {
        guard let url = URL(string: "http://127.0.0.1:15723/api/usage") else { return }
        let task = URLSession.shared.dataTask(with: url) { [weak self] data, response, error in
            guard let data = data, error == nil else { return }
            do {
                if let json = try JSONSerialization.jsonObject(with: data, options: []) as? [String: Any],
                   let summary = json["summary"] as? [String: Any],
                   let totalTokens = summary["total_tokens"] as? Int {
                    
                    let formatted = self?.formatTokensForStatusBar(totalTokens) ?? "--"
                    DispatchQueue.main.async {
                        if let button = self?.statusItem.button {
                            button.title = "🔥\(formatted)"
                        }
                    }
                }
            } catch {
                // 静默
            }
        }
        task.resume()
    }
    
    func formatTokensForStatusBar(_ num: Int) -> String {
        if num >= 100000000 {
            return String(format: "%.1f亿", Double(num) / 100000000.0)
        }
        if num >= 1000000 {
            return String(format: "%.1fM", Double(num) / 1000000.0)
        }
        if num >= 1000 {
            return String(format: "%.1fK", Double(num) / 1000.0)
        }
        return "\(num)"
    }

    // 拦截关闭窗口事件：将窗口隐藏而不是销毁
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        window.orderOut(nil)
        NSApp.setActivationPolicy(.accessory)
        return false
    }

    // 当所有窗口关闭后，APP 进程不退出，保持状态栏存活
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        return false
    }

    // 点击 Dock 栏应用图标时，唤醒显示主大屏
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return true
    }
    
    // 写入并配置开机自启动 LaunchAgent plist
    func setupAutostart() {
        let appBundlePath = Bundle.main.bundlePath
        let plistLabel = "com.baggio.tokenmonitor"
        let fileManager = FileManager.default
        
        let homeDir = NSHomeDirectory()
        let launchAgentsDir = URL(fileURLWithPath: homeDir).appendingPathComponent("Library/LaunchAgents")
        let plistURL = launchAgentsDir.appendingPathComponent("\(plistLabel).plist")
        
        // 确保 Library/LaunchAgents 目录存在
        try? fileManager.createDirectory(at: launchAgentsDir, withIntermediateDirectories: true, attributes: nil)
        
        // 构造 plist 数据，通过 open -a 优雅拉起 App
        let plistContent: [String: Any] = [
            "Label": plistLabel,
            "ProgramArguments": [
                "/usr/bin/open",
                "-a",
                appBundlePath
            ],
            "RunAtLoad": true
        ]
        
        if let plistData = try? PropertyListSerialization.data(fromPropertyList: plistContent, format: .xml, options: 0) {
            do {
                try plistData.write(to: plistURL)
            } catch {
                // 静默
            }
        }
    }
    
    // 炫酷极简磨砂脉冲加载骨架屏
    let loadingHTML = """
    <!DOCTYPE html>
    <html lang="zh-CN">
    <head>
        <meta charset="UTF-8">
        <style>
            body {
                background-color: #050609;
                color: #f3f4f6;
                font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                margin: 0;
                padding: 40px;
                display: flex;
                flex-direction: column;
                height: 85vh;
                justify-content: center;
                align-items: center;
                overflow: hidden;
            }
            .header-bar {
                width: 300px;
                height: 32px;
                background: rgba(255, 255, 255, 0.03);
                border-radius: 8px;
                margin-bottom: 40px;
                animation: pulse 1.5s infinite ease-in-out;
            }
            .container {
                display: flex;
                gap: 24px;
                width: 100%;
                max-width: 900px;
                height: 350px;
            }
            .card {
                flex: 1;
                background: rgba(9, 11, 16, 0.6);
                border: 1px solid rgba(255, 255, 255, 0.05);
                border-radius: 20px;
                padding: 30px;
                display: flex;
                flex-direction: column;
                justify-content: space-between;
                position: relative;
                overflow: hidden;
            }
            .card::after {
                content: "";
                position: absolute;
                top: 0; left: 0; right: 0; bottom: 0;
                background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.015), transparent);
                transform: translateX(-100%);
                animation: shimmer 2.5s infinite;
            }
            .title-placeholder {
                height: 20px;
                width: 100px;
                background: rgba(255, 255, 255, 0.04);
                border-radius: 6px;
                animation: pulse 1.5s infinite ease-in-out;
            }
            .num-placeholder {
                height: 54px;
                width: 240px;
                background: rgba(255, 255, 255, 0.03);
                border-radius: 10px;
                animation: pulse 1.5s infinite ease-in-out;
            }
            .desc-placeholder {
                height: 14px;
                width: 180px;
                background: rgba(255, 255, 255, 0.02);
                border-radius: 4px;
                animation: pulse 1.5s infinite ease-in-out;
            }
            .status-panel {
                margin-top: 50px;
                font-size: 13px;
                color: #6e7681;
                letter-spacing: 0.5px;
                display: flex;
                align-items: center;
                gap: 10px;
                background: rgba(255, 255, 255, 0.02);
                padding: 10px 20px;
                border-radius: 30px;
                border: 1px solid rgba(255, 255, 255, 0.03);
            }
            .spinner {
                width: 14px;
                height: 14px;
                border: 2px solid rgba(168, 85, 247, 0.1);
                border-top-color: #a855f7;
                border-radius: 50%;
                animation: spin 0.8s linear infinite;
            }
            @keyframes pulse {
                0%, 100% { opacity: 0.6; }
                50% { opacity: 0.3; }
            }
            @keyframes shimmer {
                100% { transform: translateX(100%); }
            }
            @keyframes spin {
                to { transform: rotate(360deg); }
            }
        </style>
    </head>
    <body>
        <div class="header-bar"></div>
        <div class="container">
            <div class="card">
                <div class="title-placeholder"></div>
                <div class="num-placeholder"></div>
                <div class="desc-placeholder"></div>
            </div>
            <div class="card">
                <div class="title-placeholder"></div>
                <div class="num-placeholder"></div>
                <div class="desc-placeholder"></div>
            </div>
        </div>
        <div class="status-panel">
            <div class="spinner"></div>
            <span>正在建立与本地 Token 数据服务的安全连接...</span>
        </div>
    </body>
    </html>
    """
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
