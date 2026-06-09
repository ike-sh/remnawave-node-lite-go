# GitHub Release 发布清单

面向维护者：将 `remnawave-node-lite-go` 从本地仓库发布到 GitHub Releases，供 `install-node.sh` / `upgrade.sh` 一键安装。

## 前置条件

- GitHub 仓库：`ike-sh/remnawave-node-lite-go`（或设置 `RNL_REPO` 覆盖）
- 本地已配置 `git remote origin` 指向该仓库
- GitHub Actions `release.yml` 已启用（push tag 自动构建）

## 1. 版本号对齐

发布前确保以下文件中的版本一致（例如 `0.8.1`）：

| 文件 | 字段 |
|------|------|
| `internal/version/version.go` | `var Version` |
| `internal/version/contract.version` | upstream contract 版本（与官方 package.json 对齐） |
| `scripts/install-node.sh` | `VERSION=` |
| `scripts/install-node-alpine.sh` | `VERSION=` |
| `scripts/upgrade.sh` | `VERSION=` |

## 1.1 已安装节点：刷新 systemd unit（CAP_NET_ADMIN）

v0.8.1 起，`deploy/remnawave-node.service` 默认包含 `AmbientCapabilities=CAP_NET_ADMIN`（对齐官方 Docker `cap_add: NET_ADMIN`）。

**已有节点**无需重装，执行：

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.1/deploy/remnawave-node.service \
  | sudo tee /etc/systemd/system/remnawave-node.service
sudo systemctl daemon-reload
sudo systemctl restart remnawave-node
```

或直接运行升级脚本（会自动刷新 unit）：

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.1/scripts/upgrade.sh | sudo bash -s -- --yes
```

验证：

```bash
sudo remnanode-lite doctor
# 或
grep AmbientCapabilities /etc/systemd/system/remnawave-node.service
journalctl -u remnawave-node -n 20 --no-pager   # 不应再出现 CAP_NET_ADMIN warning
```

## 2. 本地验证

```bash
cd remnawave-node-lite-go
go test ./...
go build -o remnanode-lite ./cmd/remnanode-lite
./sudo remnanode-lite doctor
```

## 3. 提交并打 tag

```bash
git add -A
git commit -m "release: v0.8.1"
git tag v0.8.1
git push origin main
git push origin v0.8.1
```

## 4. 等待 CI Release

1. 打开 GitHub → Actions → `release` workflow
2. 确认 tag `v0.8.1` 构建成功
3. Releases 页应出现：
   - `remnanode-lite_linux_amd64.tar.gz`
   - `remnanode-lite_linux_arm64.tar.gz`
   - `SHA256SUMS`

### 4.1 补充 Release 说明（可选）

CI 默认 Release 无正文。维护者可从仓库内说明文件同步：

```bash
gh release edit v0.8.22 --notes-file docs/releases/v0.8.22.md
```

或打开 GitHub Releases 页面手动粘贴 `docs/releases/v0.8.22.md` 内容。

## 5. 服务器验证（推荐）

### 全新安装

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.1/scripts/install-node.sh | sudo bash -s -- --yes
# 编辑 /etc/remnanode/node.env 填入 SECRET_KEY
sudo systemctl restart remnawave-node
sudo systemctl status remnawave-node
```

### 升级已有节点

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v0.8.1/scripts/upgrade.sh | sudo bash -s -- --yes
```

### 健康检查

```bash
sudo remnanode-lite doctor
journalctl -u remnawave-node -n 50 --no-pager
# 有 CAP_NET_ADMIN 时：
sudo nft list ruleset | grep -E 'remnanode|remnanode6'
```

### Panel 联调

1. Panel 添加节点（端口 = `NODE_PORT`，默认 2222）
2. 下发 Xray 配置 → 节点在线
3. 检查流量统计、用户热更新、插件 sync（如启用 torrent-blocker）

## 6. 回滚

```bash
sudo systemctl stop remnawave-node
sudo cp /usr/local/bin/remnanode-lite.bak.TIMESTAMP /usr/local/bin/remnanode-lite
sudo systemctl start remnawave-node
```

或指定旧 tag 重新 upgrade：

```bash
sudo RNL_TAG=v0.7.3 bash upgrade.sh --yes
```

## 7. 常见问题

| 问题 | 处理 |
|------|------|
| install 404 | Release 未发布或 tag 名不匹配（需 `v` 前缀） |
| Panel 连不上 | 检查 `SECRET_KEY`、防火墙、`NODE_PORT` |
| nft 无规则 | 进程需 `CAP_NET_ADMIN`；见 systemd unit |
| 升级后 Xray 未更新 | 使用 `--upgrade-xray` 或 `RNL_UPGRADE_XRAY=1` |

## 8. 首次推送 remote（若尚未配置）

```bash
git remote add origin git@github.com:ike-sh/remnawave-node-lite-go.git
git branch -M main
git push -u origin main
git push origin v0.8.1
```
