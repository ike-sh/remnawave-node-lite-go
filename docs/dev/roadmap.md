# remnawave-node-lite-go 分阶段实施计划

> 目标：将 [remnawave/node](https://github.com/remnawave/node)（Node.js + supervisord + Docker）重写为 **Go 单二进制 + 一键安装脚本**，在 Linux 服务器上无需 Docker 即可接入 Remnawave Panel。
>
> 参考分析：[`docs/compat-analysis.md`](compat-analysis.md)（基于官方 `@remnawave/node` v2.7.0）
>
> **当前版本：v0.8.22** | 估算整体完成度：**~98%**（Panel 主流程可用）

变更记录：[`CHANGELOG.md`](CHANGELOG.md)

---

## 零、v0.8.22 状态快照（2026-06-09）

| 模块 | 状态 |
|------|------|
| xray/start gRPC Ping 启动死锁 | ✅ v0.8.22 修复 |
| 低内存 gRPC 等待 90s + `LOW_MEMORY=1` 自动检测 | ✅ v0.8.21 |
| Alpine 安装/升级/卸载菜单与 Debian 对齐 | ✅ v0.8.19+ |
| **256MB VPS 生产验证（KDDI Alpine）** | ✅ v0.8.22 联调通过 |
| Docker 镜像 | ❌ **不实现** |
| `CUSTOM_CORE_URL` | ❌ 可选，未做 |

---

## 零（归档）、v0.8.1 状态快照（2026-06）

| 模块 | 状态 |
|------|------|
| 一键安装 + systemd + CAP_NET_ADMIN | ✅ |
| GitHub Release CI + v0.8.1 已发布 | ✅ |
| 28 条 REST 路由 + DTO golden tests | ✅ |
| Stats / Handler gRPC 真实数据 | ✅ |
| hash 重启优化（HashedSet） | ✅ |
| plugin sync + schema 校验（node-plugins 0.4.4） | ✅ |
| nftables ip + ip6 双表 | ✅ |
| torrent-blocker webhook + outbound | ✅ |
| Vision IP 封禁 | ✅ |
| zstd body + 低内存模式 | ✅ |
| contract-sync CI（跟踪 upstream） | ✅ |
| `remnanode-lite doctor` 部署自检 | ✅ |
| 256MB VPS 生产压测报告 | ✅ KDDI Alpine（v0.8.22） |
| Docker 镜像 | ❌ **不实现**（用 Docker 请用官方 node） |
| `CUSTOM_CORE_URL` | ❌ 可选，未做 |

---

## 一、现状盘点

### 1.1 已完成

| 模块 | 文件 | 状态 |
|------|------|------|
| 配置加载 | `internal/config/config.go` | ✅ `.env` + `SECRET_KEY_FILE` + 低内存 |
| SECRET_KEY 解析 | `internal/secret/secret.go` | ✅ |
| mTLS HTTPS + JWT | `internal/httpserver/`、`internal/auth/` | ✅ |
| Xray 生命周期 | `internal/xray/manager.go` | ✅ os/exec 替代 supervisord |
| gRPC 客户端 | `internal/xtls/` | ✅ Stats / Handler / Router |
| Stats / Handler / Plugin / Vision 路由 | 各 handler | ✅ 28/28 |
| Internal webhook | `internal/unixconfig/server.go` | ✅ |
| 网卡速率 | `internal/system/network.go` | ✅ |
| 部署自检 | `internal/doctor/` | ✅ |
| 单元 + contract 测试 | `internal/*_test.go` | ✅ |

### 1.2 可选 / 未做

| 类别 | 说明 |
|------|------|
| Docker 镜像 | **不计划**（定位裸机替代，非容器） |
| `CUSTOM_CORE_URL` | 自定义 core 下载 |
| geo-zapret.dat 挂载 | Panel 文档中的可选 volume |
| Panel 256MB 压测报告 | 需真实 VPS 验证 |

---

## 二、与官方差距（v0.8.22）

### 2.1 架构对比

```
官方 (remnawave/node)          Go lite (v0.8.22)
─────────────────────          ─────────────────
Docker 镜像                     单二进制 + install.sh ✅
supervisord                     Go os/exec ✅
Node.js NestJS                  Go net/http ✅
28 REST 路由                    28/28 ✅
cap_add: NET_ADMIN              systemd AmbientCapabilities ✅
install-xray 内置               install-xray.sh ✅
```

### 2.2 仍存在的细微差异

| 项 | 说明 | 影响 |
|----|------|------|
| 错误码格式 | 官方统一 `errorCode` | 低，Panel 很少依赖 |
| compression / helmet | 官方 HTTP 中间件 | 无 |
| `@remnawave/node` 2.8+ | upstream 仍为 2.7.0 | contract-sync CI 跟踪 |

**结论：Panel v2.7.x 主流程（注册、启动、流量、热更新、插件、Vision）已可替代官方 node。**

---

## 三、历史分阶段计划（归档）

以下 Phase 0–4 为早期规划，**均已实现或标记为可选**。保留供参考。

<details>
<summary>展开 Phase 0–4 原始计划</summary>

### Phase 0 — 工程基础 ✅

install-node.sh、install-xray.sh、upgrade/uninstall、systemd、GitHub Release CI、version ldflags。

### Phase 1 — Panel 基础兼容 ✅

28 路由、Stats/Handler/Plugin stub → 真实实现、contract tests、1000MB body limit。

### Phase 2 — Xray gRPC ✅

`internal/xtls/`：SysStats、UsersStats、online、inbound/outbound、IP 列表。

### Phase 3 — 用户热更新 ✅

HashedSet、5 协议 add、remove、batch、drop connections、remove 后踢连接。

### Phase 4 — 插件与高级 ✅（除可选项）

torrent-blocker、nftables、Vision、zstd、plugin schema 校验、低内存模式。

</details>

---

## 四、推荐目录结构（当前）

```
remnawave-node-lite-go/
├── cmd/remnanode-lite/main.go
├── internal/
│   ├── auth/ config/ secret/ system/
│   ├── httpserver/ stats/ nodehandler/ plugin/ vision/
│   ├── xray/ xtls/ unixconfig/ connections/ netadmin/
│   ├── bodylimit/ contract/ doctor/ version/
├── scripts/          install | upgrade | uninstall | install-xray
├── deploy/           systemd unit + node.env.example
├── .github/workflows/ test | release | contract-sync
└── docs/             compat-analysis | release | roadmap | grpc-research
```

---

## 五、风险与注意事项

### 5.1 许可证（AGPL-3.0）

Go 版独立实现，参考公开 contract，不复制官方 TypeScript 源码。

### 5.2 Panel / node contract 升级

- 官方 node 当前 **v2.7.0**，28 路由无变化
- `contract-sync.yml` 每周对比 upstream，缺失路由 CI 失败

### 5.3 Linux 依赖

| 功能 | 依赖 |
|------|------|
| nftables / drop connections | CAP_NET_ADMIN（systemd 已配置） |
| nft 命令 | `apt install nftables` |
| ss -K | `iproute2` |

---

## 六、下一步（可选）

1. ~~**256MB VPS 实测**~~ — ✅ KDDI Alpine 已通过（v0.8.22）
2. **Panel 生产联调** — 全插件场景回归
3. **Docker 镜像** — 如需与官方相同的分发方式
4. **upstream 2.8** — contract-sync 告警后跟进
