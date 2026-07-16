# 0.1.0 本地发布清单

本项目默认只在本地提交和打 tag，不 push、不创建 PR。未来若明确决定公开发布，自有仓库的 tag workflow 才负责构建 GitHub Release。

当前尚未冻结代码候选 `C`，也未生成真实验收 evidence；本清单是发布流程，不是已经完成的验收报告。

发布采用两阶段冻结：先形成代码候选 commit `C`，所有真实验收绑定 `C`；验收后只能修改发布文档和验收记录。任何 Go、脚本、workflow 或部署文件变化都会使证据失效，必须形成新候选并重跑验收。

## 1. 冻结代码候选

准备固定官方源码 checkout、完整 Go module cache，以及 `shellcheck`、`actionlint`、`govulncheck`：

```bash
export REMNANODE_OFFICIAL_SOURCE=/path/to/remnawave-node-2.8.0-596f015
export REQUIRE_GOVULNCHECK=1

bash scripts/check.sh
git status --short
```

按主题显式暂存文件，不使用 `git add -A`。暂存后重新检查 staged diff：

```bash
git add <本次候选的明确文件列表>
git diff --cached --check
git diff --cached --stat
git commit -m "chore(release): freeze 0.1.0 candidate"

C="$(git rev-parse HEAD)"
git rev-parse "${C}^{tree}"
```

此时可以记录本地辅助 tag `checkpoint-m08-code-candidate`，但不得创建 `v0.1.0`。

## 2. 执行 M8 真实验收

验收协议见 [`development/release-acceptance.md`](development/release-acceptance.md)。必须对同一个 `C` 完成：

- 官方 Node `2.8.0@596f015` 的 26 路由黑盒语义差分。
- Panel `2.8.1` 在 systemd 与 OpenRC 节点上的完整生命周期、统计、用户和插件流程。
- 并发交错 `xray start/stop` 与 `plugin sync/recreate`，确认共用外层 lifecycle gate、固定锁序和取消传播。
- Ubuntu 24.04/systemd 与 Alpine 3.22/OpenRC；两者架构并集覆盖 amd64、arm64。
- rw-core `v26.6.27`、nftables、socket kill、安装、重复安装、升级、坏版本回滚、reboot 和卸载隔离；两种 init 环境都用 wrapper + child 验证独立进程组及后代清理。
- 整机 `512 MiB / 1 CPU / 2 GiB / no swap`、50k 用户、至少 24 小时持续运行及故障恢复。

证据写入 `docs/development/acceptance/v0.1.0/`。不得记录 JWT、证书、私钥、Secret Key、IP、hostname 或原始响应 body。

## 3. 提交验收与发布资料

所有验收通过后，填写四份 evidence、`manifest.json` 和 `docs/releases/v0.1.0.md`，并更新：

- `README.md`：移除“开发中”。
- `docs/CHANGELOG.md`：将 `Unreleased` 改为实际日期。
- `docs/development/roadmap.md`：M8 标记为已完成。

从候选 `C` 到最终 HEAD 只允许以下路径变化：

```text
README.md
docs/CHANGELOG.md
docs/development/roadmap.md
docs/development/acceptance/v0.1.0/**
docs/releases/v0.1.0.md
```

```bash
git diff --name-only "${C}..HEAD"
git add README.md docs/CHANGELOG.md docs/development/roadmap.md \
  docs/development/acceptance/v0.1.0 docs/releases/v0.1.0.md
git diff --cached --check
git commit -m "docs(release): record v0.1.0 acceptance"
```

## 4. 最终门禁与本地 tag

```bash
RELEASE_TAG=v0.1.0 \
REMNANODE_OFFICIAL_SOURCE="$REMNANODE_OFFICIAL_SOURCE" \
REQUIRE_GOVULNCHECK=1 \
  bash scripts/release-check.sh

git tag -a checkpoint-m08-release-acceptance -m "checkpoint M8 release acceptance"
git tag -a v0.1.0 -m "release v0.1.0"

RELEASE_TAG=v0.1.0 \
REMNANODE_OFFICIAL_SOURCE="$REMNANODE_OFFICIAL_SOURCE" \
REQUIRE_GOVULNCHECK=1 \
REQUIRE_TAG_AT_HEAD=1 \
  bash scripts/release-check.sh
```

完成后只保留本地 commit/tag，不执行 `git push`。

## 5. 未来公开发布

只有明确授权 push 自有仓库 tag 后，`.github/workflows/release.yml` 才会重新执行完整代码门禁和 Linux namespace 集成测试，构建 amd64/arm64 归档、ASN 数据及 `SHA256SUMS`，并以 `docs/releases/v0.1.0.md` 作为 Release body。

## 6. 回滚

`upgrade.sh` 在替换前备份 binary、service、support、`node.env` 和可选 rw-core 资产。新服务未启动或未监听配置端口时会恢复旧文件与运行状态；失败日志保留事务目录供检查。

版本回退只允许使用本项目确实发布过的旧 tag：`sudo RNL_TAG=vX.Y.Z bash upgrade.sh --yes`。不得使用参考仓库的历史 tag。
