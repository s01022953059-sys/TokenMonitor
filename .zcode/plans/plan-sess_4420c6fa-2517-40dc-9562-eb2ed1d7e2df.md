## 中继服务器时区修复

### 问题
`validateReport` 用 `now.Format("2006-01-02")`（服务器本地时间）校验 report_date，但 `runArchive` 用 `communityArchiveDate(now)`（北京时间）过滤归档。两个时区不一致，导致跨时区用户的报告可能通过校验但被归档排除。

### 修复
`community_relay/main.go` 第 387 行：将 `serverDay` 的计算从 `now.Format(...)` 改为 `communityArchiveDate(now)`，与归档过滤保持一致。

### 改动
- 1 行代码变更
- 客户端已用 `_community_today()` 统一上报北京时间，服务端校验对齐即可
- 无需 bump 版本号（中继独立部署，不走客户端发版流程）

### 说明
客户端 v1.4.88 已经修复了四个 bug，中继这个小修复让服务端校验也统一到北京时间。中继部署后，配合客户端升级，时区不一致的问题就彻底解决了。