# 512 MiB 资源预算与 M6-M7 基准

## 验收边界

生产目标是整机 `512 MiB RAM / 1 vCPU / 2 GB disk`。资源门禁将 Node 测试进程与真实 rw-core 放在同一个 cgroup 内，并使用以下限制：

- `448 MiB` hard memory limit，为宿主机内核与基础服务保留至少 `64 MiB`。
- `1 CPU`、`256` 个 PID、禁用 swap 与外部网络。
- 只读 rootfs，仅提供 `64 MiB` tmpfs。
- `LOW_MEMORY=1`，Go 运行时软内存上限为 `180 MiB`。
- 大配置包含 `50,000` 个 VLESS 用户。

门禁脚本为 [`scripts/test-low-memory.sh`](../../scripts/test-low-memory.sh)，Linux 集成测试为 [`internal/xray/resource_linux_integration_test.go`](../../internal/xray/resource_linux_integration_test.go)。测试同时验证最小 protobuf wire client 的系统统计、inbound 用户数、VLESS 热增删和用户 IP 统计 RPC。

## 固定测试资产

- 日期：2026-07-15
- 容器架构：Linux arm64
- Go：`go1.26.5`
- Docker Engine：`29.5.2`
- rw-core：`v26.6.27`
- 官方资产：`Xray-linux-arm64-v8a.zip`
- 资产 SHA-256：`13a251379bea366c2cf10363ad71e75734193d401f26f518bf0c25e5c8f8c931`

执行命令：

```bash
scripts/test-low-memory.sh \
  --rw-core /path/to/rw-core-v26.6.27 \
  --users 50000 \
  --memory 448
```

## 实测结果

`cgroup_current` 和 `cgroup_peak` 包含 Node 测试进程、rw-core、文件页和容器开销；`node_test_rss` 只表示 Node 测试进程 RSS。因此 `cgroup_peak` 是本门禁的判定指标。

| 阶段 | cgroup current | cgroup peak | Node test RSS |
| --- | ---: | ---: | ---: |
| 空闲，core 未启动 | 40.3 MiB | 44.3 MiB | 11.1 MiB |
| 启动 1k 用户 | 50.2 MiB | 51.1 MiB | 13.2 MiB |
| 1k 配置无变化同步 | 50.2 MiB | 51.1 MiB | 13.4 MiB |
| 强制重启为 50k 用户 | 102.2 MiB | 143.9 MiB | 22.6 MiB |
| 50k 用户热增删与统计 | 102.3 MiB | 143.9 MiB | 22.6 MiB |

50k 用户场景峰值为预算的 `32.1%`，距离 `448 MiB` 门禁还有约 `304 MiB`。无变化同步没有抬高峰值，说明 active 配置释放和 hash-only 状态生效。

## 二进制与磁盘

使用同一 Go 工具链和 `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'` 对比 fork 基线 `0821988`：

| 架构 | 基线 | M6 | 减少 |
| --- | ---: | ---: | ---: |
| linux/arm64 | 17,563,810 B | 12,320,930 B | 29.9% |
| linux/amd64 | 18,874,530 B | 13,176,994 B | 30.2% |

M7 使用最终安装布局补充了两类真实发行环境快照：

| 环境 | 运行内存 | 项目/整机磁盘 | 说明 |
| --- | ---: | ---: | --- |
| Ubuntu 24.04 arm64 / systemd | Node RSS `11.9 MiB` | 项目文件约 `74 MiB` | 全新安装，真实 rw-core/geo/ASN，core 尚未由 Panel 拉起 |
| Alpine 3.22 arm64 / OpenRC 容器 | 整容器 `44.1 MiB` | 整个 rootfs `150.2 MiB` | 容器限制 `512 MiB / 1 CPU / 256 PIDs`，真实安装依赖与服务 |

项目文件包括约 `12 MiB` Node、`34 MiB` 的 rw-core/support 和 `28 MiB` 的 geo/ASN。`xray.out.log`、`xray.err.log`、`openrc.log`、`openrc.err.log` 均在 `10 MiB` 时 copy-truncate，并各只保留一份 `.1`，最坏应用日志预算为 `80 MiB`。静态资产、事务临时文件和应用日志距离 `2 GB` 仍有充足余量；systemd journal 的长期增长与故障风暴留给 M8 持续运行门禁记录。

## 保护策略

- low-memory 默认请求体上限为 `16 MiB`，显式 `BODY_LIMIT_MB` 只能是 `1..1024`，`0/空` 表示自动默认。
- zstd 压缩体最多 `64 MiB`，低内存窗口最多 `16 MiB`，最多两个单线程 decoder 并发。
- 单次 gRPC 响应最多 `16 MiB`，内部 RPC 具有 deadline。
- torrent report 环形队列最多保留最新 `1024` 条。
- Xray ready 后释放解码配置树和规范 JSON，仅保留 hash 与运行状态。
- Debian 与 Alpine 安装器在 `MemTotal <= 512 MiB` 时自动写入 `LOW_MEMORY=1`。

任何修改请求解码、Xray 配置生命周期、RPC 消息、报告队列或依赖图的提交，都应重新执行此门禁并比较阶段峰值。
