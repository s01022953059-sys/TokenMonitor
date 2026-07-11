# Community Relay Design

## Goal

让 Token Monitor 用户无需安装 Git、配置 GitCode 凭据或手工点击，也能自动提交匿名社区统计。GitCode token 只保存在鹏帅的 VPS，不进入客户端、安装包或公开仓库。

## Architecture

- 客户端仍在本机生成统计，只提交匿名 ID、报告日期、总 Token、按工具汇总和应用版本。
- 新增纯 Go `community_relay`，监听 VPS `127.0.0.1:18190`。
- Nginx 将 `https://new.taqi.cc/token-monitor-community/` 转发到中继服务，并限制请求体和频率。
- 中继服务校验请求后，用服务端环境变量中的 GitCode token 写入 `community-data` 分支。
- 社区排行读取继续使用公开 GitCode 数据，不经过中继服务。

## Device Identity

客户端首次上报时生成 32 字节随机设备密钥并仅保存在 `~/.token_monitor/community_credential.json`。服务端只在公开报告中保存密钥的 SHA-256，不保存明文。后续上报必须匹配该哈希，因此其他人不能覆盖已有匿名 ID。

旧报告没有密钥哈希。遇到旧报告时，中继返回 `identity_upgrade_required`，客户端自动生成新匿名 ID 后重试一次。尚未创建远端报告的旧 ID 可以直接保留。

## Validation And Abuse Controls

- ID 必须匹配 `User_[A-Z0-9]{5,12}`。
- 报告日期只能是服务器日期前后一天。
- 总 Token 和单工具 Token 必须是非负整数且不超过上限。
- 工具名限定为客户端支持的固定集合，未知项归入 `Other`。
- Nginx 按来源 IP 限流；服务端限制 16KB 请求体并设置 GitCode 请求超时。
- 中继只提供健康检查和报告写入，不提供 GitCode token、任意路径写入或通用代理能力。

## Client Behavior

- 默认启用社区统计时，启动 30 秒后自动上报，之后每小时一次。
- “立即同步”只作为重试入口，不是正常流程必需步骤。
- 自动上报失败会保留最近错误，社区页面直接显示原因，不再静默停留在“等待首次同步”。
- 中继地址从 `Info.plist` / Go 编译配置读取，macOS 与 Windows 行为一致。

## Verification

- 中继单元测试覆盖新建、更新、错误密钥、旧身份升级和输入校验。
- 客户端单元测试覆盖凭据生成、自动换 ID、成功和错误状态。
- VPS 上验证健康检查、新用户上报、同用户更新、错误密钥被拒绝，并从 `community-data` 分支读回真实数据。
