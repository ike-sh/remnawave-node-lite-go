# Remnawave Node Lite (Go)

Remnawave Panel 的轻量级 Go Node 实现：以**单一可执行文件**运行，支持 Docker Compose、systemd 与 OpenRC 部署，面向小内存 Linux 服务器。

---

## 版本信息

| 项目 | 说明 |
| --- | --- |
| 当前版本 | `0.1.0`（开发中） |
| 兼容基线 | `@remnawave/node` `2.8.0@596f015`，Panel `2.8.1` |
| 变更日志 | [CHANGELOG.md](docs/CHANGELOG.md) |
| 改造路线 | [roadmap.md](docs/development/roadmap.md) |

安装与升级脚本默认固定拉取 `v0.1.0`，不会跟随 `latest` 漂移；后续版本可通过环境变量 `RNL_TAG=vX.Y.Z` 显式指定。

---

## 系统要求

- Linux amd64/arm64（Docker Compose，或 Debian / Ubuntu 等 systemd 发行版，或 Alpine + OpenRC）
- 生产目标：整机 `512 MiB RAM / 1 vCPU / 2 GB disk`
- 所有变更型 installer 依赖 `util-linux` 提供的 `flock`；Alpine 新旧节点在安装、升级、卸载或独立更新 rw-core 前均须先执行 `apk add --no-cache util-linux`
- Alpine 生产部署要求 cgroup v2 的 memory/cpu/pids controller；OpenRC 会验证 `448MiB / 0 swap / 1 CPU / 256 PID`，缺失时拒绝启动
- systemd/OpenRC 都将 `/etc/remnanode/node.env` 作为有界数据文件读取，不会把 Secret 或未知变量导出到 Node/rw-core 环境
- Panel 下发的 `SECRET_KEY`（含 mTLS 证书与 JWT 公钥）
- [rw-core](https://github.com/XTLS/Xray-core) **≥ v26.6.27**（2.8.0 抽象套接字 API 的硬性要求；安装脚本固定安装并校验该版本）
- `CAP_NET_ADMIN` 用于 nftables 插件规则与 `NETLINK_SOCK_DIAG` socket destroy；安装器使用 iproute2 的 `ss` 复核监听端口确由目标进程持有

---

## 安装

### Docker Compose

Docker 部署沿用官方 Node 的宿主网络与 `NET_ADMIN` 模型，并额外落实 `448 MiB / 1 CPU / 0 swap / 256 PIDs` 生产限制：

```bash
cp deploy/docker.env.example .env
chmod 600 .env
# 编辑 .env，填写完整 SECRET_KEY
docker compose build --pull
docker compose up -d --no-build
docker compose ps
```

完整的首次部署、异机预构建、更新、日志与卸载说明见 [Docker Compose 部署](docs/deployment-docker.md)。host network 模式下不配置 `ports:`；防火墙需只对 Panel 地址开放 `NODE_PORT`。

### systemd（Debian / Ubuntu 等）

```bash
curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v0.1.0/scripts/install-node.sh | sudo bash
```

交互菜单：**安装 · 升级 · 卸载 · 退出**

### OpenRC（Alpine）

```bash
apk add --no-cache curl bash util-linux
curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v0.1.0/scripts/install-node-alpine.sh | bash
```

### 安装流程

1. 在 Panel 创建节点并复制 `SECRET_KEY`
2. 在本机运行安装脚本并粘贴 Secret Key
3. 看到目标 `remnanode-lite` 进程正在监听 `TCP :2222` 后，在 Panel 启用节点（若已启用，约 10s 内自动上线）
4. 防火墙仅对 Panel 地址开放 `NODE_PORT`

手动配置（非交互安装未带 `SECRET_KEY` 时）：

1. 编辑 `/etc/remnanode/node.env` 确认 `NODE_PORT`，将 Secret Key 写入 `/etc/remnanode/secret.key`（单行 base64、`root:remnanode 0640`）
2. 重启服务：`systemctl restart remnawave-node`（Alpine：`rc-service remnawave-node restart`）
3. 在 Panel 中启用节点，端口须与 `NODE_PORT` 一致（默认 `2222`）

非交互安装示例：

```bash
curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v0.1.0/scripts/install-node.sh \
  | sudo env SECRET_KEY='eyJ...' NODE_PORT=2222 bash -s -- --install --yes
```

配置模板见 [deploy/node.env.example](deploy/node.env.example)。安装器接收 `SECRET_KEY` 输入后会验证并写入受限的 `SECRET_KEY_FILE`，不会将密钥内联留在 `node.env`。

---

## 配置说明

原生部署主配置文件为 `/etc/remnanode/node.env`；Docker Compose 从仓库根目录 `.env` 读取同名运行变量，模板见 `deploy/docker.env.example`。

```env
NODE_PORT=2222
SECRET_KEY=
SECRET_KEY_FILE=/etc/remnanode/secret.key
XRAY_BIN=/usr/local/lib/remnanode/rw-core
GEO_DIR=/usr/local/share/remnanode/xray
LOG_DIR=/var/log/remnanode
```

可选能力见 `deploy/node.env.example`：`LOW_MEMORY`、`BODY_LIMIT_MB`、`NODE_BIND_ADDR`（绑定监听地址）、`CUSTOM_CORE_URL`、`GEO_ZAPRET_FILE` / `IP_ZAPRET_FILE` 等。

---

## 升级

```bash
curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v0.1.0/scripts/upgrade.sh | sudo bash -s -- --yes
```

升级会校验 Release 摘要和二进制版本，并在替换前备份 binary、service、support、`node.env` 与 `secret.key`。升级前运行中或由 install 委托要求启动的服务，只有目标二进制进程实际持有监听端口才提交事务；显式升级原本 stopped 的服务则保持 stopped。默认保留 rw-core，同步升级 rw-core：

```bash
sudo RNL_UPGRADE_XRAY=1 bash upgrade.sh --yes
```

旧版配置缺少 `LOW_MEMORY` 时升级器会按整机内存迁移；512MiB 节点可强制执行 `bash upgrade.sh --yes --low-memory`。安装、升级、rw-core 安装与卸载通过 `/run/lock/remnanode-installer.lock` 串行执行，并发入口会立即失败而不排队；安装工作区固定为 root-only 的 `/var/lib/remnanode-installer`，并在下载、解压和可用磁盘不足时提前失败。

---

## 卸载

| 模式 | 操作 | 说明 |
| --- | --- | --- |
| 保留配置 | 安装菜单 → 卸载 → 选项 1 | 移除服务与二进制，保留 `node.env` 与 rw-core |
| 完全卸载 | 安装菜单 → 卸载 → 选项 2 | 删除配置、日志、数据、rw-core 及 geo 数据 |
| 命令行 | `bash uninstall.sh --full` | 等同完全卸载 |

```bash
curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v0.1.0/scripts/uninstall.sh | sudo bash -s -- --full
```

完全卸载只清理 `/usr/local/{lib,share}/remnanode` 等项目私有路径，不会终止其它 `rw-core` 进程，也不会删除通用 `/usr/local/bin/xray` 或 `/usr/local/share/xray`。

---

## 运维

```bash
sudo remnanode-lite doctor
systemctl status remnawave-node
journalctl -u remnawave-node -f
remnanode-xlogs    # rw-core 标准输出
remnanode-xerrors  # rw-core 错误输出
```

**重启语义**：Node 不在本地持久化 Panel 下发的 Xray 配置。进程重启后先报告 Xray 离线，由 Panel 健康检查重新下发 `/node/xray/start`，与官方 Node 2.8.x 保持一致。

---

## 功能与兼容性

目标是与官方 `@remnawave/node` v2.8.0 的 **26 条 REST API** 达到行为级兼容，具体方法、schema、错误与已知偏差见[契约基线](docs/development/contract-2.8.0.md)。当前静态代码对齐已关闭已知 P1/P2，`0.1.0` 仍需按[改造路线](docs/development/roadmap.md)完成真实 Panel/Linux 与发布验收后，才能作为生产稳定版发布。

功能范围涵盖：

- 节点注册与 mTLS / JWT 认证
- Xray 生命周期（启动、停止、配置热更新）
- 流量与在线统计
- 用户热更新（VLESS / Trojan / Shadowsocks）
- 插件同步（nftables、torrent-blocker、AS/IP 共享列表等）

---

## 维护者

发布流程见 [docs/release.md](docs/release.md)。

---

## 许可证

本项目采用 [AGPL-3.0-only](LICENSE) 许可证。
