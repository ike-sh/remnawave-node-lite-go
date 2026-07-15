# GitHub Release 发布清单

面向维护者：将 `remnawave-node-lite-go` 发布到 GitHub Releases，供 `install-node.sh` / `upgrade.sh` 一键安装。

## 前置条件

- 发布归属：`Luxiaba/remnawave-node-lite-go`
- 本地交付不依赖 `origin`，默认不 push、不创建 PR
- 只有未来明确决定公开发布并向自有远端 push tag 时，GitHub Actions `release.yml` 才会创建 Release

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
go test -race ./...
go vet ./...
shellcheck -x scripts/*.sh deploy/remnawave-node.openrc
actionlint
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/remnanode-lite-amd64 ./cmd/remnanode-lite
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/remnanode-lite-arm64 ./cmd/remnanode-lite
```

## 3. 本地提交并打 tag

```bash
git add -A
git commit -m "release: v0.1.0"
git tag -a v0.1.0 -m "release v0.1.0"
```

到此即完成本项目当前约定的本地交付；不要自动执行 `git push`。未来若要公开发布，应先显式确认远端属于本项目，再单独推送该 tag。

## 4. GitHub Release 资产（未来公开发布时）

1. GitHub → Actions → `release` workflow
2. 确认 tag 构建成功
3. Releases 页应出现 `remnanode-lite_linux_amd64.tar.gz`、`remnanode-lite_linux_arm64.tar.gz`、`asn-prefixes.bin`、`SHA256SUMS`
4. 两个架构归档内均应包含 binary、systemd/OpenRC service 和已校验的 upgrade/uninstall/install-xray support 脚本

可选：将 `docs/releases/vX.Y.Z.md` 同步为 Release 说明。

## 5. 服务器验证

```bash
curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v0.1.0/scripts/upgrade.sh | sudo bash -s -- --yes
sudo remnanode-lite doctor
journalctl -u remnawave-node -n 50 --no-pager
```

## 6. 回滚

`upgrade.sh` 在替换前备份 binary、service、support、`node.env` 和可选 rw-core 资产。新服务未启动或未在配置端口监听时会自动恢复旧文件和旧服务；失败日志会保留 `/tmp/remnanode-upgrade.*` 备份目录供人工检查。正常成功后事务备份会删除。

版本回退使用本项目确实发布过的旧 tag：`sudo RNL_TAG=vX.Y.Z bash upgrade.sh --yes`，同样经过摘要校验和事务门禁；不要使用参考仓库的 `v0.8.x/v1.x` tag。

## 7. 常见问题

| 问题 | 处理 |
|------|------|
| install 404 | Release 未发布或 tag 名不匹配（需 `v` 前缀） |
| Panel 连不上 | 检查 `SECRET_KEY`、防火墙、`NODE_PORT` |
| nft 无规则 | 进程需 `CAP_NET_ADMIN`；见 systemd unit |
| 升级后 Xray 未更新 | 使用 `--upgrade-xray` 或 `RNL_UPGRADE_XRAY=1` |
