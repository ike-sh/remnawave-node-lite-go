# Remnawave Node 2.8.0 行为契约基线

## 证据边界

本文件与 `internal/contract` 共同描述本项目的兼容目标。唯一官方代码基线是：

- 仓库：`https://github.com/remnawave/node.git`
- 版本：`2.8.0`
- 提交：`596f015a5c8f876dc9a9d61b6cb78d35bd8e379b`
- Panel 集成目标：`2.8.1`

路由方法取自四个官方 controller；请求和响应取自 `libs/contract/commands` 下的 Zod schema；应用错误取自 `libs/contract/constants/errors` 和 `HttpExceptionFilter`。CI 会验证官方 Git 提交、包版本以及所有引用的证据文件。

插件 `config` 的 schema 不在 Node 仓库内定义，而是来自官方 lockfile 固定的 `@remnawave/node-plugins@0.4.5`。审计证据为 npm tarball `node-plugins-0.4.5.tgz`，SHA-1 `3bfc3988278790ec40a93d6e6169f893c31bf62d`，SHA-512 integrity `sha512-r9Lce/l/kHQATNhWbcutApFSJ5hH/Yu6Kv0+/qjpUDIEa1+DFb54Q8IwuvqWzxxbGkG9oO0cAeN4busBzz0a5Q==`；Go 端插件校验以其中 `build/backend/models/node-plugins.schema.js` 为准。

## 通用语义

- 外部 API 使用双向 TLS；官方最低版本为 TLS 1.3。
- 所有 `/node` 路由使用 RS256 Bearer JWT。官方在认证失败时销毁 socket，不返回 HTTP body。
- 官方对未知路由同样销毁 socket。
- 有请求 DTO 的接口使用 Zod 校验；对象的未知字段会被剥离，而不是拒绝。
- DTO 校验失败返回 HTTP 400：`statusCode=400`、`message="Validation failed"`、`errors=[...]`。
- 成功响应统一返回 HTTP 200，顶层为 `{ "response": ... }`。
- 已知应用错误包含 `timestamp`、`path`、`message` 和 `errorCode`；当前已定义 A001-A017 中的相关错误。
- 未映射的 Nest 异常使用通用 `statusCode`、`message`、可选 `error` 响应。
- 本项目可因资源保护设置更小的请求上限，但偏差必须明确、可观测且不得在校验前产生副作用。

## Go API 边界实现

M2 已将 20 个带请求 DTO 的路由统一接入 `internal/nodeapi`。解码器只接受一个 JSON 文档，保留 Zod object 的未知字段剥离语义，并为缺字段、错类型、联合类型 discriminant、UUID/IP、枚举、nullable/default 和 `minItems` 生成统一验证响应。6 个官方无请求 DTO 的路由继续忽略 body。

`internal/httpserver` 只负责 decode、validate、command 映射和 response envelope；stats、用户 handler 与 plugin 服务不再接收 `http.ResponseWriter`、`*http.Request` 或自行解码 JSON。Xray 配置直接解码为一份 map 后转交 manager，不经过 RawMessage 和二次反序列化。

transport 测试为 provider、连接 dropper、plugin service 和 Xray manager 注入计数 spy，验证每类非法请求的调用数为 0；合法请求则经过真实 dispatcher 后由独立官方响应 schema 再次校验。

## Go Xray 生命周期实现

M3 以官方 `src/modules/xray-core/xray.service.ts`、`xray-process.service.ts`、`xray.module.ts` 和应用关闭钩子为行为依据：Node 启动时 Xray 缓存状态为离线，不从磁盘恢复旧 Panel 配置；`healthcheck` 只读取缓存状态；Panel stop 同时承担停止 core 和清理插件的语义。Go 实现先确认 core 已停止再重置插件，避免停止失败时提前撤销过滤规则；应用退出采用相同顺序。

Go manager 使用单一显式状态，而不是多个可形成非法组合的布尔量：

| 当前状态 | 事件 | 后续状态 | 提交语义 |
| --- | --- | --- | --- |
| `stopped` | 接受 start | `starting` | 只暂存供新进程拉取的 pending config |
| `running` | 接受需重启的 start | `starting` | 先回收旧进程，再暂存新配置 |
| `starting` | gRPC 就绪且进程仍存活 | `running` | 原子提交 active config、hash 和 inbound tag |
| `starting` | 取消、超时、spawn/进程失败 | `stopped` | 回收进程并清空 pending/active 状态 |
| `starting` | stop | `stopping` | generation 失效并取消 start，由 stop 接管进程 |
| `running` | stop 或自然退出 | `stopping` / `stopped` | 回收进程并清理配置与 hash 状态 |

Manager 内部的生命周期修改由 operation mutex 保护，状态发布与所有权交接在 manager mutex 下完成。HTTP coordinator 为 start 提供共享 lease，并用独立的两个 handler 槽限制同时保留的配置；因此第二个并发 start 可以进入 Manager 并立即得到官方兼容的 `Request already in progress`。stop、plugin sync/recreate 使用独占 lease，等待中的独占操作阻止后续 start 插队。每个已接受的 start 使用单调 generation，旧操作不能覆盖新 stop；所有成功 spawn 的子进程都由唯一 `Wait` goroutine 回收。Linux 将 rw-core 启动在独立 process group 中并设置 parent-death signal；正常 stop 的 SIGINT、超时后的 SIGKILL 以及组长自然退出后的兜底清理都作用于整个进程组。parent-death signal 只直接保护组长，Node 硬崩时后代进程的最终清理仍依赖 systemd cgroup/OpenRC supervisor，后续发布阶段必须在两种发行环境用 wrapper + child 实测，不能把该信号表述为对所有后代的单独保证。

进程级测试覆盖 pending 到 active 的提交边界、并发 start、start 与 stop 交错、context cancel、启动超时、就绪前后退出、自然退出、并发/重复 stop、SIGINT 与 SIGKILL 升级。Linux 测试额外固定独立进程组、整组信号与后代清理。路由测试覆盖 start 共享进入、stop/plugin 独占等待、等待取消、Panel stop 的 `Stop -> ResetPlugins` 顺序，以及 Stop 失败时保留插件快照和 nft 规则。

## Go 插件与 nftables 实现

M4 以官方 `plugin.service.ts`、`nft.service.ts`、`plugin-state.service.ts`、torrent blocker state/webhook handler，以及 `@remnawave/node-plugins@0.4.5` 为行为依据。每次变更先从已校验配置构建不可变 plan，一次完成 shared list/ASN 展开、connection-drop whitelist、torrent effective state 和 firewall plan。启用或更新时先应用 firewall 再协调 Xray；关闭、清理和破坏性 recreate 则先协调 Xray，再重置 firewall；两侧成功后才提交 snapshot。失败不发布不匹配的新状态，并在可回滚路径重放上一份 firewall plan，使同一 Panel 请求可以安全重试。

Initialize、sync、reset、block、unblock 与 recreate 通过容量为 1、支持 context 取消的 operation gate 串行化。HTTP 应用层使用 shared-start/exclusive-mutation lifecycle coordinator，固定锁序为 `Xray lifecycle lease -> Plugin operation gate -> Manager`，防止 core 启动读取配置期间插件快照变化；未来新增绕过 HTTP 的内部入口时也必须复用该协调器。无 `includeRuleTags` 的 torrent blocker 关闭会热删除 `RW_TB_OUTBOUND_BLOCK`，不停止在线 core；健康态 `recreate-tables` 只重建 nftables，只有从 degraded firewall 恢复并使 torrent blocker 重新生效时才停止 core。webhook 接收不直接等待 operation gate，而是在内部请求的 30 秒 deadline 内等待最多 64 条的有界队列容量，再由单 worker 获取同一 gate 后执行 nft/report 副作用；容量未恢复、请求取消或服务关闭时返回 `503 + Retry-After`，不会把未接纳事件伪报为成功。collect 只在 State 锁下原子 drain 报告。nftables 初始化与 Go 对象构造分离；缺少 `CAP_NET_ADMIN` 或 nft 初始化失败时仍接受合法插件配置，但 ingress/egress/torrent 保持不可用，torrent 状态不会错误注入 Xray。reset 只替换插件快照，不丢弃 Panel 尚未 collect 的 torrent reports；recreate 重放当前已提交过滤计划，而不是错误地创建空表。

Close 先设置不可逆的 mutation admission fence 并停止 webhook worker；此前已经接纳的 mutation 可以完成，新 mutation 一律拒绝。等待 gate、删除 nft 表和 join worker 共享调用方 deadline，并由服务再限制为最多 15 秒；清理失败保留已提交快照，只允许后续 Close 重试，不会重新开放业务操作。

nft backend 在单个 `nft -f` 原子事务内替换 IPv4/IPv6 私有表和过滤元素，批量处理 block，unblock 同时覆盖 torrent/ingress 的双栈 set。进程退出先停止 rw-core，再删除 `remnanode`/`remnanode6`，listener 失败也会进入同一清理路径。Linux network namespace 集成测试真实覆盖初始化、两次 plan 替换、双栈 block、重复 block/unblock、recreate 和 close；M4 checkpoint 已在 Linux arm64 6.8 内核与 nftables 上通过，amd64 测试二进制已交叉编译并纳入同一 CI 门控。

## Go 用户、连接与统计实现

M5 将所有用户 mutation 放入可取消的串行 gate。每次 add/remove 先完成 rw-core Handler RPC，只有 RPC 成功且 Xray generation 未变化时才更新本地 inbound hash；清理失败时不继续为该用户添加新账号，批量请求的任一失败都会返回 `success=false` 和首个明确错误。多个远端 RPC 不能形成真正的跨调用事务，已经成功的前序操作不会伪装成回滚，但本地状态不会领先于 rw-core，同一 Panel 请求可以安全重试。

连接踢除会先规范化和去重 IP，跳过白名单，并拒绝非法地址、unspecified、loopback、link-local、multicast、IPv4 广播和本机接口地址。每批通过 `NETLINK_SOCK_DIAG` 分别枚举 IPv4/IPv6 socket，并逐条校验 `SOCK_DESTROY` ACK；仅 `ENOENT` 作为幂等成功，缺少 `CAP_NET_ADMIN`、用户 IP 查询失败或任一销毁失败都会返回真实的 `success=false`。CI 在独立 Linux network namespace 中建立真实 TCP 连接并验证 socket 被关闭；M5 checkpoint 已在 Linux arm64 6.8 内核上通过该门禁。

`get-users-ip-list` 优先使用 rw-core 的单次 `GetUsersStats` 扩展 RPC；旧 core 返回 `UNIMPLEMENTED` 后会缓存 capability 并降级为最多 8 个固定 worker，不再为每个在线用户创建 goroutine。所有 Handler/Stats unary RPC 默认共享最多 5 秒 deadline，健康探测为 3 秒，调用方已有的更早 deadline 和取消信号保持生效；legacy 批量查询使用单一总预算，不会为每个用户重新续期。

## Go 资源预算实现

M6 在不改变官方 HTTP 契约的前提下收紧资源边界。Xray 配置仅在启动阶段保留解码树和规范 JSON，rw-core ready 后只留下 hash、inbound tag 与运行状态；torrent reports 使用 1024 条有界环形队列；zstd decoder、窗口、并发、请求体和 gRPC 响应均有明确上限。完整 Xray Go module 已由与官方生成类型校准的最小 protobuf wire client 替代，五种账号、Handler 请求、Stats 消息和确定性 wire golden 共同固定兼容性。

`LOW_MEMORY=1` 时默认请求体上限为 16 MiB，Go heap 软上限为 180 MiB。显式 `BODY_LIMIT_MB` 允许 `1..1024`，非法、负数或溢出值会使进程启动失败，而不是静默回退。Debian 与 Alpine 安装器在整机内存不超过 512 MiB 时自动启用该模式。生产 init 固定读取 `/etc/remnanode/node.env`，不回退到 service-writable working directory；配置必须是普通非符号链接文件，总计不超过 1 MiB、4096 行和 256 个赋值。配置与 Secret 都通过同一 `O_NOFOLLOW|O_NONBLOCK` 文件描述符完成检查和有界读取。systemd/OpenRC 均不导出整份配置环境，`GOMEMLIMIT` 与版本 override 由同一个 Go 解析器验证后应用。

真实 rw-core `v26.6.27` 的 1 CPU / 448 MiB / no-swap 门禁覆盖 1k 用户启动、无变化同步、50k 用户重启、热增删与统计 RPC，实测 cgroup 峰值为 143.9 MiB。复现条件和阶段数据见 [`resource-budget.md`](resource-budget.md)。

## Go 传输、系统与供应链实现

M7 将外部 TLS 最低版本收敛为 1.3，并禁用 Go HTTP/2 自动协商以保持官方连接处理模型。无效 JWT、未知路由和错误 HTTP method 均直接终止底层连接，不返回可枚举的 401/404/405 body；请求头上限为 64 KiB。真实 TLS 客户端测试固定了正常复用、认证失败和未知请求后的连接销毁语义。

systemd 与 OpenRC 均使用专用 `remnanode:remnanode` 账号，配置为 `root:remnanode 0640`，状态和日志目录为 `remnanode:remnanode 0750`。服务只获得 `CAP_NET_ADMIN` 与 `CAP_NET_BIND_SERVICE`；systemd 同时将 bounding set 收紧到这两项，并启用 `NoNewPrivileges`、只读系统、namespace/syscall/address-family 限制、`448 MiB` 内存、零 swap、1 CPU 和 256 tasks。Alpine 3.22 的 supervise-daemon 实测 `CapInh/Prm/Eff/Amb=0x1400`、`NoNewPrivs=1`，且由服务派生的 `nft` 子进程可创建私有表。

项目资产位于 `/usr/local/lib/remnanode` 和 `/usr/local/share/remnanode`，不再接管通用 Xray 路径。Release 归档、rw-core zip、自定义 core 与 ASN 数据都必须通过 SHA-256 和结构/版本自检后才写盘；固定 rw-core `v26.6.27` 不允许覆盖其已审计摘要。升级会备份 binary、service、support、`node.env` 以及可选 rw-core 资产，刷新后必须重新通过服务与端口门禁，否则自动逐项恢复。Ubuntu/systemd 与 Alpine/OpenRC 的坏 service 注入均验证了摘要和运行状态恢复；完全卸载也验证了不会终止无关同名进程或删除通用 Xray 文件。

整机退出使用一个共享的 25 秒应用预算，而不是为各组件串行重置 timeout。HTTPS/Unix intake、日志轮转和后台版本探测先收到取消；rw-core 最多使用 5 秒 SIGINT 加 5 秒 SIGKILL，确认 core 停止后，插件再使用剩余预算清理私有 nft 表。core 或插件的瞬时清理错误会在同一 deadline 内重试一次。公开 `xray/stop` 也串行化 start/stop，只有 core 确认停止后才 reset 插件；Stop 失败会保留规则与快照。systemd 提供 30 秒 TERM grace，OpenRC 提供 `TERM/30/KILL/5` 外层兜底；deadline 或清理失败会形成聚合错误，不能被记录为优雅退出成功。

## 路由清单

表中只列核心约束；完整类型、nullable、枚举、UUID、IP、日期和数组长度约束以 `internal/contract/official_schemas.go` 的可执行 schema 为准。

| 方法 | 路径 | 请求核心 | 响应核心 | 主要副作用或错误 |
| --- | --- | --- | --- | --- |
| POST | `/node/xray/start` | `internals.hashes`、`xrayConfig`；`forceRestart` 默认 false | `isStarted`、nullable `version/error`、节点和系统信息 | 启动或替换 rw-core，替换配置和 hash 状态；启动失败写入响应内的 RN-001 |
| GET | `/node/xray/stop` | 无 body | `isStopped` | 停止 rw-core，并清理插件状态和规则 |
| GET | `/node/xray/healthcheck` | 无 body | `isAlive`、缓存状态、nullable Xray 版本、Node 版本 | 只读缓存和进程状态 |
| POST | `/node/stats/get-user-online-status` | `username` | `isOnline` | 查询在线状态；SDK 错误降级为 false |
| POST | `/node/stats/get-users-stats` | `reset` | `users[]` 流量 | `reset=true` 清零计数；A011 |
| GET | `/node/stats/get-system-stats` | 无 body | nullable `xrayInfo`、插件和系统统计 | 查询 rw-core/宿主机；A010 |
| POST | `/node/stats/get-inbound-stats` | `tag`、`reset` | inbound 流量 | 可清零计数；A012 |
| POST | `/node/stats/get-outbound-stats` | `tag`、`reset` | outbound 流量 | 可清零计数；A013 |
| POST | `/node/stats/get-all-outbounds-stats` | `reset` | `outbounds[]` | 可清零计数；A016 |
| POST | `/node/stats/get-all-inbounds-stats` | `reset` | `inbounds[]` | 可清零计数；A015 |
| POST | `/node/stats/get-combined-stats` | `reset` | `inbounds[]`、`outbounds[]` | 可清零计数；A017 |
| POST | `/node/stats/get-user-ip-list` | `userId` | `ips[]`，含 ISO date-time | 查询并重置单用户 IP 统计 |
| GET | `/node/stats/get-users-ip-list` | 无 body | `users[].ips[]` | 查询已知用户 IP 统计 |
| POST | `/node/handler/add-user` | `data[]` 联合类型、`hashData.vlessUuid` | `success`、nullable `error` | 增加用户并更新 inbound hash |
| POST | `/node/handler/remove-user` | `username`、UUID hash | `success`、nullable `error` | 读取并踢除连接，然后删除用户和 hash |
| POST | `/node/handler/get-inbound-users-count` | `tag` | `count` | 查询 rw-core；A014 |
| POST | `/node/handler/get-inbound-users` | `tag` | `users[]` | 查询 rw-core；A014 |
| POST | `/node/handler/add-users` | `affectedInboundTags[]`、`users[]` | `success`、nullable `error` | 批量增加用户并替换受影响 hash |
| POST | `/node/handler/remove-users` | `users[]`，每项含 userId/UUID | `success`、nullable `error` | 踢除连接并批量删除用户/hash |
| POST | `/node/handler/drop-users-connections` | 非空 `userIds[]` | `success` | 查询 IP 后终止宿主机连接 |
| POST | `/node/handler/drop-ips` | 非空 `ips[]` | `success` | 终止宿主机连接；官方不要求元素是合法 IP |
| POST | `/node/plugin/sync` | nullable `plugin`；非空时含 config/UUID/name | `accepted` | 替换或清空插件状态，协调 nftables 和 rw-core |
| POST | `/node/plugin/torrent-blocker/collect` | 无 body | 完整 `reports[]` | 原子取走并清空报告队列 |
| POST | `/node/plugin/nftables/block-ips` | `ips[]`，元素为合法 IP 和数值 timeout | `accepted` | 写入定时封禁并踢连接 |
| POST | `/node/plugin/nftables/unblock-ips` | 合法 IP 数组 | `accepted` | 删除插件表内的封禁 |
| POST | `/node/plugin/nftables/recreate-tables` | 无 body | `accepted` | 重建并重新填充插件 nftables 表 |

## 请求联合类型

`handler/add-user` 的 `data[]` 只接受以下 discriminant：

- `trojan`：tag、username、password
- `vless`：tag、username、uuid、flow；flow 只能是 `xtls-rprx-vision` 或空字符串
- `shadowsocks`：tag、username、password、cipherType、ivCheck
- `shadowsocks22`：tag、username、password
- `hysteria`：tag、username、password

`handler/add-users` 的 `inboundData[]` 使用同样五种类型；VLESS 额外要求 flow。每个 `userData` 必须包含 userId、hashUuid、vlessUuid、trojanPassword 和 ssPassword。

## 当前已知偏差

M7 已关闭此前记录的 TLS/socket 与系统供应链偏差。当前没有已知的静态 `/node` 契约 P1/P2；M8 仍需以真实 Panel 2.8.1 完成发行候选的端到端差分和故障恢复验收。裸机 systemd/OpenRC 部署替代官方 Docker 运行模型属于本项目明确的运维边界，不扩展 `/node` API。

运行期 `dump-config` 是已接受的后置差异：Manager 只在 rw-core 启动期间保留完整规范配置，ready 后释放该副本并让 `CurrentConfigJSON` 返回 `{}`。这是面向 512 MiB 节点的内存取舍，不影响 `/node` 或 rw-core 启动契约；后续如恢复该诊断能力，必须采用有界方案，不能常驻第二份大配置。

## 本地验证

常规可执行契约测试：

```bash
go test ./internal/contract
```

连同固定官方源码证据一起验证：

```bash
REMNANODE_OFFICIAL_SOURCE=/tmp/remnawave-node-official-2.8.0-codex \
  go test ./internal/contract
```

在具备 root/unshare/nft 的 Linux 验收机上运行隔离防火墙测试：

```bash
sudo env "PATH=$PATH" REMNANODE_NFT_INTEGRATION=1 \
  go test ./internal/plugin -run '^TestNFTManagerInNetworkNamespace$' -count=1 -v
```

测试会验证：26 条 method/path 与真实 dispatcher 完全相同；所有合法请求样例；缺字段、错类型、额外字段、未知联合类型、UUID/IP/minItems；实际 Go handler 的完整成功响应 schema；官方统一错误 schema。

## 黑盒差分入口

列出路由及默认安全级别：

```bash
go run ./cmd/contract-probe -list
```

准备由同一 CA 签发的 Panel 客户端证书，并用第一个 target 作为官方基准：

```bash
export REMNANODE_CONTRACT_CA=/secure/ca.pem
export REMNANODE_CONTRACT_CERT=/secure/panel-client-cert.pem
export REMNANODE_CONTRACT_KEY=/secure/panel-client-key.pem

go run ./cmd/contract-probe \
  -token-file /secure/panel.jwt \
  -target official=https://127.0.0.1:2222 \
  -target candidate=https://127.0.0.1:3222
```

如果证书只包含 DNS 名称而 target 使用 IP，需额外传入 `-server-name <证书名称>`；探针不提供跳过证书验证的选项。

默认只执行 11 条无破坏性请求：健康检查、`reset=false` 的统计和 inbound 用户只读查询。探针比较状态、响应类别、应用错误码和 schema，不比较机器指标、流量值、响应大小、SHA-256 或耗时；报告不包含 JWT 和原始响应 body。

启动/停止、用户增删、连接踢除、IP 统计重置、报告 drain 和 nftables 操作必须同时显式指定 `-routes` 与 `-allow-mutating`，并只应在隔离验收环境执行。
