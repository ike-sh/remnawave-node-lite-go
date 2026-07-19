# 0.1.0 本地发布清单

本项目在获得明确授权前只在本地提交和打 tag，不 push、不创建 PR。公开发布时，自有仓库的 tag workflow 同时构建 GitHub Release 资产和 GHCR 多架构镜像。

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
CANDIDATE_TAG="checkpoint-m08-code-candidate-$(git rev-parse --short=12 "$C")"
git tag -a "$CANDIDATE_TAG" -m "checkpoint M8 code candidate $C"
```

候选 tag 包含 commit 短 SHA，因此每次重新冻结都会创建唯一且不可变的本地 tag。历史 `checkpoint-m08-code-candidate` 已指向早期审计基线，不得移动或复用。此阶段不得创建 `v0.1.0`。

## 2. 执行 M8 真实验收

验收协议见 [`development/release-acceptance.md`](development/release-acceptance.md)。必须对同一个 `C` 完成：

- 官方 Node `2.8.0@596f015` 的 26 路由黑盒语义差分。
- Panel `2.8.1` 在 systemd 与 OpenRC 节点上的完整生命周期、统计、用户和插件流程。
- 并发交错 `xray start/stop` 与 `plugin sync/recreate`，确认共用外层 lifecycle gate、固定锁序和取消传播。
- Ubuntu 24.04/systemd 与 Alpine 3.22/OpenRC；两者架构并集覆盖 amd64、arm64。
- rw-core `v26.6.27`、nftables、`NETLINK_SOCK_DIAG` socket destroy、安装、重复安装、升级、坏版本回滚、reboot 和卸载隔离；两种 init 环境都验证正常停止与 leader 自然退出后的独立进程组清理。
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

只有明确授权 push 自有仓库与 release tag 后，才执行本节操作。仓库内 workflow 不依赖长期 Registry Secret：镜像发布 job 使用短期 `GITHUB_TOKEN` 和 `contents: read`、`packages: write`；独立 attestation job 额外申请 `id-token: write`、`attestations: write`。

首次公开发布前，在 GitHub 仓库/组织设置中确认：

- GitHub Actions 允许 workflow 按 YAML 申请上述权限；若组织限制第三方 Action，显式允许 workflow 中已固定 SHA 的 Docker、GitHub 和 softprops Action。
- 首次镜像 push 后，将 `ghcr.io/luxiaba/remnawave-node-lite-go` Package 设置为 Public，并确认 Package 关联到本仓库。Public Package 的生产服务器不需要登录。
- 分支保护只要求不会因路径过滤而缺失的 `test` 检查通过；`container` 是按路径运行的辅助构建，不得设为 required check。release tag 只能指向已完成 M8 验收的最终 commit。

合入 `main` 后，`container` workflow 自动发布用于服务器验收的主线镜像：

```text
ghcr.io/luxiaba/remnawave-node-lite-go:edge
ghcr.io/luxiaba/remnawave-node-lite-go:sha-<commit>
```

候选镜像同样包含 amd64/arm64、SBOM、provenance 和 attestation，但不代表正式 Release，不得使用版本 tag。服务器必须以同一个完整 commit 从 raw GitHub 下载 `compose.yaml`、`.env.example`，并把 `REMNANODE_IMAGE` 指向不可变的 `sha-<commit>`；`edge` 仅用于观察当前主线。需要重发时可在 `main` 手动运行 workflow，它还会生成 `candidate-sha-<commit>` 别名。完整无源码命令见 [Docker Compose 部署](deployment-docker.md)。正式发布后再切换到精确版本或 manifest digest。

授权后先确保最终 commit 已通过受保护流程进入 `main`，再推送不可变 tag。Release workflow 会再次验证 tag commit 可从 `origin/main` 到达，拒绝从 `dev` 或临时分支直接发布：

```bash
git fetch origin main
git merge-base --is-ancestor v0.1.0 origin/main
git push origin v0.1.0
```

`.github/workflows/release.yml` 会先重新执行完整代码门禁、Linux namespace 集成测试并创建：

- amd64/arm64 二进制归档、compact ASN 数据库。
- `compose.yaml` 与 `remnanode.env.example` 无源码部署文件。
- 覆盖上述文件的 `SHA256SUMS` 和 `docs/releases/v0.1.0.md` Release body。

只有 `release` job 成功后，`publish-container` 才会向 GHCR 推送：

```text
ghcr.io/luxiaba/remnawave-node-lite-go:0.1.0
ghcr.io/luxiaba/remnawave-node-lite-go:0.1
ghcr.io/luxiaba/remnawave-node-lite-go:latest
ghcr.io/luxiaba/remnawave-node-lite-go:sha-<commit>
```

镜像是 `linux/amd64`、`linux/arm64` manifest list，并附带 BuildKit SBOM/provenance。独立的 `attest-container` job 从 GHCR 按不可变 commit tag 解析已发布 manifest digest，再生成 GitHub build attestation；它不会重新构建或移动镜像 tag。只有稳定 SemVer Release 才更新 `latest`，预发布 tag 不会。发布完成后验证：

```bash
docker buildx imagetools inspect \
  ghcr.io/luxiaba/remnawave-node-lite-go:0.1.0

gh attestation verify \
  oci://ghcr.io/luxiaba/remnawave-node-lite-go:0.1.0 \
  --repo Luxiaba/remnawave-node-lite-go
```

已成功发布的精确版本 tag 与 `sha-*` tag 不得移动或覆盖；minor 与 `latest` 是明确的稳定版浮动别名，不用于回滚。workflow 部分失败时，先确认 GitHub Release、GHCR digest 和 attestation 哪一步已经产生；镜像发布成功而 attestation 失败时，只重试独立的 `attest-container`，不要重跑已经成功的 `publish-container`，也不要删除并重建 tag。`dev`/PR 的 `.github/workflows/container.yml` 会执行不推送的 linux/amd64 完整镜像构建，`main` 则自动发布主线候选，降低 tag 发布时才发现 Dockerfile 失效的风险。

Dockerfile frontend、Go、Debian、BuildKit、QEMU 和 SBOM scanner 均固定版本或 multi-arch manifest digest；rw-core、geo 与 `ipverse/as-ip-blocks` ASN 源码归档继续固定 commit 和 SHA-256。更新任一 digest 必须作为普通代码变更经过 `container` 与完整代码门禁，不能在 release tag 上临时覆盖 build args。

## 6. 回滚

容器部署通过 `.env` 的 `REMNANODE_IMAGE` 回滚：改回上一个已验证的精确版本或 manifest digest，执行 `docker compose pull` 和 `docker compose up -d --no-build --force-recreate`。不得通过覆盖旧 GHCR tag 实现回滚。

`upgrade.sh` 在替换前备份 binary、service、support、`node.env`、`secret.key` 和可选 rw-core 资产，并记录升级前的 active 状态；install 委托可能修复开机注册时还会捕获 enabled 状态。所有变更型 installer 入口通过固定的 `/run/lock/remnanode-installer.lock` 串行执行，嵌套的 rw-core 安装继承同一锁；事务使用 root-only 的 `/var/lib/remnanode-installer`，并在下载、解压或任一目标文件系统的磁盘预算不足时提前失败。rw-core 子安装复用外层回滚记录，不创建第二份相同资产备份。对于升级前运行中或由 install 委托要求启动的服务，目标版本二进制必须实际持有配置端口，否则恢复旧文件、开机注册与运行状态；显式升级原本 stopped 的服务保持 stopped。回滚前若服务停止命令失败，或不能确认 Node/rw-core 全部退出，将保留备份并拒绝替换运行中的文件；任何恢复步骤失败同样保留唯一备份并以非零状态结束。

共享内核 `flock` 消除并发 installer 写入，但不恢复已经被 `SIGKILL` 或掉电中断的事务。`0.1.0` 不实现持久 phase journal、开机 fence 或 OpenRC supervisor 崩溃后的自动恢复；遇到此类情况重新运行 installer、重启主机或重新创建容器即可，这些极端恢复能力不作为发布阻断。

版本回退只允许使用本项目确实发布过的旧 tag：`sudo RNL_TAG=vX.Y.Z bash upgrade.sh --yes`。不得使用参考仓库的历史 tag。
