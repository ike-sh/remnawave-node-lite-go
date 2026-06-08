# remnawave-node-lite-go

Go 轻量�?Remnawave Node，目标是�?*单二进制 + 一键安装脚�?*替代官方 [remnawave/node](https://github.com/remnawave/node) Docker 部署�?
当前版本�?*0.8.0** | 完成度约 **~92%**

## 功能状�?
| 模块 | 状�?|
|------|------|
| mTLS HTTPS + JWT RS256 | �?|
| Xray start / stop / healthcheck | �?|
| rw-core 子进程管�?| �?|
| Unix socket get-config + webhook | �?|
| 一键安�?+ systemd | �?|
| Stats 路由（含 IP 列表�?| �?gRPC 真实数据 |
| Handler 用户热更�?| �?gRPC 真实�? 协议 + 批量�?|
| hash 重启优化 | �?与官�?HashedSet 兼容 |
| drop-users-connections / drop-ips | �?Linux `ss -K`（需 CAP_NET_ADMIN�?|
| remove 后踢连接 | �?查在�?IP �?`ss -K` |
| plugin sync 重启逻辑 | �?includeRuleTags hash + removeOutbound |
| sharedLists ext: 解析 | �?connectionDrop / torrent ignoreLists |
| nftables filter chain | �?ip + ip6 双表（对齐官�?remnanode/remnanode6�?|
| torrent-blocker webhook | �?`/internal/webhook` �?�?IP + 上报 |
| torrent-blocker outbound | �?start 时注�?blackhole + bittorrent 规则 |
| Vision IP 封禁 | �?`/vision/block-ip` + RoutingService gRPC |
| zstd 请求�?| �?`Content-Encoding: zstd` 自动解压 |
| contract 路由覆盖 | �?28 条官�?REST 路径全覆盖测�?|

完整路线图见 [`docs/roadmap.md`](docs/roadmap.md)�?
## 一键安装（Linux�?
```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.0/scripts/install-node.sh | sudo bash
```

安装完成后：

1. 编辑 `/etc/remnanode/node.env`，填�?Panel 下发�?`SECRET_KEY`
2. `sudo systemctl restart remnawave-node`
3. �?Panel 添加节点，端口与 `NODE_PORT` 一致（默认 `2222`�?
### 安装选项

```bash
# 非交互，跳过 rw-core 安装（已有时�?sudo RNL_SKIP_XRAY=1 SECRET_KEY='...' bash install-node.sh --yes

# 仅预�?bash install-node.sh --dry-run
```

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.0/scripts/upgrade.sh | sudo bash
# 同时升级 rw-core�?sudo RNL_UPGRADE_XRAY=1 bash upgrade.sh --yes
```

### 卸载

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.0/scripts/uninstall.sh | sudo bash
# 同时删除配置：加 --purge
```

## 手动构建

要求 Go 1.23+�?
```sh
go test ./...
go build -o remnanode-lite ./cmd/remnanode-lite
./remnanode-lite version
```

## 配置

生产环境推荐 `/etc/remnanode/node.env`（见 [`deploy/node.env.example`](deploy/node.env.example)）�?
开发环境可在仓库根目录创建 `.env`�?
```env
NODE_PORT=2222
SECRET_KEY=base64-json-payload-from-panel
XTLS_API_PORT=61000
XRAY_BIN=/usr/local/bin/rw-core
GEO_DIR=/usr/local/share/xray
LOG_DIR=./logs
```

`SECRET_KEY` �?base64 编码 JSON，含 `caCertPem`、`jwtPublicKey`、`nodeCertPem`、`nodeKeyPem`�?
## 运维

```bash
systemctl status remnawave-node
journalctl -u remnawave-node -f
xlogs      # tail Xray stdout
xerrors    # tail Xray stderr
```

## 发布

维护者发布流程见 [`docs/release.md`](docs/release.md)�?
�?tag 触发 GitHub Actions 构建 linux/amd64、arm64 二进制：

```bash
git tag v0.8.0
git push origin v0.8.0
```

## 兼容性说�?
基于官方 `@remnawave/node` v2.7.0 contract。Panel 可完成节点注册、Xray 启动、流�?在线统计、用户热更新、插�?sync、torrent-blocker、Vision 封禁�?
**尚未完成�?* GitHub Release 首次推送与生产联调、Docker 镜像、`CUSTOM_CORE_URL`。发布步骤见 [`docs/release.md`](docs/release.md)�?
## License

AGPL-3.0-only. See `LICENSE`.
