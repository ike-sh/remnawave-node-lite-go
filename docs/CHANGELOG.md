# 变更日志

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。  
仅记录面向用户/运维的 notable 变更；完整 diff 见 GitHub Releases。

## [0.8.23] - 2026-06-09

### 修复

- **用户流量统计始终 0B**：`GetAllUsersStats` 错误优先调用 rw-core 扩展 `GetUsersStats` RPC，成功但返回空流量，未回退到官方 SDK 使用的 `QueryStats(pattern=user>>>)`。现已与 `@remnawave/xtls-sdk` 对齐，仅走 `QueryStats`。
- **inbound/outbound 流量解析**：计数器名格式为 `inbound>>>tag>>>traffic>>>uplink`，解析误用 `parts[2]`（值为 `traffic`），已改为 `parts[3]`。

## [0.8.22] - 2026-06-09

### 修复

- **xray/start 死锁（关键）**：`PingXrayGRPC` 在 rw-core 启动后、尚未标记 `xrayOnline` 时被 `statsAPI` 的 online 门控拒绝，导致 gRPC 永远 Ping 不通；约 20s 后 lite-go 误杀 rw-core，Panel 显示 `Required info is missing. Outdated version?` 或 `gRPC API ... did not become reachable`。启动阶段 Ping 现已绕过 online 检查。
- **菜单升级半途退出**：从安装脚本菜单选择「升级」时自动向 `upgrade.sh` 传递 `--yes`，避免二次确认在无 TTY 环境下静默取消、版本停留在旧号。

### 验证

- KDDI Alpine 256MB（`131.143.214.101:34541`）升级后 rw-core 持续在线，Panel 节点正常。

## [0.8.21] - 2026-06-09

### 修复

- 低内存模式（`LOW_MEMORY=1` / `--low-memory`）下 gRPC 启动等待由 20s 延长至 90s。
- rw-core 在等待期间异常退出时，错误信息附带进程退出提示及 `xray.err.log` 尾部。
- Alpine 安装脚本：≤512MB 内存自动写入 `LOW_MEMORY=1`。

## [0.8.20] - 2026-06-09

### 修复

- 单独 `curl` 下载安装脚本（未带 helpers）时，自动拉取 `install-env-helpers.sh`，避免 `read_tty` 等函数缺失。

## [0.8.19] - 2026-06-09

### 新增 / 改进

- Alpine `install-node-alpine.sh` 与 Debian 安装脚本对齐：交互菜单（安装 / 升级 / 卸载）、`read_tty` 支持管道安装、OpenRC 服务刷新、`/run/remnanode` 预创建。
- `uninstall.sh`：`--full` 完全卸载、运行时清理（杀 rw-core、清 socket）。

---

[0.8.23]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.23
[0.8.22]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.22
[0.8.21]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.21
[0.8.20]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.20
[0.8.19]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.19
