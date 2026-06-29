# 变更日志

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。  
仅记录面向用户/运维的 notable 变更；完整 diff 见 GitHub Releases。

## [1.1.0] - 2026-06-30

对齐上游 `@remnawave/node` v2.8.0。

### 重大变更

- **移除 Vision 模块**：上游 2.8.0 删除 `/vision/block-ip`、`/vision/unblock-ip`，IP 封禁能力转由 nftables 插件承担；本版同步移除相关路由、xray RoutingService 动态规则封装与内部证书链路。
- **Xray gRPC API 改用抽象 Unix 套接字**：内部 API inbound 从 `dokodemo-door + 127.0.0.1:XTLS_API_PORT + mTLS` 改为 `tunnel + @abstract-socket`（对齐 2.8.0），不再监听本地 TCP 端口、不再生成内部 mTLS 证书；`XTLS_API_PORT` 配置项废弃。**要求 rw-core ≥ v26.6.27**。

### 新增

- **插件 AS 列表（asList）**：`sharedLists` 支持 `type: asList`，将 AS 号解析为 IPv4/IPv6 CIDR 前缀后注入 nftables / torrent-blocker 规则。ASN 数据取自 `/usr/local/share/asn/asn-prefixes.bin`（缺失则优雅降级为空）；新增 `cmd/asn-builder` 从 ip2asn 数据集生成该库；安装脚本支持 `ASN_DB_URL` 可选下载。

### 修复

- **重叠 CIDR 致插件失效**：ingress/egress 过滤的 nftables set 改用 `flags interval`，并在写入前去重、合并重叠区间，修复携带 CIDR 的共享列表整批加载失败、以及此前 CIDR 被静默丢弃的问题（对齐上游 2.8.0）。
- **nftables 表首启幂等**：`recreateTables` 在 `delete table` 前补幂等 `add table`，避免全新主机首次启动时因删除不存在的表导致 `nft -f` 原子事务整体回滚、过滤表/set 建不出。
- **安装菜单版本标签**：`install-node.sh` / `install-node-alpine.sh` 菜单残留的 `(contract 2.7.0)` 更正为 `2.8.0`。

### 维护

- rw-core 默认版本升级至 **v26.6.27**（`install-xray.sh`）。
- 契约基线对齐 v2.8.0（`contract.version`、contract-sync CI、26 条 REST API）。
- 新增 `.gitattributes` 强制 `*.sh` / `*.service` / `*.openrc` 使用 LF 行尾，避免 CRLF 提交导致部署脚本在 Linux 失效。

## [1.0.2] - 2026-06-10

### 安全

- **JWT 身份 claim**：当 token 含 `iss`/`aud`/`sub` 时校验 Panel 约定值（`remnawave` / `remnawave-node` / `remnawave-backend`）；无 claim 的旧 token 仍可通过。
- **Vision IP 校验**：`block-ip` / `unblock-ip` 拒绝非合法 IP。
- **内部 Token 为空**：Unix 内部 API 在 token 未配置时一律拒绝。
- **默认 body limit**：非 low-memory 默认由 1000MB 降为 **256MB**（可用 `BODY_LIMIT_MB` 覆盖）。

### 新增

- **`NODE_BIND_ADDR`**：可选绑定地址（如 `127.0.0.1`），默认仍监听全部接口。

### 改进

- Webhook JSON 解析失败时写 warn 日志。

## [1.0.1] - 2026-06-10

### 安全

- **内部 Token 不再出现在进程参数**：rw-core `-config` URL 与 torrent webhook URL 移除 `?token=`；鉴权改为 Unix socket `0600` + 可选 `X-Internal-Token` 头；`?token=` 仍兼容旧版。
- **Unix socket 权限**：`internal.sock` 创建后强制 `chmod 0600`。
- **zstd 解压炸弹防护**：压缩体上限 64MB，解压后再次限制为 body limit。
- **`/node/xray/stop`**：新增 **POST** 为推荐方法；GET 保留兼容并记录弃用日志。

### 维护

- 删除未使用的 `config.randomSocketPath()`。

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

[1.1.0]: https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v1.1.0
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
