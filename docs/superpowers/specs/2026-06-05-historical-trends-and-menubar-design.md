# 历史统计折线图与 macOS 菜单栏实时 Token 显示设计方案

本方案旨在为 Token Monitor 增加最近 7 天/30 天历史消耗统计折线图，并在 macOS 系统菜单栏（右上角状态栏）中提供实时的 Token 数量挂件，同时彻底解决 Dock 启动时弹出默认浏览器网页的 Bug。

---

## 1. 核心功能设计

### 1.1 macOS 系统菜单栏挂件 (Status Bar Widget)
* **实时显示**：程序启动后，在 macOS 顶部菜单栏（右上角状态栏）实时展示一个类似 `🔥 12.3K` 或 `🔥 1.5M` 的 Token 消耗指示器。
* **常驻运行与生命周期管理**：
  * 主窗口关闭时，应用程序不会真正退出，而是仅隐藏窗口并在菜单栏继续运行。
  * 点击状态栏图标会拉出菜单，提供 “显示主大屏” 和 “退出应用” 选项。
  * 点击 Dock 栏图标时，自动唤醒并显示主大屏。
* **自动刷新**：Swift 内部启动一个每 5 秒执行一次的定时器，静默请求本地 `http://127.0.0.1:15723/api/usage` 并自动刷新菜单栏文本。

### 1.2 浮动毛玻璃折线图弹窗 (Modal Chart Overlay)
* **右上角入口**：大屏右上角提供一个半透明毛玻璃背景的极简折线图图标（SVG）。
* **毛玻璃弹窗**：点击该按钮，拉起全屏高斯模糊背景的浮窗遮罩（`backdrop-filter: blur(20px)`），中央悬浮历史图表卡片。
* **胶囊式切换 Tab**：图表顶部提供“每周趋势”与“每月趋势”的 Segmented Control，可瞬间无缝切换。
* **霓虹折线图**：图表使用 Chart.js 渲染一条紫色霓虹（`#a855f7`）平滑贝塞尔折线，下方填充紫色半透明渐变，并自动进行大数值缩写格式化。

### 1.3 启动网页弹出 Bug 清除
* **定位原因**：先前打包的 `Token Monitor.app` 依然缓存了旧版的 `start.sh` 或旧版 Swift 代码，在后台包含了 `open http://127.0.0.1:15723` 的命令。
* **修复方法**：彻底去除所有 `open` 命令行。重新编译 `app_wrapper.swift` 生成新的二进制，重新通过 `hdiutil` 制作新的 `Token Monitor.dmg` 并重新覆盖安装。

---

## 2. 详细实现细节

### 2.1 Swift 菜单栏与窗口逻辑修改 (`app_wrapper.swift`)
1. 引入 `NSStatusItem`：
   ```swift
   var statusItem: NSStatusItem!
   ```
2. 在 `applicationDidFinishLaunching` 中初始化菜单栏挂件，并设定下拉菜单（Show Main Window / Quit）。
3. 增加 `Timer`，每 5 秒向 `127.0.0.1:15723/api/usage` 发起 GET 请求：
   * 解析 JSON 中的 `summary.total_tokens`。
   * 进行 K/M/亿 的本地格式化。
   * 更新 `statusItem.button?.title = "🔥 \(formatted)"`。
4. 重构主窗口关闭逻辑：
   * 在 `app_wrapper.swift` 中，将窗口关闭代理设为“隐藏窗口”：
     ```swift
     func windowShouldClose(_ sender: NSWindow) -> Bool {
         window.orderOut(nil) // 隐藏窗口而非销毁
         return false
     }
     ```
   * 设定 `applicationShouldTerminateAfterLastWindowClosed` 返回 `false`，确保窗口关闭后程序不退出。
   * 编写 `applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool`，当用户点击 Dock 图标时重新拉起并显示主窗口。

### 2.2 前端浮窗与折线图修改 (`index.html`)
1. **DOM 部分**：
   * 右上角追加 `<button id="historyOpenBtn" class="history-btn">` 放置 SVG 📊 按钮。
   * 底部追加 `<div id="historyModal" class="modal-overlay">` 结构，内部包含胶囊 Tab、用于 Chart.js 的 `<canvas id="historyChart">` 区域，以及关闭按钮。
2. **CSS 部分**：
   * `.history-btn`：采用 `backdrop-filter: blur(10px)` 半透明毛玻璃样式，hover 时带有微光发散效果。
   * `.modal-overlay`：`position: fixed` 铺满，`background: rgba(5, 6, 9, 0.5)` + `backdrop-filter: blur(20px)` 强虚化遮罩。
   * `.modal-content`：深色半透明面板，采用霓虹渐变上边框。
3. **JS 部分**：
   * 请求 `/api/history` 获得 30 天消耗。
   * 绘制平滑曲线折线图：
     ```javascript
     tension: 0.4,
     borderColor: '#a855f7',
     backgroundColor: gradientBg // 紫色霓虹渐变填充
     ```
   * 点击 “周统计”：`data.labels.slice(-7)` 和 `data.values.slice(-7)`。
   * 点击 “月统计”：`data.labels.slice(-30)` 和 `data.values.slice(-30)`。

---

## 3. 验证计划
1. **本地调试**：编译并双击运行 `Token Monitor.app`，验证：
   * 菜单栏右上角是否正常出现 `🔥 0` 或对应的 Token 数，且每 5 秒根据 API 变化更新。
   * 点击状态栏菜单中的“显示主大屏”或双击 Dock 栏图标，大屏是否正常出现。
   * 点击关闭主大屏窗口，程序是否继续驻留状态栏且依然保持实时更新。
   * 整个启动过程是否绝对没有打开系统 Chrome/Safari 网页。
2. **图表验证**：
   * 点击右上角折线图图标，浮窗是否优雅淡入。
   * 切换周/月趋势，折线图数据与横纵轴是否无缝平滑切换。
   * 点击遮罩空白区，弹窗是否成功关闭。
