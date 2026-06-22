import Cocoa
import WebKit

class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var window: NSWindow!
    var webView: WKWebView!
    var statusItem: NSStatusItem!
    var updateCheckInProgress = false
    // 持有 server 进程引用, 防止 ARC 回收 Process 对象时杀掉子进程。
    var serverProcess: Process?

    // 单一来源: API 端口从 Info.plist 的 TokenMonitorAPIPort 读取，
    // 启动 server 时透传给 start.sh, 并注入到 WebView 全局。
    private var fallbackPlistDict: NSDictionary? {
        let path = "/Applications/Token Monitor.app/Contents/Info.plist"
        return NSDictionary(contentsOfFile: path)
    }

    private var apiPort: Int {
        if let p = Bundle.main.object(forInfoDictionaryKey: "TokenMonitorAPIPort") as? String,
           let n = Int(p), n > 0, n < 65536 {
            return n
        }
        if let p = fallbackPlistDict?["TokenMonitorAPIPort"] as? String,
           let n = Int(p), n > 0, n < 65536 {
            return n
        }
        return 15723
    }

    private var apiBaseURL: String {
        return "http://127.0.0.1:\(apiPort)"
    }

    @objc func applicationDidFinishLaunching(_ notification: Notification) {
        // 单实例闸门: 同一个 bundleId (com.baggio.tokenmonitor) 只允许一个 GUI 实例.
        // 第二次启动时把已有实例抢到前台, 自己直接退出.
        let singleOK = enforceSingleInstance()
        if !singleOK {
            NSApplication.shared.terminate(nil)
            return
        }
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
            menu.addItem(NSMenuItem(title: "检查更新...", action: #selector(checkForUpdatesFromMenu), keyEquivalent: "u"))
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
        // Swift 在 documentStart 注入 API 根地址，避免 JS 端硬编码端口
        let userContentController = WKUserContentController()
        let injectionScript = "window.__API_BASE__ = '\(apiBaseURL)';"
        let apiBaseScript = WKUserScript(
            source: injectionScript,
            injectionTime: .atDocumentStart,
            forMainFrameOnly: true
        )
        userContentController.addUserScript(apiBaseScript)
        config.userContentController = userContentController
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
        // 符号链接启动时 Bundle.main.resourcePath 可能不指向 bundle, 加 fallback。
        var resourcePath = Bundle.main.resourcePath ?? ""
        if !FileManager.default.fileExists(atPath: resourcePath + "/index.html") {
            let fallback = "/Applications/Token Monitor.app/Contents/Resources"
            if FileManager.default.fileExists(atPath: fallback + "/index.html") {
                resourcePath = fallback
            }
        }
        let resourceDir = URL(fileURLWithPath: resourcePath)
        let htmlFile = resourceDir.appendingPathComponent("index.html")
        webView.loadFileURL(htmlFile, allowingReadAccessTo: resourceDir)

        checkForUpdates(silent: true)
    }

    func startLocalServer() {
        // Bundle.main.resourcePath 在符号链接启动方式下可能不指向 bundle 内的 Resources,
        // 用 bundlePath 推导, 并加固定 fallback 确保能找到 server.py。
        var resourceDir = Bundle.main.resourcePath ?? ""
        let serverCheck = resourceDir + "/server.py"
        if !FileManager.default.fileExists(atPath: serverCheck) {
            // fallback: 从已知 app bundle 路径找 Resources
            let fallback = "/Applications/Token Monitor.app/Contents/Resources"
            if FileManager.default.fileExists(atPath: fallback + "/server.py") {
                resourceDir = fallback
            }
        }
        var cmd = "/usr/bin/python3 \"\(resourceDir)/server.py\" --port \(apiPort)"
        if let feedURLString = configuredUpdateFeedURL()?.absoluteString,
           !feedURLString.isEmpty {
            cmd += " --update-feed-url \"\(feedURLString)\""
        }
        cmd += " &"

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/bin/sh")
        proc.arguments = ["-c", cmd]
        let devNull = FileHandle.nullDevice
        proc.standardOutput = devNull
        proc.standardError = devNull

        do {
            try proc.run()
            self.serverProcess = proc
        } catch {
            let msg = "startLocalServer error: \(error)\n"
            try? msg.write(toFile: "/tmp/tm_swift_error.log", atomically: true, encoding: .utf8)
        }
    }

    @objc func showMainWindow() {
        NSApp.setActivationPolicy(.regular)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
    
    @objc func quitApp() {
        NSApplication.shared.terminate(nil)
    }

    @objc func checkForUpdatesFromMenu() {
        checkForUpdates(silent: false)
    }
    
    func startTokenUpdateTimer() {
        Timer.scheduledTimer(withTimeInterval: 5.0, repeats: true) { [weak self] _ in
            self?.updateStatusBarToken()
        }
    }
    
    func updateStatusBarToken() {
        // 端口必须跟着 Info.plist 的 TokenMonitorAPIPort 走,
        // 否则改端口后状态栏永远拿到 "🔥--" 而主窗口正常, 用户无法理解。
        guard let url = URL(string: apiBaseURL + "/api/usage") else { return }
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
    
    // 单实例检查: 若发现同 bundleId 的其他进程在跑, 把它抢到前台后返回 false
    // (调用者应当 NSApplication.shared.terminate 退出自己). 找不到就返回 true.
    private func enforceSingleInstance() -> Bool {
        guard let bundleId = Bundle.main.bundleIdentifier else {
            // 没有 bundleId 时不做拦截, 避免开发期调试卡死
            return true
        }
        let currentPID = ProcessInfo.processInfo.processIdentifier
        let others = NSRunningApplication.runningApplications(withBundleIdentifier: bundleId)
            .filter { $0.processIdentifier != currentPID }
        guard let existing = others.first else { return true }
        // 已有实例: 抢到前台
        existing.activate(options: [.activateAllWindows, .activateIgnoringOtherApps])
        return false
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

    func configuredUpdateFeedURL() -> URL? {
        var raw = Bundle.main.object(forInfoDictionaryKey: "TokenMonitorUpdateFeedURL") as? String
        // 符号链接启动时 Bundle.main 可能不指向 app bundle, 从已知路径读 Info.plist
        if raw == nil {
            let fallbackPlist = "/Applications/Token Monitor.app/Contents/Info.plist"
            if let dict = NSDictionary(contentsOfFile: fallbackPlist) {
                raw = dict["TokenMonitorUpdateFeedURL"] as? String
            }
        }
        guard let feedURL = raw else { return nil }
        let trimmed = feedURL.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty || trimmed.hasPrefix("https://example.com/") {
            return nil
        }
        return URL(string: trimmed)
    }

    func checkForUpdates(silent: Bool) {
        guard !updateCheckInProgress else { return }
        guard let feedURL = configuredUpdateFeedURL() else {
            if !silent {
                showAlert(
                    title: "未配置更新源",
                    message: "请先在 Info.plist 的 TokenMonitorUpdateFeedURL 中填入更新 JSON 或 GitHub Releases latest API 地址。"
                )
            }
            return
        }

        updateCheckInProgress = true
        var request = URLRequest(url: feedURL, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 8)
        request.setValue("Token Monitor", forHTTPHeaderField: "User-Agent")

        URLSession.shared.dataTask(with: request) { [weak self] data, response, error in
            guard let self = self else { return }
            defer {
                DispatchQueue.main.async {
                    self.updateCheckInProgress = false
                }
            }

            if let error = error {
                if !silent {
                    DispatchQueue.main.async {
                        self.showAlert(title: "检查更新失败", message: error.localizedDescription)
                    }
                }
                return
            }

            guard let httpResponse = response as? HTTPURLResponse,
                  200..<300 ~= httpResponse.statusCode,
                  let data = data else {
                if !silent {
                    DispatchQueue.main.async {
                        self.showAlert(title: "检查更新失败", message: "更新源没有返回有效响应。")
                    }
                }
                return
            }

            guard let update = self.parseUpdateInfo(from: data, fallbackURL: feedURL) else {
                if !silent {
                    DispatchQueue.main.async {
                        self.showAlert(title: "检查更新失败", message: "更新源 JSON 格式不符合预期。")
                    }
                }
                return
            }

            let currentVersion = self.currentAppVersion()
            let isNewer = self.compareVersions(update.version, currentVersion) == .orderedDescending

            DispatchQueue.main.async {
                if isNewer {
                    self.showUpdateAvailable(update: update, currentVersion: currentVersion)
                } else if !silent {
                    self.showAlert(title: "已是最新版本", message: "当前版本 \(currentVersion) 已是最新。")
                }
            }
        }.resume()
    }

    struct UpdateInfo {
        let version: String
        let title: String
        let notes: String
        let downloadURL: URL
    }

    func parseUpdateInfo(from data: Data, fallbackURL: URL) -> UpdateInfo? {
        guard let json = try? JSONSerialization.jsonObject(with: data, options: []) as? [String: Any] else {
            return nil
        }

        let rawVersion = (json["version"] as? String) ?? (json["tag_name"] as? String) ?? ""
        let version = rawVersion.trimmingCharacters(in: CharacterSet(charactersIn: "vV "))
        guard !version.isEmpty else { return nil }

        let title = (json["title"] as? String) ?? (json["name"] as? String) ?? "Token Monitor \(version)"
        let notes = (json["notes"] as? String) ?? (json["body"] as? String) ?? ""

        var downloadString = (json["download_url"] as? String) ?? (json["downloadUrl"] as? String) ?? (json["html_url"] as? String)
        let assetList = (json["assets"] as? [[String: Any]]) ?? (json["files"] as? [[String: Any]])
        if downloadString == nil, let assets = assetList {
            let preferredAsset = assets.first { asset in
                let name = (asset["name"] as? String ?? "").lowercased()
                return name.hasSuffix(".dmg") || name.hasSuffix(".zip")
            } ?? assets.first
            downloadString =
                (preferredAsset?["browser_download_url"] as? String) ??
                (preferredAsset?["download_url"] as? String) ??
                (preferredAsset?["downloadUrl"] as? String) ??
                (preferredAsset?["url"] as? String) ??
                (preferredAsset?["html_url"] as? String)
        }

        guard let rawDownloadURL = downloadString,
              let downloadURL = URL(string: rawDownloadURL, relativeTo: fallbackURL)?.absoluteURL else {
            return nil
        }

        return UpdateInfo(version: version, title: title, notes: notes, downloadURL: downloadURL)
    }

    func currentAppVersion() -> String {
        if let v = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String {
            return v
        }
        return (fallbackPlistDict?["CFBundleShortVersionString"] as? String) ?? "0"
    }

    func compareVersions(_ lhs: String, _ rhs: String) -> ComparisonResult {
        let lhsParts = lhs.split(separator: ".").map { Int($0) ?? 0 }
        let rhsParts = rhs.split(separator: ".").map { Int($0) ?? 0 }
        let count = max(lhsParts.count, rhsParts.count)

        for i in 0..<count {
            let left = i < lhsParts.count ? lhsParts[i] : 0
            let right = i < rhsParts.count ? rhsParts[i] : 0
            if left > right { return .orderedDescending }
            if left < right { return .orderedAscending }
        }
        return .orderedSame
    }

    func showUpdateAvailable(update: UpdateInfo, currentVersion: String) {
        let alert = NSAlert()
        alert.messageText = "发现新版本 \(update.version)"
        let notesSummary = summarizedReleaseNotes(update.notes)
        let noteText = notesSummary.isEmpty ? "" : "\n\n更新说明：\n\(notesSummary)"
        alert.informativeText = "当前版本：\(currentVersion)\n最新版本：\(update.version)\(noteText)"
        alert.addButton(withTitle: "下载更新")
        alert.addButton(withTitle: "查看说明")
        alert.addButton(withTitle: "稍后")
        alert.addButton(withTitle: "关闭")
        // ESC 直接走关闭按钮,避免弹窗卡住主线程时整个 app 没法退。
        if let closeButton = alert.buttons.last {
            closeButton.keyEquivalent = "\u{1b}"
        }
        switch alert.runModal() {
        case .alertFirstButtonReturn:
            NSWorkspace.shared.open(update.downloadURL)
        case .alertSecondButtonReturn:
            NSWorkspace.shared.open(update.downloadURL)
        case .alertThirdButtonReturn:
            break
        default:
            break
        }
    }

    func summarizedReleaseNotes(_ notes: String, limit: Int = 280) -> String {
        let cleaned = notes
            .replacingOccurrences(of: "\r\n", with: "\n")
            .split(separator: "\n")
            .map { line in
                line.replacingOccurrences(of: #"!\[[^\]]*\]\([^)]+\)"#, with: "", options: .regularExpression)
                    .replacingOccurrences(of: #"<[^>]+>"#, with: "", options: .regularExpression)
                    .trimmingCharacters(in: .whitespacesAndNewlines)
            }
            .filter { !$0.isEmpty }
            .prefix(3)
            .joined(separator: "\n")

        guard cleaned.count > limit else { return cleaned }
        let end = cleaned.index(cleaned.startIndex, offsetBy: limit)
        return String(cleaned[..<end]).trimmingCharacters(in: .whitespacesAndNewlines) + "..."
    }

    func showAlert(title: String, message: String) {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.addButton(withTitle: "好")
        alert.runModal()
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
app.setActivationPolicy(.accessory)
let delegate = AppDelegate()
app.delegate = delegate
app.run()
