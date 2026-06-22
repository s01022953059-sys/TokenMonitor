import Cocoa
import WebKit

class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, WKScriptMessageHandler {
    var window: NSWindow!
    var webView: WKWebView!
    var statusItem: NSStatusItem!
    var updateCheckInProgress = false
    // 持有 server 进程引用, 防止 ARC 回收 Process 对象时杀掉子进程。
    var serverProcess: Process?
    // 缓存最近一次检测到的可用更新, 等前端通过 webkit.messageHandlers 触发时直接用。
    // 不缓存的话前端点了"立即更新"还要再走一遍网络, 体验更差。
    private var pendingUpdate: UpdateInfo?
    private var pendingCurrentVersion: String = "0"
    // 下载进度 KVO observation 的关联 key, 避免 ARC 释放导致进度回调失效。
    // 用实例级别 (非 static), 每个 AppDelegate 实例独立 key, 多窗口场景不互相覆盖。
    private static var downloadObservationKey: UInt8 = 0

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
        // 前端通过 webkit.messageHandlers.tokenMonitor.postMessage({...}) 触发
        // 桥接的 Swift 端操作, 例如 triggerAutoUpdate 调起自更新流程。
        // 所有 handler 集中到 self 处理, 见 userContentController(_:didReceive:)。
        userContentController.add(self, name: "tokenMonitor")
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

        // 延迟 1.5 秒再调 checkForUpdates, 给 webView 留时间把 index.html
        // 的 DOMContentLoaded 跑完 (那时 JS 端 __tokenMonitorOnUpdateAvailable
        // 已经注册, Swift 推过去能收到)。之前立即调会竞态: 异步
        // checkForUpdates 拉到 release 后 evaluateJavaScript, 但 JS 还没注册
        // callback, push 丢失, About 弹窗"立即更新"按钮不显示。
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            self?.checkForUpdates(silent: true)
        }
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
                // 同步设 false, 不走 DispatchQueue.main.async: async 会让
                // defer 排队到主线程, 但闭包已经 return, ordering 不保证
                // defer 在 performAutoUpdate 之前完成。结果: 用户点 NSAlert
                // 立即更新按钮时, updateCheckInProgress 还是 true, guard
                // 阻断。同步设保证 showUpdateAvailable 进入时 (同主线程
                // 后续代码) updateCheckInProgress 已经是 false。
                self.updateCheckInProgress = false
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
                    // 缓存 update, 等前端通过 message handler 触发自更新时直接用
                    self.pendingUpdate = update
                    self.pendingCurrentVersion = currentVersion
                    self.showUpdateAvailable(update: update, currentVersion: currentVersion)
                    // 通知前端"有新版本", 让 About 弹窗和首页徽章反映状态
                    self.notifyFrontendUpdateAvailable(version: update.version, currentVersion: currentVersion)
                } else if !silent {
                    self.pendingUpdate = nil
                    self.notifyFrontendNoUpdate(currentVersion: currentVersion)
                    self.showAlert(title: "已是最新版本", message: "当前版本 \(currentVersion) 已是最新。")
                }
            }
        }.resume()
    }

    // MARK: - 前端桥接
    //
    // JS 端通过 window.webkit.messageHandlers.tokenMonitor.postMessage({action: 'triggerAutoUpdate'})
    // 触发自更新。我们解析 action, 路由到对应 Swift 行为。
    // 任何前端→Swift 的调用都走这里, 避免散落的 handler 名字。

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.name == "tokenMonitor" else { return }
        guard let body = message.body as? [String: Any] else { return }
        let action = (body["action"] as? String) ?? ""
        switch action {
        case "triggerAutoUpdate":
            if let update = pendingUpdate {
                performAutoUpdate(update: update)
            } else {
                showAlert(title: "暂无可用更新", message: "没有缓存的更新信息, 请稍后重试或手动检查更新。")
            }
        case "openUpdatePage":
            if let url = pendingUpdate?.downloadURL {
                NSWorkspace.shared.open(url)
            } else if let feedURL = configuredUpdateFeedURL() {
                NSWorkspace.shared.open(feedURL)
            }
        default:
            break
        }
    }

    // 前端调用: window.webkit.messageHandlers.tokenMonitor.postMessage({...})
    // 反向通道: Swift 主动 push 状态给前端, 通过 evaluateJavaScript 注入 JS 调用。
    private func notifyFrontendUpdateAvailable(version: String, currentVersion: String) {
        let js = "window.__tokenMonitorOnUpdateAvailable && window.__tokenMonitorOnUpdateAvailable({version: '\(version)', currentVersion: '\(currentVersion)'});"
        webView?.evaluateJavaScript(js, completionHandler: nil)
    }

    private func notifyFrontendNoUpdate(currentVersion: String) {
        let js = "window.__tokenMonitorOnNoUpdate && window.__tokenMonitorOnNoUpdate({currentVersion: '\(currentVersion)'});"
        webView?.evaluateJavaScript(js, completionHandler: nil)
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
        // 按钮重排: "立即更新" 走自更新, "下载 zip" 走浏览器, "查看说明" 也走浏览器, "稍后" 关闭。
        // 这样无论用户偏好"全自动"还是"手动下载源码"都能满足。
        alert.addButton(withTitle: "立即更新")
        alert.addButton(withTitle: "下载 zip")
        alert.addButton(withTitle: "查看说明")
        alert.addButton(withTitle: "稍后")
        if let closeButton = alert.buttons.last {
            closeButton.keyEquivalent = "\u{1b}"
        }
        switch alert.runModal() {
        case .alertFirstButtonReturn:
            debugLog("NSAlert first button (立即更新) clicked")
            performAutoUpdate(update: update)
        case .alertSecondButtonReturn:
            debugLog("NSAlert second button (下载 zip) clicked")
            NSWorkspace.shared.open(update.downloadURL)
        case .alertThirdButtonReturn:
            debugLog("NSAlert third button (查看说明) clicked")
            NSWorkspace.shared.open(update.downloadURL)
        default:
            debugLog("NSAlert closed without clicking button")
            break
        }
    }

    // MARK: - 应用内自动更新
    //
    // 流程:
    //   1. 下载 release zip 到 /tmp/TokenMonitor/update-vX.Y.Z/
    //   2. 解压到同一目录
    //   3. 跑 build_macos.sh 编译 + 拼装新 .app
    //   4. 杀掉 server.py 子进程, 退出主 app
    //   5. 启动 update_helper.sh, 它在主 app 退出后做替换 + 重启
    //   6. 任何一步失败 → 弹错误 + 兜底到浏览器下载
    //
    // 工作目录: 优先用 .app 内 Resources/ 的 build_macos.sh, 找不到就
    // 退到 /Applications/Token Monitor.app/Contents/Resources/, 再不行
    // 就放弃自更新走浏览器兜底。

    private var inProgressUpdateWindow: NSWindow?

    // 临时 debug log 路径, 用于排查 v1.3.17 "立即更新" 按钮没反应。
    // 写到 /tmp/tm_debug.log, NSLog 也会进 Console.app。
    private static let debugLogPath = "/tmp/tm_debug.log"

    private func debugLog(_ msg: String) {
        let line = "[\(Date())] \(msg)\n"
        if let data = line.data(using: .utf8) {
            if FileManager.default.fileExists(atPath: Self.debugLogPath) {
                if let handle = try? FileHandle(forWritingTo: URL(fileURLWithPath: Self.debugLogPath)) {
                    handle.seekToEndOfFile()
                    try? handle.write(contentsOf: data)
                    try? handle.close()
                }
            } else {
                try? data.write(to: URL(fileURLWithPath: Self.debugLogPath))
            }
        }
        NSLog("TokenMonitor[update] %@", msg)
    }

    func performAutoUpdate(update: UpdateInfo) {
        debugLog("performAutoUpdate enter, version=\(update.version), updateCheckInProgress=\(updateCheckInProgress)")
        guard !updateCheckInProgress else {
            debugLog("performAutoUpdate guard blocked (in progress)")
            return
        }
        updateCheckInProgress = true

        let updateDir = "/tmp/TokenMonitor/update-\(update.version)"
        let zipPath = "\(updateDir).zip"
        let fm = FileManager.default

        // 准备暂存目录
        try? fm.removeItem(atPath: updateDir)
        try? fm.removeItem(atPath: zipPath)
        try? fm.createDirectory(atPath: "/tmp/TokenMonitor", withIntermediateDirectories: true)
        debugLog("staged dir prepared: \(updateDir)")

        // 弹非模态进度窗口, 用户能看到当前阶段。
        // RegardlessVisibility + makeKey 一起, 确保窗口在所有空间前置显示,
        // 即便主窗口 WebView 在全屏 + 焦点态, 进度窗也会盖在前面。
        let progressWindow = makeUpdateProgressWindow(version: update.version)
        self.inProgressUpdateWindow = progressWindow
        progressWindow.orderFrontRegardless()
        debugLog("progress window created, ordering front")
        updateProgress(stage: "下载更新包 (\(update.version))")

        // 1. 下载, 显式 30s timeout, 走 URLSessionConfiguration 而不是默认全局
        // (默认 URLSession.shared timeoutIntervalForRequest=60s, 加上 connect 等等
        //  卡 90s 都有可能。30s 用户能接受, 真卡了给一个明确错误比沉默好。)
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 30
        config.timeoutIntervalForResource = 60
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        // 不缓存任何响应, 避免下载到 gitcode 重定向到 myhuaweicloud
        // 后的过期签名 URL (Expires 24h), 那会导致下载到 AccessDenied XML。
        config.urlCache = nil
        let session = URLSession(configuration: config)

        // progress 观察 (用 KVO observation 关联到 task 防止 ARC 释放)
        var lastReportedBytes: Int64 = 0

        // 给 download URL 加一个 cache buster query 参数, 强制 URLSession 不
        // 命中任何 path-based 缓存 (NSURLCache 也会按 path 匹配, 即使
        // requestCachePolicy=reloadIgnoringLocalData 也可能命中 path 缓存)。
        // 双重保险: 同时设 If-None-Match header, 因为某些中间层 (公司代理 / VPN)
        // 会 strip query 参数, 但不会改 header。
        var downloadURL = update.downloadURL
        if var comps = URLComponents(url: update.downloadURL, resolvingAgainstBaseURL: false) {
            comps.queryItems = (comps.queryItems ?? []) + [
                URLQueryItem(name: "_tm", value: "\(Int(Date().timeIntervalSince1970))")
            ]
            if let newURL = comps.url {
                downloadURL = newURL
            }
        }
        debugLog("download URL: \(downloadURL.absoluteString)")

        var request = URLRequest(url: downloadURL)
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.timeoutInterval = 30
        // 用 Cache-Control: no-cache header 强制 gitcode CDN 每次生成新响应
        // (生成新的 myhuaweicloud 签名 URL, 24h Expires 不会过期)。
        // header 不会被中间代理 strip (代理改 query, 不改 standard headers)。
        // If-None-Match 可能触发 304, 不用。
        request.setValue("no-cache", forHTTPHeaderField: "Cache-Control")
        request.setValue("no-cache", forHTTPHeaderField: "Pragma")

        let downloadTask = session.downloadTask(with: request) { [weak self] tempURL, response, error in
            guard let self = self else { return }
            if let error = error {
                self.failAutoUpdate(update: update, message: "下载失败: \(error.localizedDescription)\n\n请检查网络连接, 或点 NSAlert 的'下载 zip'手动下载。")
                return
            }
            // 调试用: 检查 HTTP body 大小和 content-type
            if let http = response as? HTTPURLResponse {
                debugLog("download HTTP \(http.statusCode), content-length=\(http.expectedContentLength), url=\(http.url?.absoluteString ?? "?")")
            }
            guard let tempURL = tempURL, let http = response as? HTTPURLResponse, 200..<300 ~= http.statusCode else {
                let code = (response as? HTTPURLResponse)?.statusCode ?? 0
                self.failAutoUpdate(update: update, message: "下载失败, HTTP \(code)\n\n可能是 release 源 502/504, 稍后再试。")
                return
            }
            // gitcode CDN 在某些节点会返回 download-error 占位页 (3606 字节 HTML),
            // 而不是真 zip (470+ KB)。检测到小文件就 retry 一次, 多数情况
            // 第二次能拿到真 zip (CDN 节点可能抖动)。
            if let attr = try? FileManager.default.attributesOfItem(atPath: tempURL.path),
               let size = attr[.size] as? Int64, size < 10_000 {
                debugLog("download too small (\(size) bytes), retrying once")
                // 删除占位文件, 重试
                try? FileManager.default.removeItem(atPath: tempURL.path)
                self.retryDownload(update: update, attempt: 1, lastError: "CDN 返回占位 (\(size) bytes)")
                return
            }
            do {
                try fm.moveItem(at: tempURL, to: URL(fileURLWithPath: zipPath))
            } catch {
                self.failAutoUpdate(update: update, message: "暂存下载文件失败: \(error.localizedDescription)")
                return
            }
            self.continueAutoUpdateAfterDownload(update: update, zipPath: zipPath, updateDir: updateDir)
        }
        // 进度反馈: 每收到 ~64KB 更新一次文案, 让用户知道在下载
        let observation = downloadTask.progress.observe(\.fractionCompleted, options: [.new]) { [weak self] progress, _ in
            guard let self = self else { return }
            let received = progress.completedUnitCount
            if received - lastReportedBytes > 64 * 1024 || progress.fractionCompleted >= 1.0 {
                lastReportedBytes = received
                let total = progress.totalUnitCount
                let pct = total > 0 ? Int(progress.fractionCompleted * 100) : 0
                let totalKB = total / 1024
                let receivedKB = received / 1024
                self.updateProgress(stage: "下载更新包 (\(pct)%, \(receivedKB)KB / \(totalKB)KB)")
            }
        }
        // 持有 observation, 防止 ARC 释放
        objc_setAssociatedObject(downloadTask, &AppDelegate.downloadObservationKey, observation, .OBJC_ASSOCIATION_RETAIN)
        downloadTask.resume()
    }

    // 重试下载: 给下载 URL 加一个新的 cache buster 重新下载, 最多 2 次。
    // gitcode CDN 节点会返回 download-error 占位 (3.6 KB), retry 一次通常
    // 能命中另一个 CDN 节点拿真 zip。
    private func retryDownload(update: UpdateInfo, attempt: Int, lastError: String) {
        if attempt > 1 {
            // 2 次都失败, 报失败
            self.failAutoUpdate(update: update, message: "下载失败: \(lastError)\n\nCDN 节点异常, 稍后再试或手动下载。")
            return
        }
        // 用新的 cache buster 重试
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 30
        config.timeoutIntervalForResource = 60
        config.urlCache = nil
        let session = URLSession(configuration: config)

        var downloadURL = update.downloadURL
        if var comps = URLComponents(url: update.downloadURL, resolvingAgainstBaseURL: false) {
            comps.queryItems = (comps.queryItems ?? []) + [
                URLQueryItem(name: "_tm", value: "\(Int(Date().timeIntervalSince1970))_retry\(attempt)")
            ]
            if let newURL = comps.url {
                downloadURL = newURL
            }
        }
        debugLog("retry \(attempt) URL: \(downloadURL.absoluteString)")

        var request = URLRequest(url: downloadURL)
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.timeoutInterval = 30
        // 双重保险: Cache-Control no-cache 强制新响应
        request.setValue("no-cache", forHTTPHeaderField: "Cache-Control")
        request.setValue("no-cache", forHTTPHeaderField: "Pragma")

        var lastReportedBytes: Int64 = 0
        let task = session.downloadTask(with: request) { [weak self] tempURL, response, error in
            guard let self = self else { return }
            if let error = nil as Error? {
                // placeholder, real check below
            }
            if let http = response as? HTTPURLResponse {
                debugLog("retry \(attempt) HTTP \(http.statusCode), content-length=\(http.expectedContentLength)")
            }
            if let error = error {
                self.failAutoUpdate(update: update, message: "下载失败 (重试): \(error.localizedDescription)")
                return
            }
            guard let tempURL = tempURL, let http = response as? HTTPURLResponse, 200..<300 ~= http.statusCode else {
                self.retryDownload(update: update, attempt: attempt + 1, lastError: "HTTP \((response as? HTTPURLResponse)?.statusCode ?? 0)")
                return
            }
            if let attr = try? FileManager.default.attributesOfItem(atPath: tempURL.path),
               let size = attr[.size] as? Int64, size < 10_000 {
                try? FileManager.default.removeItem(atPath: tempURL.path)
                debugLog("retry \(attempt) too small (\(size) bytes), retrying")
                self.retryDownload(update: update, attempt: attempt + 1, lastError: "CDN 占位 (\(size) bytes)")
                return
            }
            let zipPath = "/tmp/TokenMonitor/update-\(update.version).zip"
            do {
                try FileManager.default.moveItem(at: tempURL, to: URL(fileURLWithPath: zipPath))
            } catch {
                self.failAutoUpdate(update: update, message: "暂存下载文件失败 (重试): \(error.localizedDescription)")
                return
            }
            self.continueAutoUpdateAfterDownload(update: update, zipPath: zipPath, updateDir: "/tmp/TokenMonitor/update-\(update.version)")
        }
        let observation = task.progress.observe(\.fractionCompleted, options: [.new]) { [weak self] progress, _ in
            guard let self = self else { return }
            let received = progress.completedUnitCount
            if received - lastReportedBytes > 64 * 1024 || progress.fractionCompleted >= 1.0 {
                lastReportedBytes = received
                let total = progress.totalUnitCount
                let pct = total > 0 ? Int(progress.fractionCompleted * 100) : 0
                let totalKB = total / 1024
                let receivedKB = received / 1024
                self.updateProgress(stage: "下载更新包 (重试 \(attempt), \(pct)%, \(receivedKB)KB / \(totalKB)KB)")
            }
        }
        objc_setAssociatedObject(task, &AppDelegate.downloadObservationKey, observation, .OBJC_ASSOCIATION_RETAIN)
        task.resume()
    }

    private func continueAutoUpdateAfterDownload(update: UpdateInfo, zipPath: String, updateDir: String) {
        DispatchQueue.main.async { self.updateProgress(stage: "解压源码包") }
        let fm = FileManager.default
        do {
            try fm.createDirectory(atPath: updateDir, withIntermediateDirectories: true)
            // 用 /usr/bin/ditto 解压: macOS 原生 zip 工具, 给 .app bundle 设计,
            // 对 UTF-8 文件名 (例如 '启动 Token Monitor.bat') 比 /usr/bin/unzip 友好。
            // 失败也不致命, 走 unzip 兜底 (但 unzip 可能因为中文文件名失败)。
            let ditto = Process()
            ditto.executableURL = URL(fileURLWithPath: "/usr/bin/ditto")
            ditto.arguments = ["-x", "-k", zipPath, updateDir]
            let pipe = Pipe()
            ditto.standardOutput = pipe
            ditto.standardError = pipe
            try ditto.run()
            ditto.waitUntilExit()
            // ditto 失败也继续: 可能是 zip 内某些条目 ditto 解不了,
            // 其他条目可能 OK, find + rm 兜底。
            // ditto 失败信息进 stderr 日志。
            if ditto.terminationStatus != 0 {
                let errData = pipe.fileHandleForReading.readDataToEndOfFile()
                let msg = String(data: errData, encoding: .utf8) ?? ""
                NSLog("[update] ditto 解压 exit \(ditto.terminationStatus): \(msg)")
            }
            // 兜底清理 windows_build 目录 (无论 ditto 是否成功)
            // 用 find -type d -name windows_build -prune -exec rm -rf {} +
            // 一次清掉所有嵌套的 windows_build, 不依赖具体路径前缀。
            let find = Process()
            find.executableURL = URL(fileURLWithPath: "/usr/bin/find")
            find.arguments = [updateDir, "-type", "d", "-name", "windows_build", "-exec", "rm", "-rf", "{}", "+"]
            let findPipe = Pipe()
            find.standardOutput = findPipe
            find.standardError = findPipe
            try? find.run()
            find.waitUntilExit()
        } catch {
            self.failAutoUpdate(update: update, message: "解压阶段异常: \(error.localizedDescription)")
            return
        }

        // gitcode 的 zip 解压后是 TokenMonitor-<tag>-<sha>/ 这样的子目录,
        // build_macos.sh 期望在源码根运行, 所以找这个子目录。
        guard let sourceRoot = locateSourceRoot(in: updateDir) else {
            self.failAutoUpdate(update: update, message: "在 \(updateDir) 下找不到源码根目录")
            return
        }

        DispatchQueue.main.async { self.updateProgress(stage: "编译新版本") }

        // 2. 跑 build_macos.sh
        let buildScript = "\(sourceRoot)/build_macos.sh"
        guard fm.fileExists(atPath: buildScript) else {
            self.failAutoUpdate(update: update, message: "暂存源码里没有 build_macos.sh")
            return
        }
        let build = Process()
        build.executableURL = URL(fileURLWithPath: "/bin/bash")
        build.arguments = [buildScript]
        build.currentDirectoryURL = URL(fileURLWithPath: sourceRoot)
        let buildLog = Pipe()
        build.standardOutput = buildLog
        build.standardError = buildLog
        do {
            try build.run()
            build.waitUntilExit()
        } catch {
            self.failAutoUpdate(update: update, message: "编译启动失败: \(error.localizedDescription)")
            return
        }
        if build.terminationStatus != 0 {
            let logData = buildLog.fileHandleForReading.readDataToEndOfFile()
            let snippet = String(data: logData, encoding: .utf8) ?? "未知错误"
            self.failAutoUpdate(update: update, message: "编译失败 (exit \(build.terminationStatus))\n\(snippet.prefix(400))")
            return
        }

        let builtApp = "\(sourceRoot)/build/Token Monitor.app"
        guard fm.fileExists(atPath: builtApp) else {
            self.failAutoUpdate(update: update, message: "编译完成但找不到产物 \(builtApp)")
            return
        }

        DispatchQueue.main.async {
            self.updateProgress(stage: "准备安装, 即将重启 app")
        }
        // 给用户半秒看"准备安装"的提示, 然后退出主 app。
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.6) { [weak self] in
            self?.performAppReplacement(stagedApp: builtApp, update: update)
        }
    }

    private func performAppReplacement(stagedApp: String, update: UpdateInfo) {
        // 杀掉自己启动的 server.py 子进程, 否则 update_helper 替换 .app 时
        // 仍然有 python 在跑 (虽然不影响, 但保持干净)。
        self.serverProcess?.terminate()

        // helper 路径优先用 stagedApp (新版本) 里的, 而不是当前 app (旧版本) 里的。
        // 原因: 自更新代码会"自我升级", 旧 .app 里的 helper 不知道新代码的设计
        // (例如 v1.3.12 引入了 osascript sudo 弹窗, v1.3.10 的 helper 没有)。
        // 如果用旧 helper, 永远装不上新代码 (helper 自己不知道如何处理新需求)。
        // 兜底: 如果 stagedApp 里没找到 helper, 用当前 app 里的 (向后兼容)。
        let fm = FileManager.default
        let bundlePath = Bundle.main.bundlePath
        let bundleId = Bundle.main.bundleIdentifier ?? "com.baggio.tokenmonitor"
        let stagedHelper = "\(stagedApp)/Contents/Resources/update_helper.sh"
        let legacyHelper = "\(bundlePath)/Contents/Resources/update_helper.sh"
        let helperPath = fm.fileExists(atPath: stagedHelper) ? stagedHelper : legacyHelper
        // 主 app 退出后 helper 还要活, 必须 nohup + disown (走 sh -c &)
        // helper 内置 3 秒 sleep 等主 app 进程完全释放 /Applications/Token Monitor.app
        let cmd = "nohup /bin/bash \"\(helperPath)\" \"\(stagedApp)\" \"\(bundlePath)\" \"\(bundleId)\" >/dev/null 2>&1 &"

        let helperLauncher = Process()
        helperLauncher.executableURL = URL(fileURLWithPath: "/bin/sh")
        helperLauncher.arguments = ["-c", cmd]
        try? helperLauncher.run()

        // 自己退出, helper 会接管替换 + 重启
        NSApplication.shared.terminate(nil)
    }

    private func locateSourceRoot(in dir: String) -> String? {
        let fm = FileManager.default
        guard let entries = try? fm.contentsOfDirectory(atPath: dir) else { return nil }
        // gitcode zip 解出来通常是 <repo>-<sha>/ 这种名字, 取第一个子目录
        for entry in entries.sorted() {
            let full = "\(dir)/\(entry)"
            var isDir: ObjCBool = false
            if fm.fileExists(atPath: full, isDirectory: &isDir), isDir.boolValue {
                if fm.fileExists(atPath: "\(full)/build_macos.sh") {
                    return full
                }
            }
        }
        // 兜底: 解压出来的根直接就是源码 (罕见)
        if fm.fileExists(atPath: "\(dir)/build_macos.sh") {
            return dir
        }
        return nil
    }

    private func failAutoUpdate(update: UpdateInfo, message: String) {
        DispatchQueue.main.async {
            self.updateCheckInProgress = false
            self.inProgressUpdateWindow?.orderOut(nil)
            self.inProgressUpdateWindow = nil
            self.showAlert(
                title: "自动更新失败",
                message: "\(message)\n\n你可以选择手动下载 zip 后替换 /Applications/Token Monitor.app, 或打开浏览器下载页面。"
            )
            // 兜底: 直接打开浏览器, 用户至少能下载到文件
            NSWorkspace.shared.open(update.downloadURL)
        }
    }

    private func makeUpdateProgressWindow(version: String) -> NSWindow {
        // 不要 .closable: 自更新流程一旦启动就不能中断,
        // 用户关掉进度窗会导致 UI 状态混乱。让用户只能等待完成。
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 460, height: 180),
            styleMask: [.titled],
            backing: .buffered,
            defer: false
        )
        window.title = "正在更新到 \(version)"
        window.isReleasedWhenClosed = false
        // 进度窗置顶 modalPanel 层级, 盖在主窗口 WebView 上面, 不会被遮住。
        // 用户要点过"立即更新"会期望看到反馈, 而不是面对一片漆黑主窗口发呆。
        window.level = .modalPanel
        window.hidesOnDeactivate = false
        window.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        let label = NSTextField(labelWithString: "准备中...")
        label.frame = NSRect(x: 30, y: 100, width: 400, height: 30)
        label.font = NSFont.systemFont(ofSize: 14, weight: .medium)
        label.tag = 9001  // 用 tag 找回来更新文案
        let detail = NSTextField(labelWithString: "更新过程中请勿关闭 app。")
        detail.frame = NSRect(x: 30, y: 60, width: 400, height: 20)
        detail.font = NSFont.systemFont(ofSize: 11)
        detail.textColor = NSColor.secondaryLabelColor
        let spinner = NSProgressIndicator()
        spinner.frame = NSRect(x: 30, y: 30, width: 20, height: 20)
        spinner.style = .spinning
        spinner.startAnimation(nil)
        let container = NSView(frame: NSRect(x: 0, y: 0, width: 460, height: 180))
        container.addSubview(label)
        container.addSubview(detail)
        container.addSubview(spinner)
        window.contentView = container
        window.center()
        return window
    }

    private func updateProgress(stage: String) {
        guard let window = inProgressUpdateWindow,
              let label = window.contentView?.viewWithTag(9001) as? NSTextField else { return }
        label.stringValue = stage
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
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = AppDelegate()
app.delegate = delegate
app.run()
