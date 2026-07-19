# 变更日志

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。  
仅记录面向用户/运维的 notable 变更；完整 diff 见 GitHub Releases。

## [0.1.0] - Unreleased

这是 `Luxiaba/remnawave-node-lite-go` 的首个自有版本线，兼容目标固定为官方 Node 2.8.0 与 Panel 2.8.1。

### 新增

- 新增 GHCR 多架构镜像发布链：tag Release 在既有门禁后发布 amd64/arm64 manifest、精确版本与 commit 标签、SBOM、BuildKit provenance 和 GitHub build attestation；主分支/PR 独立验证容器构建。
- 新增 amd64/arm64 多阶段 Docker 镜像与生产 Compose：固定并校验 rw-core/geo/ASN 资产，采用官方 host network 与能力模型，同时落实 448 MiB/no-swap/1 CPU/256 PID、只读 rootfs、健康检查和日志上限。
- 固化官方 Node `2.8.0@596f015` 的 26 条路由、Zod 请求/响应、错误格式和副作用为可执行契约。
- 新增默认只读、需 mTLS/JWT 的 `contract-probe`，用于官方 Node 与 Go Node 的黑盒语义差分。
- 新增统一 Node API 边界，覆盖 Zod 等价的必填字段、联合类型、UUID/IP、枚举、nullable/default 和数组长度校验。
- 新增 Linux network namespace nftables 与 socket-kill 集成门禁，真实覆盖双栈规则替换、封禁、解封、重建、退出清理和 TCP 连接关闭。
- 新增固定官方 JSON 摘要的 ASN 构建链，Release 同时发布 compact `asn-prefixes.bin` 与 `SHA256SUMS`。
- 新增 `448 MiB / 1 CPU / no-swap` 真实 rw-core 资源门禁；M6 工程基线的 50k 用户场景峰值为 `143.9 MiB`，M8 冻结候选仍须重跑。

### 安全

- JWT header 与 claims 必须各自只包含一个完整 JSON 值；签名有效但附带第二个 JSON 值的畸形 token 不再被接受。
- 外部传输最低版本收敛为 TLS 1.3，并禁用 HTTP/2；无效 JWT、未知路由和错误 method 与官方一致地直接销毁连接。
- systemd/OpenRC 改用专用 `remnanode` 用户，只保留 `CAP_NET_ADMIN` 与 `CAP_NET_BIND_SERVICE`；systemd 同时启用 capability bounding、sandbox、448 MiB/no-swap/1 CPU/256 tasks 限额。
- Release archive、rw-core、自定义 core 与 ASN 资产均在写盘前校验 SHA-256、结构和版本；固定 rw-core 摘要不可覆盖，GitHub Actions 固定到完整 commit SHA。
- systemd/OpenRC 通过空环境启动，`node.env` 与 Secret 均由 Go 使用 `O_NOFOLLOW|O_NONBLOCK` 的同一文件描述符有界读取；符号链接、FIFO、device、超限或读取期间变化会在启动前失败。
- 安装器拒绝受管路径中的不安全 owner、权限、符号链接和硬链接；日志 helper、rw-core、geo 与 ASN 使用同目录 staging 原子替换，service 更新则由外层升级事务备份和验证。
- 安装、升级、rw-core 安装与卸载共用固定内核锁；嵌套入口复用同一锁 FD。同步包管理、文件和 service mutation 持锁到子进程结束；下载、Node/rw-core 自检、状态查询和可能派生常驻服务的 OpenRC 启动链不继承该 FD。Alpine 入口显式依赖 `util-linux`。

### 修复

- 路由测试改为校验真实 dispatcher 注册表；`/node/xray/stop` 收敛为官方定义的仅 GET，不再错误接受 POST。
- stats、handler、plugin 与 Xray start 不再吞掉 JSON 解码和类型错误；畸形、尾随或不完整请求会在任何 provider、进程、nftables、连接和状态副作用前返回 400。
- 已知应用错误补齐官方要求的 `timestamp`、`path`、`message` 与 `errorCode`，底层 SDK 错误不再替换官方 A001/A010-A017 文案。
- 对齐官方边界细节：未知对象字段剥离、`forceRestart` 默认 false、空字符串与无最小长度数组、五种用户联合类型、数值型 nftables timeout。
- Xray 启动、停止、健康检查和自然退出改为显式四态生命周期；stop 可取消正在启动的 core，失败/超时不再提交配置或 hash，所有子进程均被回收。
- 移除非官方的 `last-start.json` 持久化与开机旧配置恢复；Node 重启后由 Panel 健康检查重新下发 start，`healthcheck` 只读缓存状态。
- Panel stop 固定先确认 core 停止再清理插件；停止失败时保留插件快照与 nft 规则，避免运行中的 core 出现无过滤窗口。
- Linux 将 rw-core 置于独立进程组，SIGINT、超时 SIGKILL 和 leader 自然退出后的兜底清理覆盖整个进程组；parent-death signal 保护直接子进程，Node 或 supervisor 自身被强杀后通过重启或重新部署恢复。
- 插件同步改为不可变 plan 的 `apply -> Xray reconcile -> commit` 事务；nft/Xray 失败不再提前提交状态，并会尝试恢复上一份 firewall plan。`plugin sync/recreate` 与 `xray start/stop` 共用应用层 lifecycle gate，消除 core 启动配置与插件快照竞态。
- nftables 初始化、双栈批处理、ingress/torrent 解封、recreate 重放、错误传播和退出清表统一收口；缺失元素的多种 nft 错误文案均按幂等成功处理。
- nft 不可用时合法配置仍按官方语义接受，但 torrent effective state 保持禁用；reset 不再丢弃未 collect reports，ASN/shared list 降级会写入明确日志。
- listener 异常不再从 goroutine 调用 `log.Fatalf` 跳过清理；统一关闭路径先停止 rw-core，再删除本项目 nftables 表。
- 用户热更新改为可取消的串行 mutation；只有 rw-core RPC 成功且 Xray generation 未变化时才提交 inbound hash，清理失败不再继续添加该用户，批量部分失败会返回真实错误并保持可重试。
- 连接踢除会规范化并去重 IP，保护非法、特殊、本机和白名单地址；缺少 capability、IP 查询失败或任一 `NETLINK_SOCK_DIAG` socket destroy 失败不再伪报成功。
- `get-users-ip-list` 优先使用单次批量 RPC；旧 core 只在 `UNIMPLEMENTED` 时降级到最多 8 个固定 worker，并缓存 capability，消除 N+1 无界 goroutine。
- 所有内部 Handler/Stats unary gRPC 调用增加取消传播和有界 deadline；默认 5 秒，健康探测 3 秒，批量 legacy 查询共享总预算。
- Xray webhook 改为 64 条有界等待队列和单 worker；容量超时、取消或关闭会明确返回 503，插件关闭使用不可逆 admission fence，超时或 nft 清理失败后拒绝新 mutation 并允许 Close 重试。
- 整机退出改为共享 25 秒预算；后台版本探测可取消并等待，rw-core 确认停止后才清理 nft 表，避免独立 timeout 累加越过 service manager 的 TERM grace。
- 公开 `xray/stop` 串行化 start/stop，并只在 core 停止成功后 reset 插件；停止失败不再提前撤销 nft 过滤。
- 重复执行安装脚本会进入同一可回滚升级事务；坏 systemd/OpenRC service、binary/support/node.env/rw-core 写入失败均恢复升级前文件、开机注册和运行状态，恢复不完整时保留唯一备份并明确失败。
- rw-core 安装按 installer、core、geo 与 ASN 的实际目标文件系统分别聚合 staging/备份峰值；任一挂载空间不足会在替换资产前失败。
- CLI 只有零参数会进入 daemon；未知或多余参数直接失败。Unix socket 启动拒绝 live、symlink 与非 socket 路径，退出时只删除当前实例实际拥有的 socket。
- 卸载不再按进程名终止任意 `rw-core`，也不再删除通用 Xray 路径，只清理本项目私有进程、socket、nftables 表与 `/usr/local/{lib,share}/remnanode`。
- 非交互安装未提供 Secret Key 时会完成落盘但保持服务停止，不再错误等待未启动服务的端口。
- 所有安装/升级包装入口的 `--dry-run` 保持零写入；路径型 Release tag 在 bootstrap 和事务开始前拒绝，service/core 始终取自目标 Release 的已校验 support。
- 旧版通用 Xray/geo/ASN 路径仅在对应私有资产安装成功后迁移；默认保留 core 的升级不再把可用配置改向空路径。

### 维护

- 从参考仓库 `ike-sh/remnawave-node-lite-go@0821988` 建立干净基线。
- Go module、安装脚本、发布地址和文档归属切换到本仓库。
- 建立行为兼容、架构修复和 512 MiB 小内存验收路线。
- 契约 CI 验证固定官方提交、版本和所有引用的源码证据文件。
- 发布门禁绑定冻结候选 commit、严格 JSON 验收证据、兼容/资源/故障结果和只允许发布文档变化的两阶段流程。
- HTTP transport 与 stats、用户 handler、plugin 业务服务分离，业务层不再依赖 `net/http` 或自行解码 JSON。
- 固定并校准外部 `@remnawave/node-plugins@0.4.5` schema 证据，覆盖显式 null、AS number、`ext:` 与数值边界。
- 用最小 rw-core protobuf wire client 替换完整 Xray Go module，双架构二进制缩小约 30%。
- M7 已在 Ubuntu 24.04/systemd 与 Alpine 3.22/OpenRC 完成全新安装、升级回滚、启停、专用用户/capability、日志、磁盘和卸载隔离工程基线；它不替代冻结候选的 M8 验收。

## 参考仓库历史

以下记录继承自参考仓库，仅用于追溯基线，不代表本仓库发布过这些版本。

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
