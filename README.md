# remnawave-node-lite-go

Go 轻量版 Remnawave Node，目标是用**单二进制 + 一键安装脚本**替代官方 [remnawave/node](https://github.com/remnawave/node) Docker 部署，面向低内存 Linux VPS（128/256MB）。

当前版本：**v0.8.3** | 基于官方 `@remnawave/node` v2.7.0 contract

## 功能状态

| 模块 | 状态 |
|------|------|
| mTLS HTTPS + JWT RS256 | ✅ |
| Xray start / stop / healthcheck | ✅ |
| rw-core 子进程管理 | ✅ |
| Unix socket get-config + webhook | ✅ |
| 一键安装 + systemd + CAP_NET_ADMIN | ✅ |
| Stats 路由（含 IP 列表） | ✅ gRPC 真实数据 |
| Handler 用户热更新 | ✅ gRPC 真实协议 + 批量 |
| hash 重启优化 | ✅ 与官方 HashedSet 兼容 |
| drop-users-connections / drop-ips | ✅ Linux `ss -K` |
| remove 后踢连接 | ✅ 查在线 IP + `ss -K` |
| plugin sync + schema 校验 | ✅ 对齐 `@remnawave/node-plugins` 0.4.4 |
| sharedLists `ext:` 解析 | ✅ connectionDrop / torrent ignoreLists |
| nftables filter chain | ✅ ip + ip6 双表 |
| torrent-blocker webhook / outbound | ✅ |
| Vision IP 封禁 | ✅ gRPC Router |
| zstd 请求体 | ✅ `Content-Encoding: zstd` 自动解压 |
| 低内存模式 | ✅ `--low-memory` / `LOW_MEMORY=1` |
| contract 路由 + DTO 测试 | ✅ 28 条官方 REST 路径 |
| 部署自检 | ✅ `remnanode-lite doctor` |

完整路线图见 [`docs/roadmap.md`](docs/roadmap.md)。

## 一键安装（Linux）

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.3/scripts/install-node.sh | sudo bash
```

**256MB VPS 推荐：**

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.3/scripts/install-node.sh \
  | sudo bash -s -- --low-memory
```

安装完成后：

1. 编辑 `/etc/remnanode/secret.key`，粘贴 Panel 下发的 Secret Key（推荐，支持超长 Key）
2. `sudo systemctl restart remnawave-node`
3. 在 Panel 添加节点，端口与 `NODE_PORT` 一致（默认 `2222`）
4. 防火墙仅对 Panel IP 开放 `NODE_PORT`

### 安装选项

```bash
# 非交互，跳过 rw-core（已安装时）
sudo RNL_SKIP_XRAY=1 bash install-node.sh --yes

# 从文件导入 Secret Key
sudo bash install-node.sh --secret-file /path/to/key

# 仅预览
bash install-node.sh --dry-run
```

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.3/scripts/upgrade.sh | sudo bash

# 同时升级 rw-core
sudo RNL_UPGRADE_XRAY=1 bash upgrade.sh --yes
```

### 卸载

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.3/scripts/uninstall.sh | sudo bash

# 同时删除配置
sudo bash uninstall.sh --purge --yes
```

## 部署自检

```bash
sudo remnanode-lite doctor
```

检查 CAP_NET_ADMIN、Secret Key、rw-core、geo 数据、nft/ss 等。

## 手动构建

要求 Go 1.23+：

```sh
go test ./...
go build -o remnanode-lite ./cmd/remnanode-lite
./remnanode-lite version
./remnanode-lite doctor
```

## 配置

生产环境推荐 `/etc/remnanode/node.env`（见 [`deploy/node.env.example`](deploy/node.env.example)）。

```env
SECRET_KEY_FILE=/etc/remnanode/secret.key
NODE_PORT=2222
XTLS_API_PORT=61000
XRAY_BIN=/usr/local/bin/rw-core
GEO_DIR=/usr/local/share/xray
LOG_DIR=/var/log/remnanode

# 低内存 VPS（可选）
LOW_MEMORY=1
BODY_LIMIT_MB=64
```

`SECRET_KEY` / `SECRET_KEY_FILE` 为 Panel 下发的 base64 JSON，含 `caCertPem`、`jwtPublicKey`、`nodeCertPem`、`nodeKeyPem`。

## 运维

```bash
systemctl status remnawave-node
journalctl -u remnawave-node -f
xlogs      # tail Xray stdout
xerrors    # tail Xray stderr
sudo remnanode-lite doctor
```

## 发布

维护者发布流程见 [`docs/release.md`](docs/release.md)。推送 `v*` tag 触发 GitHub Actions 构建 linux/amd64、arm64 二进制。

## 兼容性说明

- 基于官方 `@remnawave/node` **v2.7.0** REST contract（28 路由）
- Panel 可完成：节点注册、Xray 启动、流量/在线统计、用户热更新、插件 sync、torrent-blocker、Vision 封禁
- **可选未实现**：Docker 镜像、`CUSTOM_CORE_URL`、geo-zapret 挂载

## License

AGPL-3.0-only. See `LICENSE`.
