# remnawave-node-lite-go

Go 实现的 Remnawave Node 轻量版：**单二进制 + 一键脚本**，适用于裸机 / VPS（systemd 或 Alpine OpenRC）。

不提供 Docker 镜像；容器部署请使用官方 [remnawave/node](https://github.com/remnawave/node)。

| 项 | 值 |
|---|---|
| 当前版本 | **v0.8.26** |
| Panel contract | `@remnawave/node` **v2.7.0**（上报 `nodeVersion=2.7.0`） |
| 变更记录 | [`docs/CHANGELOG.md`](docs/CHANGELOG.md) |

安装脚本从 `main` 拉取，默认下载 **GitHub 最新 Release** 二进制；可用 `RNL_TAG=v0.8.x` 固定版本。

## 快速开始

### Debian / Ubuntu（systemd）

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/install-node.sh | sudo bash
```

菜单：**安装 / 升级 / 卸载 / 退出**。

### Alpine（OpenRC）

```bash
apk add --no-cache curl bash   # 极简镜像需先装依赖
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/install-node-alpine.sh -o /tmp/install-alpine.sh
bash /tmp/install-alpine.sh
```

Alpine 无 `sudo` 时以 root 直接执行；systemd 发行版请用 `install-node.sh`。

### 安装后

1. 编辑 `/etc/remnanode/node.env`，设置 `NODE_PORT` 与 `SECRET_KEY`
2. 重启服务：`systemctl restart remnawave-node`（Alpine：`rc-service remnawave-node restart`）
3. 在 Panel 添加节点，端口与 `NODE_PORT` 一致（默认 `2222`）
4. 防火墙仅对 Panel IP 开放 `NODE_PORT`

非交互安装示例：

```bash
SECRET_KEY='eyJ...' NODE_PORT=2222 curl -fsSL .../install-node.sh | sudo bash -s -- --yes
```

密钥过长可使用 `SECRET_KEY_FILE=/etc/remnanode/secret.key`（与 `SECRET_KEY` 二选一）。

## 配置

生产环境配置文件：`/etc/remnanode/node.env`（模板见 [`deploy/node.env.example`](deploy/node.env.example)）。

```env
NODE_PORT=2222
SECRET_KEY="eyJ..."          # Panel 下发的 base64 JSON
XTLS_API_PORT=61000
XRAY_BIN=/usr/local/bin/rw-core
GEO_DIR=/usr/local/share/xray
LOG_DIR=/var/log/remnanode
```

`SECRET_KEY` 含 `caCertPem`、`jwtPublicKey`、`nodeCertPem`、`nodeKeyPem`，用于 mTLS 与 JWT 验签。

修改端口后重启服务即可；`NODE_PORT` 须与 Panel 中节点端口一致。

## 升级与卸载

```bash
# 升级 lite-go（保留 node.env 与 rw-core）
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/upgrade.sh | sudo bash -s -- --yes

# 同时升级 rw-core
sudo RNL_UPGRADE_XRAY=1 bash upgrade.sh --yes

# 卸载（交互式）
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/uninstall.sh | bash
```

固定版本：`RNL_TAG=v0.8.26 curl -fsSL .../upgrade.sh | sudo bash -s -- --yes`

## 运维

```bash
sudo remnanode-lite doctor          # 部署自检（含重启自动恢复检查）
systemctl status remnawave-node
journalctl -u remnawave-node -f
```

重启后 rw-core 自动恢复依赖 `/var/lib/remnanode/last-start.json`（Panel 成功启用节点后生成）。详见 [`docs/releases/v0.8.26.md`](docs/releases/v0.8.26.md)。

## 功能概览

与官方 `@remnawave/node` v2.7.0 **28 条 REST 路由**对齐，包括：

- mTLS HTTPS + JWT RS256
- Xray start / stop / healthcheck，rw-core 子进程管理
- Stats、Handler 用户热更新、Plugin sync、nftables、torrent-blocker、Vision 封禁
- Unix socket `get-config` / webhook

完整路线图：[`docs/roadmap.md`](docs/roadmap.md) · 兼容分析：[`docs/compat-analysis.md`](docs/compat-analysis.md)

**有意未实现**：Docker 镜像、`CUSTOM_CORE_URL`、geo-zapret 挂载。

## 开发与发布

```bash
go test ./...
go build -o remnanode-lite ./cmd/remnanode-lite
```

维护者发布流程：[`docs/release.md`](docs/release.md)。推送 `v*` tag 触发 CI 构建 linux/amd64、arm64 产物。

## License

AGPL-3.0-only. See `LICENSE`.
