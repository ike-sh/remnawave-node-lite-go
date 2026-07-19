# M8 发布验收证据协议

本协议定义 `0.1.0` 的机器可校验验收记录。它不替代真实测试；它保证所有结果绑定同一个代码候选，并防止验收后代码漂移。当前尚未冻结候选、未生成 evidence，本文件是协议而不是验收结果。

## 候选冻结

先提交全部 Go、测试、脚本、workflow、部署和治理改动，得到 40 位 commit `C` 与 tree。所有 evidence 的 `candidateCommit` 必须是 `C`，测试开始时间不得早于该 commit 时间。

用于验收的 Node 二进制必须由 `scripts/build-release-binaries.sh` 从干净的 `C` 构建。该脚本要求本地工具链精确为 `go1.26.5`，关闭 workspace 与自动工具链漂移，清空会改变产物的 Go 构建选项，并固定 `CGO_ENABLED=0`、架构级别、`-trimpath`、release ldflags 和 `-buildvcs=false`；最终 release gate 会用同一脚本重建两种架构并比较 SHA-256。

验收后只允许修改 README、CHANGELOG、roadmap、`docs/development/acceptance/v0.1.0/` 和 `docs/releases/v0.1.0.md`。验证器要求 `C` 是最终 HEAD 的祖先，逐 commit、逐 parent 检查白名单，并拒绝发布最终化阶段的 merge；修改代码后再 revert 也不能绕过。

## 文件布局

```text
docs/development/acceptance/v0.1.0/
  manifest.json
  systemd.json
  openrc.json
  panel.json
  resource-fault.json
```

五个文件必须是 Git 跟踪、非可执行且不超过 `1 MiB` 的普通文件，工作树和 index 必须与 HEAD blob 完全一致。JSON key 大小写敏感，拒绝重复或未知字段。`manifest.json` 记录其余四个文件的 SHA-256。

## Manifest

Manifest 固定以下发布边界：

- `releaseVersion=0.1.0`、`releaseTag=v0.1.0`、`decision=pass`。
- `candidateCommit`、`candidateTree`、RFC3339 `acceptedAt`。
- 官方 Node `2.8.0@596f015a5c8f876dc9a9d61b6cb78d35bd8e379b`。
- Panel `2.8.1`。
- rw-core `v26.6.27@45cf2898ab12e97a55dd8f1f3d78d903340bdc9e`。
- 固定下载资产 SHA-256：amd64 `b3e5902d06d6282fe53cfa2fc426058b9aeaa429b2c812e20887cd47f26d08bf`，arm64 `13a251379bea366c2cf10363ad71e75734193d401f26f518bf0c25e5c8f8c931`。
- 策略：整机 512 MiB、service 448 MiB、1 CPU、2048 MiB disk、50k 用户、no swap、soak 至少 86400 秒。
- evidence 必须且只能包含 `systemd`、`openrc`、`panel`、`resource-fault` 四类。

风险项的 severity 只能是 P1/P2/P3，status 只能是 open/closed。任何 `releaseBlocking=true`，或未关闭的 P1/P2，都会拒绝发布。

## 发行环境证据

`systemd.json` 与 `openrc.json` 记录：

- `schemaVersion`、kind、candidate、pass 状态、开始/结束时间和实际命令。
- OS、版本、init、arch、kernel、内存、CPU 和磁盘。
- Node version output 与安装后二进制 SHA-256。
- rw-core version、commit、固定下载资产 SHA-256 和安装后二进制 SHA-256。
- 全新安装、重复安装、启停/重启、成功升级、失败升级回滚、reboot 后 Panel 重同步、capability 边界、卸载隔离、nft namespace 和 socket-kill namespace 检查。
- `systemChecks.rwCoreProcessGroupCleanup=true`：使用 wrapper + child 验证独立 PGID、正常停止的整组 SIGINT/SIGKILL，以及 leader 自然退出后的残余组清理；不测试 Node 或 supervisor 自身被强杀后的自动恢复。

环境固定为 Ubuntu 24.04/systemd 与 Alpine 3.22/OpenRC；两条记录的架构并集必须覆盖 amd64 和 arm64。

## Panel 证据

`panel.json` 固定 Panel `2.8.1`，targets 必须覆盖 systemd/openrc。`artifacts` 为两个 target 分别记录架构、Node binary SHA-256 和 rw-core binary SHA-256，并必须与对应发行环境证据完全一致。26 条路由必须全部通过，semantic mismatch 为 0，并覆盖节点注册、Xray 生命周期、统计、用户 mutation 与插件同步。`panelChecks.lifecyclePluginSerialization=true` 必须由 `start/stop` 与 `sync/recreate` 的并发交错场景证明：transport lifecycle gate 始终在最外层，Plugin operation gate 与 Manager ownership 不发生反向重叠，等待期间的取消可以终止请求。

## 资源与故障证据

`resource-fault.json` 使用与发行环境相同的 Node/rw-core 身份字段，并记录 50k 用户、soak 秒数、cgroup 峰值、OOM kill、项目磁盘峰值。门禁要求：

- soak 不少于 86400 秒。
- peak memory 不超过 448 MiB。
- OOM kill 为 0，项目磁盘不超过 2048 MiB，no swap。
- core kill（包含 process-group 后代清理）、Node restart、Panel disconnect、nft failure/retry、日志故障风暴和失败升级回滚全部通过。

## 数据边界

只记录白名单指标、命令和摘要。不得提交 Secret Key、JWT、CA、客户端证书、私钥、IP、hostname、Panel URL、原始请求/响应或可还原用户的数据。命令参数中的敏感值必须先脱敏。

## 验证

```bash
go run ./cmd/release-evidence-check \
  -manifest docs/development/acceptance/v0.1.0/manifest.json \
  -tag v0.1.0
```

`scripts/release-check.sh` 会调用同一验证器，并继续检查 release note、版本、完整 Go 门禁、供应链和 tag 位置。在真实证据尚未生成前，release gate 失败是预期行为。
