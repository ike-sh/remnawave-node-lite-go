# Docker Compose 部署

`v0.1.0` 正式发布后，Release workflow 将同一份源码构建为 `linux/amd64`、`linux/arm64` multi-arch 镜像并发布到：

```text
ghcr.io/luxiaba/remnawave-node-lite-go
```

生产服务器只需要 `compose.yaml` 和 `.env`。Compose 与官方 Remnawave Node 一样使用宿主网络；Go Node 直接管理 rw-core 生命周期，因此容器只有一个主进程，不需要 s6 或第二个常驻 supervisor。

## 前置条件

- Linux amd64 或 arm64 主机
- Docker Engine 与 `docker compose` 插件
- 已在 Panel 创建节点并取得完整 `SECRET_KEY`
- Panel 配置的 Node 端口与 `NODE_PORT` 一致，默认 `2222`

生产 Compose 限制容器使用 `448 MiB RAM / 0 swap / 1 CPU / 256 PIDs`，为整机 `512 MiB RAM / 1 vCPU / 2 GB disk` 留出宿主机余量。

## 无源码部署

正式版本发布后，从 GitHub Release 下载经过同一份 `SHA256SUMS` 覆盖的部署文件。以下以 `0.1.0` 为例：

```bash
mkdir -p /opt/remnanode && cd /opt/remnanode

base_url=https://github.com/Luxiaba/remnawave-node-lite-go/releases/download/v0.1.0
curl -fLO "$base_url/compose.yaml"
curl -fLO "$base_url/remnanode.env.example"
curl -fLO "$base_url/SHA256SUMS"

grep -E ' (compose.yaml|remnanode.env.example)$' SHA256SUMS | sha256sum -c
mv remnanode.env.example .env
chmod 600 .env
```

编辑 `.env`，至少填写完整 Secret：

```env
REMNANODE_IMAGE=ghcr.io/luxiaba/remnawave-node-lite-go:0.1.0
NODE_PORT=2222
SECRET_KEY=粘贴_Panel_提供的完整_base64_内容
LOW_MEMORY=1
```

启动并观察状态：

```bash
docker compose pull
docker compose up -d --no-build
docker compose ps
docker compose logs --tail=100 remnanode
```

看到容器为 `healthy` 且目标进程监听 `NODE_PORT` 后，在 Panel 启用节点。宿主机防火墙只需对 Panel 地址开放 Node API 端口；Panel 下发的代理入站端口也必须按实际配置放行。

Compose 使用 `network_mode: host`，因此没有也不应添加 `ports:` 映射。`NET_ADMIN` 用于 nftables 和 socket destroy；显式的 `NET_BIND_SERVICE` 等价于官方镜像默认保留的低端口监听能力。

## GHCR 可见性与登录

项目计划将 GHCR Package 设为 Public。Public Package 可以匿名拉取，不需要在服务器保存 GitHub 凭据。

首次发布后、尚未切换为 Public 时，使用只包含 `read:packages` 的最小权限 PAT 登录。不要把 PAT 写入 `.env`：

```bash
printf '%s' "$GHCR_TOKEN" | docker login ghcr.io \
  --username YOUR_GITHUB_USERNAME --password-stdin
unset GHCR_TOKEN
```

## 正式发布前的候选验收

正式 Release 前的服务器验收使用维护者从 `main` 手动发布的 `candidate-sha-<commit>` 镜像。候选镜像不承诺版本稳定性，只能用于与该 commit 绑定的验收；不得自行改写为 `0.1.0` 等正式 tag。

候选阶段没有 GitHub Release 资产。服务器按完整 commit 下载同一版本的 Compose 和环境模板，不需要 clone 仓库：

```bash
candidate_commit=替换为_40位小写_commit
printf '%s\n' "$candidate_commit" | grep -Eq '^[0-9a-f]{40}$'

mkdir -p /opt/remnanode && cd /opt/remnanode
base_url="https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/${candidate_commit}"
curl -fL "$base_url/compose.yaml" -o compose.yaml
curl -fL "$base_url/.env.example" -o remnanode.env.example
sed -i "s|^REMNANODE_IMAGE=.*|REMNANODE_IMAGE=ghcr.io/luxiaba/remnawave-node-lite-go:candidate-sha-${candidate_commit}|" remnanode.env.example
mv remnanode.env.example .env
chmod 600 .env
# 编辑 .env，填写完整 SECRET_KEY

docker compose pull
docker compose up -d --no-build
docker compose ps
```

完整 commit 同时固定部署文件与镜像 tag。候选 attestation 发布成功后，还可验证镜像确由本仓库 workflow 构建：

```bash
gh attestation verify \
  "oci://ghcr.io/luxiaba/remnawave-node-lite-go:candidate-sha-${candidate_commit}" \
  --repo Luxiaba/remnawave-node-lite-go
```

## 固定镜像摘要

精确版本 tag 不会被维护者主动覆盖。需要抵御 registry tag 被误移动时，可在首次拉取后记录 manifest digest：

```bash
docker image inspect \
  --format '{{index .RepoDigests 0}}' \
  ghcr.io/luxiaba/remnawave-node-lite-go:0.1.0
```

将输出的完整 `name@sha256:...` 写入 `.env` 的 `REMNANODE_IMAGE` 即可固定摘要。GitHub CLI 可验证发布 workflow 生成的 build attestation：

```bash
gh attestation verify \
  oci://ghcr.io/luxiaba/remnawave-node-lite-go:0.1.0 \
  --repo Luxiaba/remnawave-node-lite-go
```

## 更新与回滚

跨版本更新时，先在临时目录按“无源码部署”步骤下载并校验目标 Release 的 `compose.yaml`、环境模板和 `SHA256SUMS`；确认配置兼容后再替换当前 `compose.yaml`，并把 `.env` 中 `REMNANODE_IMAGE` 改为新的精确版本。随后执行：

```bash
docker compose pull
docker compose up -d --no-build --force-recreate
docker compose ps
```

回滚时同时恢复上一个版本配套的 `compose.yaml`，将 `REMNANODE_IMAGE` 恢复为已验证的版本 tag 或 digest，再重复相同命令。修改 Secret 或端口后也需要重新创建容器。

## 本地源码构建

源码构建仅用于开发、审计或 GHCR 暂不可用的应急场景。`compose.build.yaml` 会覆盖生产镜像名并增加本地 build 配置：

```bash
git clone https://github.com/Luxiaba/remnawave-node-lite-go.git
cd remnawave-node-lite-go
cp .env.example .env
chmod 600 .env
# 编辑 .env，填写 SECRET_KEY

docker compose -f compose.yaml -f compose.build.yaml build --pull
docker compose -f compose.yaml -f compose.build.yaml up -d --no-build
```

不要在磁盘仅剩 2 GB 的生产机上源码构建；Go 工具链、跨架构基础层和 BuildKit 缓存可能超过运行时磁盘预算。

## 运维

```bash
docker compose ps
docker compose logs -f remnanode
docker compose restart remnanode
docker compose stop remnanode
docker compose down
```

rw-core 输出保存在命名卷 `remnanode-logs`，Node 会限制并轮转日志；Docker 自身日志也限制为 `2 x 5 MiB`。普通 `docker compose down` 保留日志，确认不再需要数据时才执行：

```bash
docker compose down --volumes
```

不要提交 `.env`。Secret 会作为容器环境变量存在于本机 Docker 元数据中，应限制 Docker socket 和主机管理员权限。

## 镜像内容

`0.1.0` 镜像包含：

- `remnanode-lite` `0.1.0`，上报 Node 契约版本 `2.8.0`
- rw-core `v26.6.27`，分别固定 amd64/arm64 Release 资产与 SHA-256
- 同一 rw-core Release 的 `geoip.dat` / `geosite.dat`
- 固定 `ipverse/as-ip-blocks@56d021c` 源码归档与 SHA-256，流式生成 compact ASN 数据库
- 固定 manifest digest 的 Go、Debian 与 Dockerfile frontend 基础镜像
- Debian bookworm slim、CA 证书和 nftables 运行依赖

发布 workflow 不生成或消费 `latest`；任一固定外部资产摘要不匹配都会使构建失败。
