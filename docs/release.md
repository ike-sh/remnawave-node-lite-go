# GitHub Release 发布清单

面向维护者：将 `remnawave-node-lite-go` 发布到 GitHub Releases，供 `install-node.sh` / `upgrade.sh` 一键安装。

## 前置条件

- GitHub 仓库：`ike-sh/remnawave-node-lite-go`
- 本地已配置 `git remote origin`
- GitHub Actions `release.yml` 已启用（push tag 自动构建）

## 1. 版本号对齐

发布前确保以下文件版本一致：

| 文件 | 字段 |
|------|------|
| `internal/version/version.go` | `var Version` |
| `internal/version/contract.version` | upstream contract 版本 |
| `scripts/install-node.sh` | `VERSION=` |
| `scripts/install-node-alpine.sh` | `VERSION=` |
| `scripts/upgrade.sh` | `VERSION=` |
| `scripts/uninstall.sh` | `VERSION=` |
| `README.md` | 当前版本链接 |

## 2. 本地验证

```bash
go test ./...
go build -o remnanode-lite ./cmd/remnanode-lite
```

## 3. 提交并打 tag

```bash
git add -A
git commit -m "release: v1.0.0"
git tag v1.0.0
git push origin main
git push origin v1.0.0
```

## 4. 等待 CI Release

1. GitHub → Actions → `release` workflow
2. 确认 tag 构建成功
3. Releases 页应出现 `remnanode-lite_linux_amd64.tar.gz`、`remnanode-lite_linux_arm64.tar.gz`、`SHA256SUMS`

可选：将 `docs/releases/vX.Y.Z.md` 同步为 Release 说明。

## 5. 服务器验证

```bash
curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v1.0.0/scripts/upgrade.sh | sudo bash -s -- --yes
sudo remnanode-lite doctor
journalctl -u remnawave-node -n 50 --no-pager
```

## 6. 回滚

```bash
sudo systemctl stop remnawave-node
sudo cp /usr/local/bin/remnanode-lite.bak.TIMESTAMP /usr/local/bin/remnanode-lite
sudo systemctl start remnawave-node
```

或指定旧 tag：`sudo RNL_TAG=v0.8.30 bash upgrade.sh --yes`

## 7. 常见问题

| 问题 | 处理 |
|------|------|
| install 404 | Release 未发布或 tag 名不匹配（需 `v` 前缀） |
| Panel 连不上 | 检查 `SECRET_KEY`、防火墙、`NODE_PORT` |
| nft 无规则 | 进程需 `CAP_NET_ADMIN`；见 systemd unit |
| 升级后 Xray 未更新 | 使用 `--upgrade-xray` 或 `RNL_UPGRADE_XRAY=1` |
