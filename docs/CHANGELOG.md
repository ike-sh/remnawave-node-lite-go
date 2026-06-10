# 变更日志

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。  
仅记录面向用户/运维的 notable 变更；完整 diff 见 GitHub Releases。

## [1.0.0] - 2026-06-10

### 正式版

- **v1.0.0 稳定发布**：Panel 2.7.x 主流程生产验证通过（全新安装、升级、reboot 自动恢复）。
- **文档整理**：移除内部开发/分析文档（`docs/dev/`），README 与安装提示面向生产用户重写。
- **安装提示更新**：反映 v0.8.28+ Panel 10s 健康检查自动上线，不再要求手动禁用→启用。

功能与 v0.8.30 代码等价，无行为变更。

## [0.8.30] - 2026-06-10

### 改进

- **`GOMEMLIMIT` 内化**：仅 `LOW_MEMORY=1` 时进程自动设 180MiB 软上限；systemd/OpenRC 不再默认注入，大节点不会被误限。可用 `GOMEMLIMIT` 环境变量覆盖。
- **rw-core 日志轮转**：`xray.out.log` / `xray.err.log` 达 10MB 自动轮转（保留一份 `.1` 备份），防止小盘 VPS 日志打满。
- **网卡速率轮询**：`/proc/net/dev` 采样间隔 1s → **3s**，降低空闲 CPU 唤醒。
- **配置 JSON 缓存**：internal unix socket `get-config` 复用 `xray/start` 时序列化结果，避免每次 rw-core 轮询全量 re-marshal。
- **`xray version` 探测优化**：版本已知后 health 检查不再每次 fork 子进程；仅在未知或 core 重启后刷新。

### 修复

- **网卡计数器回绕**：rx/tx 字节回绕或接口重置时跳过异常采样，避免 Panel 显示离谱速率。

## [0.8.29] - 2026-06-10

### 新增

- **`CUSTOM_CORE_URL`**：`install-xray.sh` 支持从自定义 URL 下载 rw-core（对齐官方 Docker entrypoint）；可写入 `node.env`。
- **geo-zapret 支持**：`GEO_ZAPRET_FILE` / `IP_ZAPRET_FILE` 安装时复制到 `GEO_DIR`；`doctor` 检测可选 zapret 文件。

### 修复

- **gRPC 启动等待**：`waitForGRPC` 轮询间隔 500ms → **2s**（对齐官方 pRetry minTimeout）。
- **Stats 在线/IP 语义**：`get-user-ip-list` / `get-users-ip-list` gRPC 失败时返回 **200 + 空列表**（对齐官方）；`get-user-online-status` provider 不可用时返回 200 false。

## [0.8.28] - 2026-06-10

### 修复

- **首次安装后 Panel 不上线（关键）**：`get-system-stats` 在 rw-core 离线时改为返回 `500 A010`（对齐官方 node），不再返回 `200 + xrayInfo:null`。Panel `NodeHealthCheckQueueProcessor` 据此走 `handleDisconnectedNode` 并每 10s 触发 `startNode`，无需手动禁用→启用。

### 改进

- 安装脚本新增 Panel 接入前置提示、`wait_for_service_stable` 就绪检测，README 补充推荐安装顺序。

## [0.8.27] - 2026-06-10

### 修复

- **`/node/xray/stop` 未清理插件状态**：对齐官方 `withPluginCleanup: true`，Panel 禁用节点时先 `ResetPlugins()`（清空 plugin state + nftables 插件表），再停止 rw-core。

## [0.8.26] - 2026-06-10

### 修复

- **RestoreOnBoot 单次失败即放弃**：对齐官方 `pRetry`，启动恢复 rw-core 失败时重试 10 次（间隔 2s），避免慢启动 VPS 重启后永久离线。
- **关机前未落盘 last-start.json**：进程 SIGTERM 退出时若内存中仍有上次 start 配置，额外 flush 到磁盘（`Stop(false)` 安全网）。
- **doctor 自检**：新增 `last-start.json` 存在性检查，便于排查「从未 xray/start 成功」导致的无法自动恢复。

## [0.8.25] - 2026-06-10

### 修复

- **服务器重启后无法自动上线**：v0.8.24 引入的 `last-start.json` 在进程收到 SIGTERM 退出时被 `Stop()` 误删，导致 `RestoreOnBoot` 找不到持久化配置。现仅 Panel 调用 `/node/xray/stop`（禁用节点）时清除持久化；正常关机/重启保留配置以便自动恢复 rw-core。

## [0.8.24] - 2026-06-09

### 修复

- **重启后需手动禁用/启用节点**：成功 `xray/start` 后将配置持久化到 `/var/lib/remnanode/last-start.json`，进程启动时自动恢复 rw-core（与官方节点重启后 Panel 自动恢复行为对齐）。
- **healthcheck 误报在线**：`/node/xray/healthcheck` 改为实时 gRPC Ping（对齐官方 `getSysStats` 探测），不再仅返回内存缓存的 `xrayOnline`。
- `xray/stop` 时清除持久化配置，避免禁用节点后重启仍自动拉起 core。

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

[1.0.0]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v1.0.0
[0.8.30]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.30
[0.8.29]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.29
[0.8.28]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.28
[0.8.27]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.27
[0.8.26]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.26
[0.8.25]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.25
[0.8.24]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.24
[0.8.23]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.23
[0.8.22]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.22
[0.8.21]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.21
[0.8.20]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.20
[0.8.19]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.19
