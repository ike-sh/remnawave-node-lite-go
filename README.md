# Remnawave Node Lite (Go)

Remnawave Panel 的轻量级 Node 实现：以**单一可执行文件**配合安装脚本，在 Linux 服务器（systemd / OpenRC）上完成部署与运维，无需 Docker。

若需容器化部署，请使用官方项目 [remnawave/node](https://github.com/remnawave/node)。

---

## 版本信息

| 项目 | 说明 |
| --- | --- |
| 当前版本 | [v0.8.28](https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v0.8.28) |
| Panel 契约 | `@remnawave/node` v2.7.0（上报 `nodeVersion=2.7.0`） |
| 变更日志 | [CHANGELOG.md](docs/CHANGELOG.md) |

安装脚本默认拉取 GitHub 最新 Release；可通过环境变量 `RNL_TAG=v0.8.28` 指定版本。

---

## 系统要求

- Linux（Debian / Ubuntu 等 systemd 发行版，或 Alpine + OpenRC）
- Panel 下发的 `SECRET_KEY`（含 mTLS 证书与 JWT 公钥）
- [rw-core](https://github.com/XTLS/Xray-core)（安装脚本可自动安装）
- 可选：`nft`、`ss`（插件 IP 封禁与连接踢除，需 `CAP_NET_ADMIN`）

---

## 安装

### systemd（Debian / Ubuntu 等）

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/install-node.sh | sudo bash
```

交互菜单：**安装 · 升级 · 卸载 · 退出**

### OpenRC（Alpine）

```bash
apk add --no-cache curl bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/install-node-alpine.sh -o /tmp/install-alpine.sh
bash /tmp/install-alpine.sh
```

### 安装后配置

**推荐顺序（避免装完后 Panel 显示离线）：**

1. 在 Panel 创建节点并复制 `SECRET_KEY`，**保持节点禁用**（或先不填真实 IP）
2. 在本机运行安装脚本并粘贴 Secret Key
3. 看到 `OK: TCP :2222 已监听` 后，在 Panel **启用**节点
4. 防火墙仅对 Panel 地址开放 `NODE_PORT`

若安装前已在 Panel 保存并启用节点，装完后需 **禁用 → 启用** 一次（Panel 仅在状态变更时重连；安装期间节点尚未监听）。

手动配置（非交互安装未带 `SECRET_KEY` 时）：

1. 编辑 `/etc/remnanode/node.env`，填写 `NODE_PORT` 与 `SECRET_KEY`
2. 重启服务：`systemctl restart remnawave-node`（Alpine：`rc-service remnawave-node restart`）
3. 在 Panel 中启用节点，端口须与 `NODE_PORT` 一致（默认 `2222`）

非交互安装示例：

```bash
SECRET_KEY='eyJ...' NODE_PORT=2222 \
  curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/install-node.sh \
  | sudo bash -s -- --yes
```

配置模板见 [deploy/node.env.example](deploy/node.env.example)。密钥过长时可改用 `SECRET_KEY_FILE`。

---

## 配置说明

主配置文件：`/etc/remnanode/node.env`

```env
NODE_PORT=2222
SECRET_KEY="eyJ..."
XTLS_API_PORT=61000
XRAY_BIN=/usr/local/bin/rw-core
GEO_DIR=/usr/local/share/xray
LOG_DIR=/var/log/remnanode
```

---

## 升级

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/upgrade.sh | sudo bash -s -- --yes
```

升级保留现有 `node.env`、数据目录及 rw-core。同步升级 rw-core：

```bash
sudo RNL_UPGRADE_XRAY=1 bash upgrade.sh --yes
```

---

## 卸载

卸载行为取决于所选模式，**默认不会删除全部文件**。

| 模式 | 操作 | 说明 |
| --- | --- | --- |
| 保留配置 | 安装菜单 → 卸载 → 选项 1 | 移除服务与二进制，保留 `node.env` 与 rw-core |
| 完全卸载 | 安装菜单 → 卸载 → 选项 2 | 删除配置、日志、数据、rw-core 及 geo 数据 |
| 命令行 | `bash uninstall.sh --full` | 等同完全卸载 |
| 部分清理 | `bash uninstall.sh --purge --yes` | 删除配置/日志/数据，保留 rw-core |

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/uninstall.sh | bash -s -- --full
```

---

## 运维

```bash
sudo remnanode-lite doctor
systemctl status remnawave-node
journalctl -u remnawave-node -f
```

**重启后自动恢复**：Panel 成功启用节点后会在 `/var/lib/remnanode/last-start.json` 写入配置；服务器重启后 Node 将自动拉起 rw-core（v0.8.26 起）。说明见 [docs/releases/v0.8.26.md](docs/releases/v0.8.26.md)。

---

## 功能与兼容性

实现与官方 `@remnawave/node` v2.7.0 对齐的 **28 条 REST API**，涵盖节点注册、Xray 生命周期、流量与在线统计、用户热更新、插件同步、nftables / torrent-blocker 及 Vision IP 封禁等能力。

未实现项（不影响 Panel 常规接入）：Docker 镜像、`CUSTOM_CORE_URL`、geo-zapret 数据卷。

---

## 开发者

- 构建与测试：`go test ./...` · `go build -o remnanode-lite ./cmd/remnanode-lite`
- 发布流程：[docs/release.md](docs/release.md)
- 内部分析文档：[docs/dev/](docs/dev/)

---

## 许可证

本项目采用 [AGPL-3.0-only](LICENSE) 许可证。
