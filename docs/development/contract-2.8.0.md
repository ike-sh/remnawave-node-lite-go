# Remnawave Node 2.8.0 行为契约基线

## 证据边界

本文件与 `internal/contract` 共同描述本项目的兼容目标。唯一官方代码基线是：

- 仓库：`https://github.com/remnawave/node.git`
- 版本：`2.8.0`
- 提交：`596f015a5c8f876dc9a9d61b6cb78d35bd8e379b`
- Panel 集成目标：`2.8.1`

路由方法取自四个官方 controller；请求和响应取自 `libs/contract/commands` 下的 Zod schema；应用错误取自 `libs/contract/constants/errors` 和 `HttpExceptionFilter`。CI 会验证官方 Git 提交、包版本以及所有引用的证据文件。

## 通用语义

- 外部 API 使用双向 TLS；官方最低版本为 TLS 1.3。
- 所有 `/node` 路由使用 RS256 Bearer JWT。官方在认证失败时销毁 socket，不返回 HTTP body。
- 官方对未知路由同样销毁 socket。
- 有请求 DTO 的接口使用 Zod 校验；对象的未知字段会被剥离，而不是拒绝。
- DTO 校验失败返回 HTTP 400：`statusCode=400`、`message="Validation failed"`、`errors=[...]`。
- 成功响应统一返回 HTTP 200，顶层为 `{ "response": ... }`。
- 已知应用错误包含 `timestamp`、`path`、`message` 和 `errorCode`；当前已定义 A001-A017 中的相关错误。
- 未映射的 Nest 异常使用通用 `statusCode`、`message`、可选 `error` 响应。
- 本项目可因资源保护设置更小的请求上限，但偏差必须明确、可观测且不得在校验前产生副作用。

## Go API 边界实现

M2 已将 20 个带请求 DTO 的路由统一接入 `internal/nodeapi`。解码器只接受一个 JSON 文档，保留 Zod object 的未知字段剥离语义，并为缺字段、错类型、联合类型 discriminant、UUID/IP、枚举、nullable/default 和 `minItems` 生成统一验证响应。6 个官方无请求 DTO 的路由继续忽略 body。

`internal/httpserver` 只负责 decode、validate、command 映射和 response envelope；stats、用户 handler 与 plugin 服务不再接收 `http.ResponseWriter`、`*http.Request` 或自行解码 JSON。Xray 配置直接解码为一份 map 后转交 manager，不经过 RawMessage 和二次反序列化。

transport 测试为 provider、连接 dropper、plugin service 和 Xray manager 注入计数 spy，验证每类非法请求的调用数为 0；合法请求则经过真实 dispatcher 后由独立官方响应 schema 再次校验。

## 路由清单

表中只列核心约束；完整类型、nullable、枚举、UUID、IP、日期和数组长度约束以 `internal/contract/official_schemas.go` 的可执行 schema 为准。

| 方法 | 路径 | 请求核心 | 响应核心 | 主要副作用或错误 |
| --- | --- | --- | --- | --- |
| POST | `/node/xray/start` | `internals.hashes`、`xrayConfig`；`forceRestart` 默认 false | `isStarted`、nullable `version/error`、节点和系统信息 | 启动或替换 rw-core，替换配置和 hash 状态；启动失败写入响应内的 RN-001 |
| GET | `/node/xray/stop` | 无 body | `isStopped` | 停止 rw-core，并清理插件状态和规则 |
| GET | `/node/xray/healthcheck` | 无 body | `isAlive`、缓存状态、nullable Xray 版本、Node 版本 | 只读缓存和进程状态 |
| POST | `/node/stats/get-user-online-status` | `username` | `isOnline` | 查询在线状态；SDK 错误降级为 false |
| POST | `/node/stats/get-users-stats` | `reset` | `users[]` 流量 | `reset=true` 清零计数；A011 |
| GET | `/node/stats/get-system-stats` | 无 body | nullable `xrayInfo`、插件和系统统计 | 查询 rw-core/宿主机；A010 |
| POST | `/node/stats/get-inbound-stats` | `tag`、`reset` | inbound 流量 | 可清零计数；A012 |
| POST | `/node/stats/get-outbound-stats` | `tag`、`reset` | outbound 流量 | 可清零计数；A013 |
| POST | `/node/stats/get-all-outbounds-stats` | `reset` | `outbounds[]` | 可清零计数；A016 |
| POST | `/node/stats/get-all-inbounds-stats` | `reset` | `inbounds[]` | 可清零计数；A015 |
| POST | `/node/stats/get-combined-stats` | `reset` | `inbounds[]`、`outbounds[]` | 可清零计数；A017 |
| POST | `/node/stats/get-user-ip-list` | `userId` | `ips[]`，含 ISO date-time | 查询并重置单用户 IP 统计 |
| GET | `/node/stats/get-users-ip-list` | 无 body | `users[].ips[]` | 查询已知用户 IP 统计 |
| POST | `/node/handler/add-user` | `data[]` 联合类型、`hashData.vlessUuid` | `success`、nullable `error` | 增加用户并更新 inbound hash |
| POST | `/node/handler/remove-user` | `username`、UUID hash | `success`、nullable `error` | 读取并踢除连接，然后删除用户和 hash |
| POST | `/node/handler/get-inbound-users-count` | `tag` | `count` | 查询 rw-core；A014 |
| POST | `/node/handler/get-inbound-users` | `tag` | `users[]` | 查询 rw-core；A014 |
| POST | `/node/handler/add-users` | `affectedInboundTags[]`、`users[]` | `success`、nullable `error` | 批量增加用户并替换受影响 hash |
| POST | `/node/handler/remove-users` | `users[]`，每项含 userId/UUID | `success`、nullable `error` | 踢除连接并批量删除用户/hash |
| POST | `/node/handler/drop-users-connections` | 非空 `userIds[]` | `success` | 查询 IP 后终止宿主机连接 |
| POST | `/node/handler/drop-ips` | 非空 `ips[]` | `success` | 终止宿主机连接；官方不要求元素是合法 IP |
| POST | `/node/plugin/sync` | nullable `plugin`；非空时含 config/UUID/name | `accepted` | 替换或清空插件状态，协调 nftables 和 rw-core |
| POST | `/node/plugin/torrent-blocker/collect` | 无 body | 完整 `reports[]` | 原子取走并清空报告队列 |
| POST | `/node/plugin/nftables/block-ips` | `ips[]`，元素为合法 IP 和数值 timeout | `accepted` | 写入定时封禁并踢连接 |
| POST | `/node/plugin/nftables/unblock-ips` | 合法 IP 数组 | `accepted` | 删除插件表内的封禁 |
| POST | `/node/plugin/nftables/recreate-tables` | 无 body | `accepted` | 重建并重新填充插件 nftables 表 |

## 请求联合类型

`handler/add-user` 的 `data[]` 只接受以下 discriminant：

- `trojan`：tag、username、password
- `vless`：tag、username、uuid、flow；flow 只能是 `xtls-rprx-vision` 或空字符串
- `shadowsocks`：tag、username、password、cipherType、ivCheck
- `shadowsocks22`：tag、username、password
- `hysteria`：tag、username、password

`handler/add-users` 的 `inboundData[]` 使用同样五种类型；VLESS 额外要求 flow。每个 `userData` 必须包含 userId、hashUuid、vlessUuid、trojanPassword 和 ssPassword。

## 当前已知偏差

本节是改造队列，不是允许永久存在的兼容声明。

| 范围 | 当前偏差 | 收敛里程碑 |
| --- | --- | --- |
| 用户操作 | 请求校验已先于副作用，但合法批量操作仍可能部分成功，失败聚合与状态提交语义尚未事务化 | M5 |
| Xray | 仍存在 `last-start.json` 离线恢复和不清晰的并发生命周期 | M3 |
| 插件 | 状态可能先于 nftables 成功提交，部分 nft 错误被吞，清理不完整 | M4 |
| 并发 | 用户 IP 查询存在 N+1 RPC 和无界 goroutine；连接踢除结果不够真实 | M5 |
| 资源 | 配置及解码过程存在多份大对象，未完成 512 MiB 峰值验收 | M6 |
| 传输 | 当前 TLS 最低版本为 1.2；认证失败和未知路由返回 HTTP，而官方销毁 socket | M7 |
| 系统 | systemd 权限过宽，安装资产和辅助数据尚未全部固定摘要 | M7 |

## 本地验证

常规可执行契约测试：

```bash
go test ./internal/contract
```

连同固定官方源码证据一起验证：

```bash
REMNANODE_OFFICIAL_SOURCE=/tmp/remnawave-node-official-2.8.0-codex \
  go test ./internal/contract
```

测试会验证：26 条 method/path 与真实 dispatcher 完全相同；所有合法请求样例；缺字段、错类型、额外字段、未知联合类型、UUID/IP/minItems；实际 Go handler 的完整成功响应 schema；官方统一错误 schema。

## 黑盒差分入口

列出路由及默认安全级别：

```bash
go run ./cmd/contract-probe -list
```

准备由同一 CA 签发的 Panel 客户端证书，并用第一个 target 作为官方基准：

```bash
export REMNANODE_CONTRACT_CA=/secure/ca.pem
export REMNANODE_CONTRACT_CERT=/secure/panel-client-cert.pem
export REMNANODE_CONTRACT_KEY=/secure/panel-client-key.pem

go run ./cmd/contract-probe \
  -token-file /secure/panel.jwt \
  -target official=https://127.0.0.1:2222 \
  -target candidate=https://127.0.0.1:3222
```

如果证书只包含 DNS 名称而 target 使用 IP，需额外传入 `-server-name <证书名称>`；探针不提供跳过证书验证的选项。

默认只执行 11 条无破坏性请求：健康检查、`reset=false` 的统计和 inbound 用户只读查询。探针比较状态、响应类别、应用错误码和 schema，不比较机器指标、流量值、响应大小、SHA-256 或耗时；报告不包含 JWT 和原始响应 body。

启动/停止、用户增删、连接踢除、IP 统计重置、报告 drain 和 nftables 操作必须同时显式指定 `-routes` 与 `-allow-mutating`，并只应在隔离验收环境执行。
