# Remnawave Node Lite Go 改造路线

## 项目目标

本仓库以 `ike-sh/remnawave-node-lite-go@0821988` 为代码基线，独立维护自己的实现与发布历史。官方 `remnawave/node` 只作为行为和契约参考，不作为 Git 上游，也不向参考仓库提交 PR。

首个版本线从 `0.1.0` 开始，目标如下：

- 对官方 Node `2.8.0@596f015` 达到行为级兼容。
- 与 Panel `2.8.1` 完成真实集成验证。
- 修复已知生命周期、插件、防火墙、契约和安装供应链问题。
- 在 `512 MiB RAM / 1 vCPU / 2 GB disk` 的 Linux 节点稳定运行。
- 支持 Linux `amd64` 与 `arm64`。
- Debian/systemd 为主验收环境，Alpine/OpenRC 为第二验收环境。

## 设计原则

1. 官方 Contract 和可观测行为是兼容依据，官方 TypeScript 架构不是照搬对象。
2. 所有请求必须在产生副作用前完成完整校验。
3. 外部副作用必须通过可替换接口执行，并返回可传播的错误。
4. 状态只在外部操作成功后提交；失败必须允许同一请求安全重试。
5. 所有并发、队列、请求体和缓存都必须有明确上限。
6. Node 只管理自己的进程、socket 和 nftables 私有表，不接管宿主机防火墙策略。
7. `main` 始终保持测试通过；每个里程碑形成独立 checkpoint。

## 兼容边界

- `/node` 路由严格遵循官方 Node 2.8.0 的 HTTP 方法、请求、响应和错误语义。
- 自有诊断或运维能力只放在 CLI 或独立内部接口，不扩展官方 `/node` 契约。
- Node 重启后等待 Panel 重新下发配置，不从磁盘恢复可能已失效的完整代理配置。
- 请求体上限和资源保护允许形成有文档的安全偏差，但必须返回明确错误，不能静默降级。
- nftables 插件使用独立表，可与 firewalld 共存；端口开放由系统管理员负责。

## 当前进度

| 里程碑 | 状态 | 本地 checkpoint |
| --- | --- | --- |
| M0 自有项目基线 | 已完成 | `checkpoint-m00-project-baseline` |
| M1 契约证据 | 已完成 | `checkpoint-m01-contract-evidence` |
| M2 API 边界 | 已完成 | `checkpoint-m02-api-boundary` |
| M3 Xray 生命周期 | 已完成 | `checkpoint-m03-xray-lifecycle` |
| M4 插件与 nftables | 已完成 | `checkpoint-m04-plugin-nftables` |
| M5 用户、连接与统计 | 已完成 | `checkpoint-m05-users-connections-stats` |
| M6 512 MiB 资源优化 | 已完成 | `checkpoint-m06-low-memory` |
| M7 系统与供应链 | 已完成 | `checkpoint-m07-system-supply-chain` |
| M8 发布验收 | 推进中 | - |

M6/M7 的资源与发行环境数据是对应 checkpoint 的历史工程基线，不是当前 M8 候选的发布证据；冻结候选 `C` 后必须按验收协议重新执行。

当前批次先集中关闭官方 `2.8.0` 静态行为差异、明确 Go bug 和代码级 512 MiB 约束，
全部修改完成后统一回归。release evidence、tag、真实 Panel/systemd/OpenRC 和长期 soak 仍在 M8 完成。

以下事项作为已接受限制或后续增强，不阻塞 `0.1.0`：

- installer 不实现持久 phase journal；被 `SIGKILL` 或掉电中断后重新运行 installer，容器部署则重新创建容器；
- OpenRC 正常 `stop_post` 继续清理专用 cgroup；`supervise-daemon` 自身异常退出后通过重启主机或重新部署恢复；
- active config 常驻副本与运行期 `dump-config` 的内存取舍；
- P3 测试补强：`runNode` 顶层失败收敛、Unix server 活动 handler 取消，以及更完整的官方源码内容 oracle。

## 里程碑

### M0 - 自有项目基线

- 修正 module、仓库地址、版本和发布归属。
- 固定官方 Node 与 Panel 兼容版本。
- 建立路线、验收门槛和本地 checkpoint 规则。

### M1 - 契约证据

- 固化 26 条路由及其 HTTP 方法。
- 将官方 Zod 请求和响应约束转为可执行测试数据。
- 覆盖合法、缺字段、错类型、未知类型、额外 JSON 和错误响应。
- 建立官方 Node 与 Go Node 的黑盒差分测试入口。
- 契约细节与已知偏差见 [`contract-2.8.0.md`](contract-2.8.0.md)。

### M2 - API 边界

- 引入统一严格 JSON 解码、DTO 校验和错误编码。
- 将 HTTP transport 与业务服务分离。
- 保证畸形请求不会调用 Xray、nftables、`ss` 或修改内存状态。

### M3 - Xray 生命周期

- 将启动、停止、健康检查和进程退出整理为显式状态机。
- 移除 `last-start.json` 和离线旧配置恢复。
- 修复并发启动、超时、取消、子进程回收和优雅退出。
- 保证 Panel 停用和 Node 重启语义与官方一致。

### M4 - 插件与 nftables

- 将同步改为 `plan -> apply -> commit`。
- 统一 nftables 初始化、可用性、错误传播、清理和幂等重试。
- 修复 ingress unblock、退出残留、ASN 缺失和 torrent 状态偏离。
- 对 nftables 使用 Linux network namespace 集成测试。

### M5 - 用户、连接与统计

- 修复用户热更新的校验与部分失败语义。
- 让连接踢除报告真实结果并保护特殊地址。
- 用固定 worker 或批量 RPC 替代无界 goroutine 与 N+1 放大。
- 为所有 gRPC 调用增加有界超时和取消传播。

### M6 - 512 MiB 资源优化

- 将 Xray 配置收敛为单份规范 JSON，避免 map、clone、JSON 和持久化多副本。
- 限制 zstd 解码内存、报告队列、临时切片和请求峰值。
- 评估使用最小 protobuf 客户端替代完整 Xray Go 实现依赖。
- 在 cgroup 限制下记录 idle、启动、同步和大用户集峰值。
- 50k 用户真实 rw-core 峰值为 `143.9 MiB`；完整预算和复现方式见 [`resource-budget.md`](resource-budget.md)。

### M7 - 系统与供应链

- 使用专用用户、最小 capability 和 systemd sandbox。
- 对齐 Debian/systemd 与 Alpine/OpenRC 的目录权限和生命周期。
- 所有 Release、rw-core、ASN 与辅助脚本都必须固定版本并校验摘要。
- 安装、升级、失败回滚和卸载不得影响不属于本项目的进程或 nftables 表。
- Ubuntu 24.04/systemd 与 Alpine 3.22/OpenRC 已真实通过安装、重复安装、成功升级、坏 service 回滚、启停与卸载隔离；两边的非 root 服务进程均只保留 `NET_ADMIN`/`NET_BIND_SERVICE` effective 与 ambient capability。
- 固定 rw-core、ASN 与 Release 归档均在写盘前校验 SHA-256；rw-core 资产组和 Node 升级事务均通过写入后故障注入与逐文件摘要恢复测试。

### M8 - 发布验收

- 完成真实 rw-core、Panel、nftables、systemd/OpenRC 集成测试。
- 在冻结候选上复核 `xray start/stop` 与 `plugin sync/recreate` 的 shared-start/exclusive-mutation coordinator、固定锁序和取消传播。
- 在 systemd/OpenRC 中验证 rw-core 独立进程组、正常停止、超时升级和 leader 自然退出后的后代清理；不要求 Node 或 supervisor 自身被强杀后的自动恢复。
- 通过 `go test`、race、vet、静态检查、脚本检查和多架构构建。
- 在目标资源限制下完成持续运行与故障恢复测试。
- 更新兼容矩阵、风险清单、运维文档和 `0.1.0` Release 资料。
- 先冻结代码候选 commit；全部验收记录绑定该 commit，之后只允许发布文档白名单变化。
- 使用严格 JSON、文件摘要和 Git ancestry 校验 M8 证据；协议见 [`release-acceptance.md`](release-acceptance.md)。

## Checkpoint 规则

- 每个里程碑在 `codex/mXX-*` 本地分支完成。
- commit 只包含一个可说明、可验证的变化，不混入无关格式化。
- checkpoint 前必须运行与改动风险匹配的测试；失败不得合入 `main`。
- checkpoint 使用 `checkpoint-mXX-*` 注释标签，标签后不改写提交历史。
- 本项目不创建上游 PR，也不自动 push；所有提交和标签默认只保存在本地。
