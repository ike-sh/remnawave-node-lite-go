# Remnawave Node Lite (Go)

Remnawave Panel 的轻量级 Node 实现：以**单一可执行文件**配合安装脚本，在 Linux 服务器（systemd / OpenRC）上完成部署与运维，无需 Docker。

若需容器化部署，请使用官方项目 [remnawave/node](https://github.com/remnawave/node)。

---

## 版本信息

| 项目 | 说明 |
| --- | --- |
| 当前版本 | [v1.0.0](https://github.com/ike-sh/remnawave-node-lite-go/releases/tag/v1.0.0) |
| Panel 契约 | `@remnawave/node` v2.7.0（上报 `nodeVersion=2.7.0`） |
| 变更日志 | [CHANGELOG.md](docs/CHANGELOG.md) |

安装脚本默认拉取 GitHub 最新 Release；可通过环境变量 `RNL_TAG=v1.0.0` 指定版本。

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

### 安装流程

1. 在 Panel 创建节点并复制 `SECRET_KEY`
2. 在本机运行安装脚本并粘贴 Secret Key
3. 看到 `OK: TCP :2222 已监听` 后，在 Panel 启用节点（若已启用，约 10s 内自动上线）
4. 防火墙仅对 Panel 地址开放 `NODE_PORT`

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

可选能力见 `deploy/node.env.example`：`LOW_MEMORY`、`CUSTOM_CORE_URL`、`GEO_ZAPRET_FILE` / `IP_ZAPRET_FILE` 等。

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

| 模式 | 操作 | 说明 |
| --- | --- | --- |
| 保留配置 | 安装菜单 → 卸载 → 选项 1 | 移除服务与二进制，保留 `node.env` 与 rw-core |
| 完全卸载 | 安装菜单 → 卸载 → 选项 2 | 删除配置、日志、数据、rw-core 及 geo 数据 |
| 命令行 | `bash uninstall.sh --full` | 等同完全卸载 |

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/main/scripts/uninstall.sh | bash -s -- --full
```

---

## 运维

```bash
sudo remnanode-lite doctor
systemctl status remnawave-node
journalctl -u remnawave-node -f
xlogs    # rw-core 标准输出
xerrors  # rw-core 错误输出
```

**重启后自动恢复**：Panel 成功启用节点后会在 `/var/lib/remnanode/last-start.json` 写入配置；服务器重启后 Node 将自动拉起 rw-core。

---

## 功能与兼容性

实现与官方 `@remnawave/node` v2.7.0 对齐的 **28 条 REST API**，涵盖：

- 节点注册与 mTLS / JWT 认证
- Xray 生命周期（启动、停止、配置热更新）
- 流量与在线统计
- 用户热更新（VLESS / Trojan / Shadowsocks）
- 插件同步（nftables、torrent-blocker 等）
- Vision IP 封禁

未实现：Docker 镜像（项目定位为裸机轻量部署）。

---

## 维护者

发布流程见 [docs/release.md](docs/release.md)。

---

## 许可证

本项目采用 [AGPL-3.0-only](LICENSE) 许可证。
