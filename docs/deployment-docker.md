# Docker Compose 部署

Docker 部署与官方 Remnawave Node 使用相同的宿主网络模型：Node 直接监听宿主机端口，并获得 nftables、连接关闭以及低端口监听所需的最小能力。Go Node 直接管理 rw-core 生命周期，因此容器只有一个主进程，不需要 s6 或第二个常驻 supervisor。

## 前置条件

- Linux amd64 或 arm64 主机
- Docker Engine 与 `docker compose` 插件
- 已在 Panel 创建节点并取得完整 `SECRET_KEY`
- Panel 配置的 Node 端口与 `NODE_PORT` 一致，默认 `2222`

生产配置限制容器使用 `448 MiB RAM / 0 swap / 1 CPU / 256 PIDs`，为整机 `512 MiB RAM / 1 vCPU / 2 GB disk` 留出宿主机余量。2 GB 是运行时目标；不要在磁盘仅剩 2 GB 的生产机上保留 Go 构建镜像和 BuildKit 缓存。

## 直接从仓库部署

```bash
git clone https://github.com/Luxiaba/remnawave-node-lite-go.git
cd remnawave-node-lite-go
cp deploy/docker.env.example .env
chmod 600 .env
```

编辑 `.env`，至少填写：

```env
NODE_PORT=2222
SECRET_KEY=粘贴_Panel_提供的完整_base64_内容
LOW_MEMORY=1
```

构建并启动：

```bash
docker compose build --pull
docker compose up -d --no-build
docker compose ps
docker compose logs --tail=100 remnanode
```

看到容器为 `healthy` 且目标进程监听 `NODE_PORT` 后，在 Panel 启用节点。宿主机防火墙只需对 Panel 地址开放 Node API 端口；Panel 下发的代理入站端口也必须按实际配置放行。

Compose 使用 `network_mode: host`，因此没有也不应添加 `ports:` 映射。`NET_ADMIN` 用于 nftables 和 socket destroy；显式的 `NET_BIND_SERVICE` 等价于官方镜像默认保留的低端口监听能力。

## 在其他机器构建

在 2 GB 磁盘的小机器上，建议先在工作站或 CI 构建目标架构镜像，再传到生产机，避免 Go 工具链和构建缓存占用生产磁盘。以 amd64 服务器为例：

```bash
docker buildx build --platform linux/amd64 \
  --tag remnanode-lite-go:0.1.0 --load .
docker save remnanode-lite-go:0.1.0 | gzip > remnanode-lite-go_0.1.0_amd64.tar.gz
scp remnanode-lite-go_0.1.0_amd64.tar.gz root@server:/tmp/
```

在服务器加载镜像并使用同一份 `compose.yaml`、`.env` 启动：

```bash
gzip -dc /tmp/remnanode-lite-go_0.1.0_amd64.tar.gz | docker load
rm -f /tmp/remnanode-lite-go_0.1.0_amd64.tar.gz
docker compose up -d --no-build
```

arm64 服务器将两处 `linux/amd64` / `_amd64` 改为 `linux/arm64` / `_arm64`。

## 运维

```bash
docker compose ps
docker compose logs -f remnanode
docker compose restart remnanode
docker compose stop remnanode
docker compose down
```

rw-core 输出保存在命名卷 `remnanode-logs`，Node 会限制并轮转日志；Docker 自身日志也限制为 `2 x 5 MiB`。普通 `docker compose down` 保留 rw-core 日志，确认不再需要数据时才执行：

```bash
docker compose down --volumes
```

更新源码后重新构建并原地替换：

```bash
docker compose build --pull
docker compose up -d --no-build --force-recreate
```

修改 `.env` 中的 Secret 或端口后也需要重新创建容器。不要提交 `.env`；Secret 会作为容器环境变量存在于本机 Docker 元数据中，应限制 Docker socket 和主机管理员权限。

## 打包内容与固定版本

多阶段镜像包含：

- `remnanode-lite` `0.1.0`，上报 Node 契约版本 `2.8.0`
- rw-core `v26.6.27`，分别固定 amd64/arm64 Release 资产与 SHA-256
- 同一 rw-core Release 的 `geoip.dat` / `geosite.dat`
- 固定 `2026-03-30` Remnawave ASN JSON 摘要生成的 compact ASN 数据库
- Debian bookworm slim、CA 证书和 nftables 运行依赖

镜像构建不会使用 `latest` 下载 rw-core 或 ASN 数据，任一外部资产摘要不匹配都会使构建失败。
