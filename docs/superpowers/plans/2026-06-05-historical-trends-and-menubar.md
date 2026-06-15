# 历史统计与 macOS 状态栏实时 Token 挂件实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现右上角历史趋势弹窗（周/月折线图）以及 macOS 菜单栏右上角的实时 Token 燃烧挂件，同时彻底修复 Dock 启动时的多余浏览器网页弹出。

**Architecture:** 前端通过 DOM 浮窗与 Chart.js 异步获取 `/api/history` 本地数据并进行 slice 内存切片；Swift 端利用 Cocoa `NSStatusItem` 创建全局状态菜单，结合 `Timer` 静默轮询本地 Python HTTP 数据服务，托管 NSWindow 生命周期。

**Tech Stack:** HTML5, CSS3, Vanilla JS, Chart.js, Swift (Cocoa, WebKit)

---

### Task 1: 前端大屏历史统计浮窗与折线图

**Files:**
- Modify: `index.html`

- [ ] **Step 1: 在 `index.html` 的 CSS 样式中加入浮窗和右上角按钮样式**
  需要添加在 `<style>` 内的样式（约 270 行后）：
  ```css
  /* 右上角趋势按钮 */
  .history-btn {
      position: absolute;
      top: 25px;
      right: 25px;
      background: rgba(255, 255, 255, 0.03);
      border: 1px solid rgba(255, 255, 255, 0.08);
      border-radius: 12px;
      width: 40px;
      height: 40px;
      display: flex;
      justify-content: center;
      align-items: center;
      cursor: pointer;
      color: var(--text-main);
      transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
      backdrop-filter: blur(10px);
      -webkit-backdrop-filter: blur(10px);
      z-index: 50;
  }
  .history-btn:hover {
      background: rgba(255, 255, 255, 0.08);
      border-color: rgba(168, 85, 247, 0.4);
      color: #a855f7;
      transform: translateY(-2px);
      box-shadow: 0 4px 15px rgba(168, 85, 247, 0.15);
  }
  .history-btn svg {
      width: 20px;
      height: 20px;
  }

  /* 磨砂毛玻璃弹窗遮罩 */
  .modal-overlay {
      position: fixed;
      top: 0;
      left: 0;
      width: 100vw;
      height: 100vh;
      background: rgba(5, 6, 9, 0.6);
      backdrop-filter: blur(25px);
      -webkit-backdrop-filter: blur(25px);
      z-index: 100;
      display: flex;
      justify-content: center;
      align-items: center;
      opacity: 0;
      pointer-events: none;
      transition: opacity 0.30s ease;
  }
  .modal-overlay.active {
      opacity: 1;
      pointer-events: auto;
  }

  /* 弹窗主体面板 */
  .modal-content {
      background: rgba(9, 11, 16, 0.85);
      border: 1px solid rgba(255, 255, 255, 0.08);
      border-radius: 24px;
      width: 90%;
      max-width: 620px;
      padding: 30px;
      position: relative;
      box-shadow: 0 30px 70px rgba(0, 0, 0, 0.8), 0 0 40px rgba(168, 85, 247, 0.05);
      transform: scale(0.95);
      transition: transform 0.3s cubic-bezier(0.34, 1.56, 0.64, 1);
  }
  .modal-overlay.active .modal-content {
      transform: scale(1);
  }
  .modal-content::before {
      content: '';
      position: absolute;
      top: 0;
      left: 0;
      right: 0;
      height: 2px;
      background: linear-gradient(90deg, transparent, #a855f7, transparent);
  }

  .modal-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 25px;
  }
  .modal-header h3 {
      font-size: 18px;
      font-weight: 700;
      background: linear-gradient(135deg, #ffffff 0%, #c7d2fe 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
  }
  .close-btn {
      background: transparent;
      border: none;
      color: var(--text-muted);
      cursor: pointer;
      font-size: 20px;
      transition: color 0.2s;
  }
  .close-btn:hover {
      color: #ffffff;
  }

  /* 胶囊型 Segmented Tab */
  .tab-container {
      background: rgba(255, 255, 255, 0.02);
      border: 1px solid rgba(255, 255, 255, 0.05);
      padding: 3px;
      border-radius: 12px;
      display: inline-flex;
      gap: 2px;
      margin-bottom: 20px;
  }
  .tab-btn {
      background: transparent;
      border: none;
      color: var(--text-muted);
      padding: 6px 16px;
      border-radius: 9px;
      font-size: 13px;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.2s ease;
  }
  .tab-btn.active {
      background: rgba(168, 85, 247, 0.15);
      border: 1px solid rgba(168, 85, 247, 0.2);
      color: #d8b4fe;
      text-shadow: 0 0 10px rgba(168, 85, 247, 0.3);
  }

  /* 折线图绘制区 */
  .modal-chart-wrapper {
      width: 100%;
      height: 300px;
      position: relative;
  }
  ```

- [ ] **Step 2: 在 `index.html` 的 DOM 中增加按钮与 Modal 元素**
  在 `<div class="container">` 的开头（即 `header` 元素上方）追加：
  ```html
  <button id="historyOpenBtn" class="history-btn" title="查看历史统计趋势">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <line x1="18" y1="20" x2="18" y2="10"></line>
          <line x1="12" y1="20" x2="12" y2="4"></line>
          <line x1="6" y1="20" x2="6" y2="14"></line>
      </svg>
  </button>
  ```
  在 `</body>` 前追加弹窗：
  ```html
  <!-- 历史统计弹窗 -->
  <div id="historyModal" class="modal-overlay">
      <div class="modal-content">
          <div class="modal-header">
              <h3>大模型消耗趋势</h3>
              <button id="historyCloseBtn" class="close-btn">&times;</button>
          </div>
          <div class="tab-container">
              <button class="tab-btn active" data-days="7">周统计</button>
              <button class="tab-btn" data-days="30">月统计</button>
          </div>
          <div class="modal-chart-wrapper">
              <canvas id="historyChart"></canvas>
          </div>
      </div>
  </div>
  ```

- [ ] **Step 3: 修改 JS 部分，实现历史趋势折线图的异步读取与 Tab 无缝切片逻辑**
  在 `<script>` 中追加交互代码与图表更新代码：
  ```javascript
  // 历史图表全局变量
  let historyChartInstance = null;
  let rawHistoryData = null; // 缓存的 30 天原始数据

  function initHistoryChart() {
      const ctx = document.getElementById('historyChart').getContext('2d');
      
      // 创建渐变填充色
      const gradient = ctx.createLinearGradient(0, 0, 0, 300);
      gradient.addColorStop(0, 'rgba(168, 85, 247, 0.3)');
      gradient.addColorStop(1, 'rgba(168, 85, 247, 0.0)');

      historyChartInstance = new Chart(ctx, {
          type: 'line',
          data: {
              labels: [],
              datasets: [{
                  label: '消耗 Tokens',
                  data: [],
                  borderColor: '#a855f7',
                  borderWidth: 3,
                  backgroundColor: gradient,
                  fill: true,
                  tension: 0.4, // 平滑贝塞尔曲线
                  pointBackgroundColor: '#a855f7',
                  pointBorderColor: '#ffffff',
                  pointBorderWidth: 1.5,
                  pointRadius: 4,
                  pointHoverRadius: 6
              }]
          },
          options: {
              responsive: true,
              maintainAspectRatio: false,
              plugins: {
                  legend: { display: false },
                  tooltip: {
                      callbacks: {
                          label: function(context) {
                              return ' 消耗: ' + formatTokens(context.parsed.y);
                          }
                      }
                  }
              },
              scales: {
                  x: {
                      grid: { display: false },
                      ticks: {
                          color: '#6e7681',
                          font: { family: 'JetBrains Mono', size: 10 }
                      }
                  },
                  y: {
                      grid: { color: 'rgba(255, 255, 255, 0.05)' },
                      ticks: {
                          color: '#6e7681',
                          font: { family: 'JetBrains Mono', size: 10 },
                          callback: function(value) {
                              return formatTokens(value);
                          }
                      }
                  }
              }
          }
      });
  }

  async function fetchHistoryAndRender(days = 7) {
      if (!rawHistoryData) {
          try {
              const res = await fetch('/api/history');
              rawHistoryData = await res.json();
          } catch (err) {
              console.error("加载历史统计数据出错:", err);
              return;
          }
      }

      if (!rawHistoryData || !rawHistoryData.labels) return;

      // 提取最新的 `days` 天数据
      const slicedLabels = rawHistoryData.labels.slice(-days).map(dateStr => {
          // 格式化日期为 MM/DD
          const parts = dateStr.split('-');
          return parts.length >= 3 ? `${parts[1]}/${parts[2]}` : dateStr;
      });
      const slicedValues = rawHistoryData.values.slice(-days);

      // 更新折线图
      if (!historyChartInstance) {
          initHistoryChart();
      }
      historyChartInstance.data.labels = slicedLabels;
      historyChartInstance.data.datasets[0].data = slicedValues;
      historyChartInstance.update();
  }

  // 绑定历史弹窗的触发事件
  const historyModal = document.getElementById('historyModal');
  const historyOpenBtn = document.getElementById('historyOpenBtn');
  const historyCloseBtn = document.getElementById('historyCloseBtn');
  const tabBtns = document.querySelectorAll('.tab-btn');

  historyOpenBtn.addEventListener('click', () => {
      historyModal.classList.add('active');
      fetchHistoryAndRender(7); // 默认展示周统计
  });

  historyCloseBtn.addEventListener('click', () => {
      historyModal.classList.remove('active');
  });

  // 点击遮罩外部区域关闭
  historyModal.addEventListener('click', (e) => {
      if (e.target === historyModal) {
          historyModal.classList.remove('active');
      }
  });

  // Tab 按钮绑定
  tabBtns.forEach(btn => {
      btn.addEventListener('click', () => {
          tabBtns.forEach(b => b.classList.remove('active'));
          btn.classList.add('active');
          const days = parseInt(btn.getAttribute('data-days'));
          fetchHistoryAndRender(days);
      });
  });
  ```

---

### Task 2: macOS 系统状态栏（MenuBar）指示器与生命周期逻辑

**Files:**
- Modify: `app_wrapper.swift`

- [ ] **Step 1: 在 `app_wrapper.swift` 中声明 `NSStatusItem` 状态挂件变量**
  修改 `AppDelegate` 类定义：
  ```swift
  class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
      var window: NSWindow!
      var webView: WKWebView!
      var statusItem: NSStatusItem! // 新增菜单栏挂件
  ```

- [ ] **Step 2: 初始化系统状态栏挂件与轮询定时器**
  在 `applicationDidFinishLaunching` 的开头追加：
  ```swift
          // 初始化右上角系统状态栏项
          statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
          if let button = statusItem.button {
              button.title = "🔥 --"
              
              // 创建状态栏下拉菜单
              let menu = NSMenu()
              menu.addItem(NSMenuItem(title: "显示大屏", action: #selector(showMainWindow), keyEquivalent: "s"))
              menu.addItem(NSMenuItem.separator())
              menu.addItem(NSMenuItem(title: "退出应用", action: #selector(quitApp), keyEquivalent: "q"))
              statusItem.menu = menu
          }
          
          // 启动状态栏 Token 实时刷新定时器 (每 5 秒一次)
          startTokenUpdateTimer()
  ```

- [ ] **Step 3: 编写后台数据更新与数值格式化函数**
  在 `AppDelegate` 内增加如下辅助函数：
  ```swift
      @objc func showMainWindow() {
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
                              button.title = "🔥 \(formatted)"
                          }
                      }
                  }
              } catch {
                  // 忽略本地解析异常以保证常驻运行静默
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
  ```

- [ ] **Step 4: 实现关闭窗口隐藏（不销毁程序）与 Dock 点击唤醒的代理函数**
  在 `AppDelegate` 中重构或添加生命周期处理代理：
  ```swift
      // 拦截关闭窗口事件：将窗口隐藏而不是销毁
      func windowShouldClose(_ sender: NSWindow) -> Bool {
          window.orderOut(nil)
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
  ```

---

### Task 3: 系统编译构建、打包与部署 (彻底解决 Dock 额外弹网页 Bug)

**Files:**
- Modify: `start.sh`

- [ ] **Step 1: 重新编译最新的 Swift 二进制包**
  在终端中执行 `swiftc` 对最新代码编译输出：
  Run: `swiftc /Users/baggio/.gemini/antigravity/scratch/token_monitor/app_wrapper.swift -o "/Users/baggio/.gemini/antigravity/scratch/token_monitor/Token Monitor.app/Contents/MacOS/TokenMonitor" -sdk $(xcrun --show-sdk-path) -target x86_64-apple-macos10.15`
  (若支持 arm64 可不带 target 或加 universal 选项。此处采用 macOS 10.15 作为通用支持)。

- [ ] **Step 2: 清理原有的 Python 后台常驻服务，重新载入运行**
  先停止原先在 `15723` 端口上运行的旧进程：
  Run: `/Users/baggio/.gemini/antigravity/scratch/token_monitor/start.sh stop`
  Run: `/Users/baggio/.gemini/antigravity/scratch/token_monitor/start.sh start`

- [ ] **Step 3: 重新生成 macOS .dmg 磁盘映像**
  清退原有的 DMG，重新执行 `hdiutil` 封包过程：
  Run: `rm -f "/Users/baggio/.gemini/antigravity/scratch/token_monitor/Token Monitor.dmg"`
  Run: `hdiutil create -volname "Token Monitor" -srcfolder "/Users/baggio/.gemini/antigravity/scratch/token_monitor/Token Monitor.app" -ov -format UDZO "/Users/baggio/.gemini/antigravity/scratch/token_monitor/Token Monitor.dmg"`

- [ ] **Step 4: 清理 Dock 缓存并更新注册**
  直接打开应用注册其在 Dock 上的路径：
  Run: `open "/Users/baggio/.gemini/antigravity/scratch/token_monitor/Token Monitor.app"`
