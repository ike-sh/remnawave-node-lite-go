# remnawave-node-lite-go 分阶段实施计划

> 目标：将 [remnawave/node](https://github.com/remnawave/node)（Node.js + supervisord + Docker）重写为 **Go 单二进制 + 一键安装脚本**，在 Linux 服务器上无需 Docker 即可接入 Remnawave Panel。
>
> 参考分析：`docs/compat-analysis.md`（基于官方 `@remnawave/node` v2.7.0）
>
> 当前版本：`0.7.4` | 估算整体完成度：**~92%**

---

## 零、v0.7.3 状态快照（2026-06）

| 模块 | 状态 |
|------|------|
| 一键安装 + systemd + CI | ✅ |
| 28 条 REST 路由 | ✅ contract 测试覆盖 |
| Stats / Handler gRPC | ✅ 真实数据 |
| hash 重启优化 | ✅ HashedSet |
| remove 后踢连接 | ✅ v0.7.3 |
| plugin sync includeRuleTags | ✅ v0.7.3 |
| sharedLists ext: | ✅ v0.7.3 |
| nftables filter chain | ✅ v0.7.3 ip/IPv4；v0.7.4 补 ip6 镜像表 |
| upgrade.sh | ✅ v0.7.4 |
| GitHub Release / 生产联调 | ❌ 见 docs/release.md |

---

## 一、现状盘点

### 1.1 已完成（MVP 核心链路）

| 模块 | 文件 | 状态 |
|------|------|------|
| 配置加载 | `internal/config/config.go` | ✅ `.env` + 环境变量，自动生成 socket/token |
| SECRET_KEY 解析 | `internal/secret/secret.go` | ✅ base64 JSON + PEM 归一化 |
| mTLS HTTPS | `internal/httpserver/server.go` | ✅ 双向 TLS，RequireAndVerifyClientCert |
| JWT RS256 | `internal/auth/jwt.go` | ✅ Bearer 验签 + exp/nbf |
| Xray 配置注入 | `internal/xray/apiconfig.go` | ✅ API inbound / stats / policy / routing |
| 内部 mTLS 证书 | `internal/xray/certs.go` | ⚠️ 仅 CA + Server，缺 Client cert |
| rw-core 子进程 | `internal/xray/manager.go` | ✅ os/exec 替代 supervisord |
| Unix get-config | `internal/unixconfig/server.go` | ✅ token 鉴权 |
| 系统信息 | `internal/system/system.go` | ⚠️ 基础 /proc 读取，无网卡速率 |
| 单元测试 | `internal/*_test.go` | ✅ 部分覆盖，无集成测试 |

**已实现 HTTP 路由（3/28）：**

- `POST /node/xray/start`
- `GET  /node/xray/stop`
- `GET  /node/xray/healthcheck`

### 1.2 完全缺失

| 类别 | 内容 |
|------|------|
| **一键安装** | `install.sh`、systemd unit、GitHub Release CI、预编译二进制 |
| **rw-core 安装** | 官方 `install-xray.sh` 等价逻辑、geoip/geosite.dat 下载 |
| **容器化** | Dockerfile（可选，作为第二分发渠道） |
| **Stats 路由** | 10 个端点，0 实现 |
| **Handler 路由** | 8 个端点，0 实现 |
| **Plugin 路由** | 5 个端点，0 实现 |
| **Internal webhook** | `POST /internal/webhook` |
| **Xray gRPC 客户端** | 连接 `127.0.0.1:61000` 的全部 protobuf 调用 |
| **Contract 测试** | 与官方 DTO shape 对齐的 golden test |
| **zstd body parser** | 官方 `1000mb` + zstd 压缩请求体 |
| **Hash 重启优化** | `isNeedRestartCore()` / inbound hash 比对 |
| **用户状态追踪** | `extractUsersFromConfig()` / HashedSet |
| **Torrent blocker** | webhook 路由、blackhole outbound、report 缓存 |
| **nftables 插件** | 需 CAP_NET_ADMIN + libnftnl |
| **Vision IP 封禁** | router gRPC 动态规则 |
| **运维工具** | `xlogs` / `xerrors` 日志 tail 命令 |

---

## 二、与官方差距详解

### 2.1 架构对比

```
官方 (remnawave/node)                    Go lite (当前)
─────────────────────                    ────────────────
Docker 镜像                               无分发渠道
supervisord 管理 xray                     Go os/exec 管理 xray ✅
Node.js NestJS HTTPS                      Go net/http HTTPS ✅
@remnawave/xtls-sdk gRPC                  仅 TCP 端口探测 ❌
28 个 Panel API 路由                       3 个路由 ❌
install-xray.sh 内置 core                  需手动安装 rw-core ❌
nftables + torrent blocker                  未实现 ❌
```

### 2.2 Panel 接入最低要求

Panel 周期性调用以下接口，缺失会导致功能异常：

| 优先级 | 路由 | Panel 影响 |
|--------|------|-----------|
| P0 | `GET /node/stats/get-system-stats` | 节点详情页系统状态、Xray 运行信息 |
| P0 | `POST /node/stats/get-users-stats` | 用户流量统计（核心功能） |
| P1 | `POST /node/stats/get-user-online-status` | 在线状态显示 |
| P1 | `POST /node/handler/add-user` 等 | 用户热更新（不重启 core） |
| P2 | Stats 其余 7 个 | 入站/出站/组合统计 |
| P2 | Plugin sync / torrent-blocker | 插件功能 |
| P3 | nftables / vision | IP 封禁 |

**结论：当前 MVP 只能让 Panel 完成节点注册和 Xray 启动，无法显示流量/在线状态。**

### 2.3 一键安装差距

官方 Docker 镜像在构建时自动完成：

1. `npm ci && npm run build` — Node 应用编译
2. `curl install-xray.sh | sh` — 下载 rw-core + geo 文件
3. supervisord + entrypoint 环境变量注入
4. `ln -s xray rw-core`

Go 版需要等价的一键脚本（参考同仓库 `quota-dns-router-go/scripts/install-master.sh` 模式）：

```bash
curl -fsSL https://raw.githubusercontent.com/.../install-node.sh | bash
# 期望结果：
#   /usr/local/bin/remnanode-lite   ← Go 二进制
#   /usr/local/bin/rw-core          ← Xray core
#   /usr/local/share/xray/*.dat     ← geo 文件
#   /etc/remnanode/node.env         ← SECRET_KEY 等配置
#   systemd: remnawave-node.service
```

---

## 三、分阶段实施计划

### Phase 0 — 工程基础（1–2 天）

**目标：** 可重复构建、可发布、可安装的空壳。

| # | 任务 | 产出 |
|---|------|------|
| 0.1 | 添加 `internal/version/version.go` + ldflags 注入 | 构建时嵌入 git tag |
| 0.2 | GitHub Actions：`go test` + 交叉编译 linux/amd64, arm64 | `.github/workflows/release.yml` |
| 0.3 | 编写 `scripts/install-node.sh` | 下载 release 二进制 + systemd |
| 0.4 | 编写 `scripts/install-xray.sh`（复用或 fork 官方脚本） | 自动安装 rw-core + geo |
| 0.5 | 创建 `/etc/remnanode/node.env` 模板 + `EnvironmentFile` systemd unit | `remnawave-node.service` |
| 0.6 | socket 路径默认改为 `/run/remnawave-internal-*.sock`（Linux） | 生产路径对齐官方 |
| 0.7 | 添加 `scripts/uninstall.sh` + `scripts/upgrade.sh` | 生命周期管理 |

**验收标准：**
```bash
curl ... | bash -s -- --yes
systemctl status remnawave-node   # active
curl -k https://127.0.0.1:$NODE_PORT/node/xray/healthcheck  # 401 (缺 JWT，但 TLS 通)
```

---

### Phase 1 — Panel 基础兼容（3–5 天）

**目标：** Panel 能注册节点、启动 Xray、显示系统状态（流量可先为 0）。

| # | 任务 | 产出 |
|---|------|------|
| 1.1 | 补齐 **Stats stub 路由**（10 个） | `internal/stats/handler.go` |
| 1.2 | 实现 `GET /node/stats/get-system-stats` 真实响应 | 宿主 stats + xrayInfo stub |
| 1.3 | 补齐 **Handler stub 路由**（8 个） | 返回 `{ success: true }` |
| 1.4 | 补齐 **Plugin stub 路由**（5 个） | 返回 `{ accepted: false }` / `[]` |
| 1.5 | 实现 `POST /internal/webhook` no-op | unixconfig 扩展 |
| 1.6 | HTTP body limit 提升至 1000MB | `MaxBytesReader` |
| 1.7 | 生成 **Client mTLS 证书**（gRPC 前置） | `certs.go` 扩展 |
| 1.8 | Contract golden tests：对比官方 DTO JSON shape | `internal/contract/*_test.go` |
| 1.9 | 网卡速率统计（`/proc/net/dev`） | `system.go` 扩展 |

**Stats stub 响应示例：**
```json
// GET /node/stats/get-system-stats
{
  "response": {
    "xrayInfo": null,
    "plugins": { "torrentBlocker": { "reportsCount": 0 } },
    "system": { "stats": { "memoryFree": ..., "uptime": ..., "interface": {...} } }
  }
}
```

**验收标准：**
- Panel 添加节点 → 绿色在线
- Panel 下发配置 → Xray 启动成功
- 节点详情页显示 CPU/内存/运行时间
- 流量统计显示 0（stub 可接受）

---

### Phase 2 — Xray gRPC 客户端（5–8 天）

**目标：** 真实流量统计、在线状态、健康检查。

| # | 任务 | 产出 |
|---|------|------|
| 2.1 | 调研 rw-core gRPC proto（stock Xray vs Remnawave 扩展） | `docs/grpc-research.md` |
| 2.2 | 引入/生成 Go protobuf（`StatsService`、`HandlerService`、`RoutingService`） | `internal/xtls/` |
| 2.3 | mTLS gRPC client：`127.0.0.1:61000`，SNI `internal.remnawave.local` | `internal/xtls/client.go` |
| 2.4 | `getSysStats()` → healthcheck 真实判定 | 替换 TCP 端口探测 |
| 2.5 | `getAllUsersStats(reset)` → `POST /node/stats/get-users-stats` | 真实流量 |
| 2.6 | `getUserOnlineStatus()` → online status | 真实在线 |
| 2.7 | inbound/outbound/combined stats 4 个路由 | 真实数据 |
| 2.8 | `getStatsOnlineIpList()` / `getAllOnlineUsers()` | IP 列表路由 |
| 2.9 | 集成测试：mock rw-core 或 testcontainers | `internal/xtls/client_test.go` |

**技术风险：**
- Remnawave `rw-core` 可能有扩展 API，需从 `@remnawave/xtls-sdk` 源码提取 proto
- mTLS ALPN `h2` + target name override 必须完全一致

**验收标准：**
- Panel 用户列表显示真实上下行流量
- 在线状态准确
- healthcheck 的 `xrayInternalStatusCached` 反映 gRPC 可达性

---

### Phase 3 — 用户热更新（5–7 天）

**目标：** 无需重启 core 即可增删用户。

| # | 任务 | 产出 |
|---|------|------|
| 3.1 | `extractUsersFromConfig()` — 从 inbound 提取用户 UUID/hash | `internal/internal/users.go` |
| 3.2 | HashedSet 状态管理 + `isNeedRestartCore()` | 跳过不必要重启 |
| 3.3 | Handler gRPC：`addTrojanUser/addVlessUser/addShadowsocksUser/...` | 5 协议 |
| 3.4 | Handler gRPC：`removeUser/getInboundUsers/getInboundUsersCount` | 查询 + 删除 |
| 3.5 | `add-users` / `remove-users` 批量接口 | 批量 gRPC |
| 3.6 | `drop-users-connections` / `drop-ips` | 需 CAP_NET_ADMIN 或 sockdestroy 等价 |
| 3.7 | remove 后自动 drop 在线连接 | 对齐官方行为 |

**验收标准：**
- Panel 添加单个用户 → 无需 restart → 用户可连接
- Panel 删除用户 → 连接被断开
- hash 未变时 start 请求跳过 restart

---

### Phase 4 — 插件与高级功能（7–10 天，可选）

**目标：** 接近官方完整功能。

| # | 任务 | 优先级 |
|---|------|--------|
| 4.1 | Torrent blocker：webhook 路由 + blackhole outbound 注入 | P2 |
| 4.2 | Torrent report 收集与 flush | P2 |
| 4.3 | Plugin sync 框架 | P2 |
| 4.4 | nftables block/unblock/recreate（需 root + libnftnl） | P3 |
| 4.5 | Vision block-ip / unblock-ip（router gRPC） | P3 |
| 4.6 | zstd 请求体解压 | P2 |
| 4.7 | `CUSTOM_CORE_URL` 支持 | P3 |
| 4.8 | `xlogs` / `xerrors` 运维命令 | P3 |
| 4.9 | Docker 镜像（多阶段：Go binary + rw-core） | P3 |

---

## 四、推荐目录结构（目标态）

```
remnawave-node-lite-go/
├── cmd/remnanode-lite/main.go
├── internal/
│   ├── auth/           # JWT ✅
│   ├── config/         # 配置 ✅
│   ├── contract/       # DTO 定义 + golden tests  [Phase 1]
│   ├── handler/        # Handler HTTP + gRPC       [Phase 3]
│   ├── httpserver/     # mTLS 路由分发             [Phase 1 扩展]
│   ├── plugin/         # Plugin HTTP               [Phase 4]
│   ├── secret/         # SECRET_KEY ✅
│   ├── stats/          # Stats HTTP + gRPC         [Phase 2]
│   ├── system/         # 系统信息 ✅
│   ├── unixconfig/     # internal socket ✅
│   ├── xray/           # core 管理 + apiconfig ✅
│   └── xtls/           # gRPC client               [Phase 2]
├── scripts/
│   ├── install-node.sh       [Phase 0]
│   ├── install-xray.sh       [Phase 0]
│   ├── uninstall.sh          [Phase 0]
│   └── upgrade.sh            [Phase 0]
├── .github/workflows/
│   └── release.yml           [Phase 0]
└── docs/
    ├── compat-analysis.md    ✅
    ├── roadmap.md            ✅ (本文档)
    └── grpc-research.md      [Phase 2]
```

---

## 五、工作量估算

| 阶段 | 工期 | 累计完成度 | Panel 可用程度 |
|------|------|-----------|---------------|
| 当前 MVP | — | ~18% | 仅注册 + 启动 |
| Phase 0 工程基础 | 1–2 天 | ~25% | 可一键安装 |
| Phase 1 Panel 兼容 | 3–5 天 | ~45% | 节点详情正常，流量为 0 |
| Phase 2 gRPC 统计 | 5–8 天 | ~70% | 流量/在线真实 |
| Phase 3 热更新 | 5–7 天 | ~85% | 日常运维可用 |
| Phase 4 插件高级 | 7–10 天 | ~95% | 接近官方 |

**总计：约 3–5 周（单人全职）**

---

## 六、风险与注意事项

### 6.1 许可证（AGPL-3.0）

- 官方 `@remnawave/node` 为 AGPL-3.0-only
- Go 版必须**独立实现**，不可复制官方 TypeScript 源码
- 可参考公开 API contract 和 behavior，不可照搬 generateApiConfig 等函数
- 当前 Go 版已标注 AGPL-3.0-only ✅

### 6.2 Panel 版本绑定

- contract 路由/DTO 来自 `libs/contract/`，Panel 升级可能 silent break
- 建议 Phase 1 引入 contract golden tests，CI 对比官方 schema

### 6.3 rw-core vs stock Xray

- 官方使用 Remnawave 定制 core（`rw-core` → `xray` symlink）
- gRPC proto 可能有扩展（`getStatsOnlineIpList` 等）
- Phase 2 第一步必须是 proto 调研，不可假设 stock Xray API 够用

### 6.4 Linux 依赖

| 功能 | 依赖 |
|------|------|
| nftables 插件 | root, CAP_NET_ADMIN, libnftnl, libmnl |
| 在线 IP / drop connections | CAP_NET_ADMIN |
| Unix socket | Linux only（Windows 仅开发调试） |

---

## 七、Phase 0 快速启动清单

> **状态：已实现（v0.3.0）** — 待 push 到 GitHub 并打 tag 发布首个 Release。

```
✅ 1. internal/version/version.go
✅ 2. .github/workflows/release.yml（test + build linux/amd64,arm64）
✅ 3. scripts/install-node.sh
✅ 4. scripts/install-xray.sh（curl 官方 remnawave/scripts）
✅ 5. deploy/remnawave-node.service（systemd unit）
✅ 6. config: Linux 默认 socket → /run/remnawave-internal-*.sock
□ 7. 打 tag v0.3.0，发布第一个可安装版本（需 push 到 GitHub）
✅ 8. README 更新：一键安装命令 + Panel 接入步骤
```

---

## 八、与「一键安装」的直接映射

用户原始需求：**Go 一键代码安装，替代 Docker 版 remnawave/node**

| 官方 Docker 步骤 | Go 一键等价 |
|-----------------|------------|
| `docker pull remnawave/node` | `curl install-node.sh \| bash` |
| `docker run -e SECRET_KEY=...` | 写入 `/etc/remnanode/node.env` |
| entrypoint 生成 socket/token | Go 进程自动生成（已有 ✅） |
| supervisord 启动 | systemd 启动 Go 二进制 |
| install-xray.sh 内置 | install 脚本调用同等逻辑 |
| `docker logs` | `journalctl -u remnawave-node` |
| `xlogs` / `xerrors` | 脚本安装 tail 别名或子命令 |

**最小可用一键安装 = Phase 0 + Phase 1 的 Stats stub。**
**生产可用 = Phase 0 + Phase 1 + Phase 2。**
