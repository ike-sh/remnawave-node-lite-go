# Remnawave Node Go 轻量兼容分析

> **文档性质**：官方 node 行为参考 + 早期兼容规划（撰写于 MVP 阶段）。  
> **lite-go 当前版本**：v0.8.2 | **官方参考版本**：`@remnawave/node` 2.7.0  
> **实现状态摘要**：见文末 [附录 A：lite-go v0.8.2 实现对照](#附录-a-lite-go-v082-实现对照) 与 [附录 B：v0.8.2 审计与修复记录](#附录-b-v082-审计与修复记录)。

参考仓库：[remnawave/node](https://github.com/remnawave/node)（本地快照 `remnawave-node-reference`）。本文只做兼容分析，不复制官方实现。

## 1. 官方运行模型

### mTLS HTTPS Server

入口在 `src/main.ts` 的 `bootstrap()`：

- 启动前调用 `initializeMTLSCerts()`，生成一套仅供 Node 与 Xray 内部 gRPC 使用的临时 CA/server/client 证书。
- 调用 `parseNodePayload()` 解析环境变量 `SECRET_KEY`，取得外部 Panel -> Node HTTPS mTLS 使用的 `nodeKeyPem`、`nodeCertPem`、`caCertPem` 和 JWT 验签用 `jwtPublicKey`。
- `NestFactory.create(AppModule, { httpsOptions: ... })` 以 HTTPS 启动：
  - `key`: `nodePayload.nodeKeyPem`
  - `cert`: `nodePayload.nodeCertPem`
  - `ca`: `[nodePayload.caCertPem]`
  - `requestCert: true`
  - `rejectUnauthorized: true`
- HTTP body 使用 `@kastov/body-parser-with-zstd`，JSON limit 为 `1000mb`；Go 实现如果要最大兼容，应考虑 `Content-Encoding: zstd`。
- `app.setGlobalPrefix(ROOT)` 中 `ROOT` 来自 `libs/contract/api/routes.ts`，值为 `/node`。
- 全局前缀排除了 `/internal/get-config`、`/internal/webhook`，以及 contract 中的 `/vision/block-ip`、`/vision/unblock-ip`。

结论：外部 Panel 调 Node 时需要同时满足 TLS 客户端证书校验和 Bearer JWT 校验；这是两个独立门槛。

### JWT 验证

相关文件：

- `src/common/config/app-config/config.schema.ts`
- `src/common/config/jwt/jwt.config.ts`
- `src/common/guards/jwt-guards/strategies/validate-token.ts`
- `src/common/guards/jwt-guards/def-jwt-guard.ts`

`configSchema.superRefine()` 会用 `parseNodePayloadFromConfigService()` 从 `SECRET_KEY` 中提取 `jwtPublicKey`，写入 `JWT_PUBLIC_KEY`。`JwtStrategy` 使用 Passport JWT：

- Token 来源：`Authorization: Bearer <jwt>`
- 算法：`RS256`
- 验签 key：`JWT_PUBLIC_KEY`
- `ignoreExpiration: false`

`JwtDefaultGuard.handleRequest()` 对失败请求会记录日志、销毁 socket，然后抛 `UnauthorizedException`。被该 guard 保护的外部模块包括 `xray-core`、`stats`、`handler`、`_plugin`。

### internal Unix Socket

相关文件：

- `docker-entrypoint.sh`
- `src/main.ts`
- `src/modules/internal/internal.controller.ts`
- `src/modules/internal/internal.module.ts`
- `src/modules/internal/internal.service.ts`
- `src/common/middlewares/token-auth.middleware.ts`
- `libs/contract/constants/internal/internal.constants.ts`

`docker-entrypoint.sh` 每次容器启动都会生成随机值：

- `INTERNAL_REST_TOKEN`
- `INTERNAL_SOCKET_PATH=/run/remnawave-internal-${RNDSTR}.sock`
- `SUPERVISORD_SOCKET_PATH=/run/supervisord-${RNDSTR}.sock`
- `SUPERVISORD_PID_PATH=/run/supervisord-${RNDSTR}.pid`
- `SUPERVISORD_USER`
- `SUPERVISORD_PASSWORD`

`src/main.ts` 创建第二个 Express app，监听 `INTERNAL_SOCKET_PATH` 这个 Unix socket，只转发：

- `GET /internal/get-config`
- `POST /internal/webhook`

`InternalModule.configure()` 对 `InternalController` 应用 `TokenAuthMiddleware`。该 middleware 只检查 query 参数 `token` 是否等于 `INTERNAL_REST_TOKEN`，失败时直接销毁 socket。

### rw-core/xray 启动方式

相关文件：

- `Dockerfile`
- `docker-entrypoint.sh`
- `supervisord.conf`
- `src/modules/xray-core/xray.service.ts`
- `src/common/utils/generate-api-config.ts`

`Dockerfile` 构建阶段通过 Remnawave install script 安装 Xray core 到 `/usr/local/bin/xray`，运行阶段创建符号链接：

```sh
ln -s /usr/local/bin/xray /usr/local/bin/rw-core
```

`supervisord.conf` 中 `program:xray` 的命令是：

```sh
/usr/local/bin/rw-core -config http+unix://%(ENV_INTERNAL_SOCKET_PATH)s/internal/get-config?token=%(ENV_INTERNAL_REST_TOKEN)s -format json
```

官方 Node 本身不直接把配置写文件给 Xray。`XrayService.startXray()` 的流程是：

1. 接收 `StartXrayCommand.Request`。
2. 用 `generateApiConfig()` 在 Panel 下发的 `xrayConfig` 上注入 API inbound、stats、policy、routing 等内部配置。
3. 调用 `InternalService.extractUsersFromConfig()` 保存完整配置并维护 inbound 用户 hash 状态。
4. 通过 supervisord `stopProcess/startProcess` 重启 `xray` program。
5. `rw-core` 启动时通过 `http+unix://.../internal/get-config?token=...` 从 Node 拉取完整 JSON 配置。
6. Node 再通过 Xray gRPC API 做启动健康检查。

### supervisord 的职责

相关文件：

- `docker-entrypoint.sh`
- `supervisord.conf`
- `src/app.module.ts`
- `src/modules/xray-core/xray.service.ts`

`supervisord` 在 entrypoint 中先于 Node 启动。它的职责是：

- 持有 `xray` 子进程定义，但 `autostart=false`，不会自动启动 core。
- 暴露带随机用户名/密码的 Unix socket XML-RPC API。
- 管理 `xray` 的 start/stop、进程状态和日志。
- 写入 `/var/log/supervisor/xray.out.log` 与 `/var/log/supervisor/xray.err.log`。

Nest 通过 `SupervisordNestjsModule.forRootAsync()` 连接：

```text
http://unix:${SUPERVISORD_SOCKET_PATH}:/RPC2
```

`XrayService.restartXrayProcess()` 使用 `getProcessInfo()`、`stopProcess()`、`startProcess()` 管理 `program:xray`。

### XTLS_API_PORT 的作用

相关文件：

- `Dockerfile`
- `src/common/utils/get-initial-ports.ts`
- `src/app.module.ts`
- `src/common/utils/generate-api-config.ts`
- `libs/contract/constants/xray/stats.ts`

默认值在 Dockerfile 中是：

```text
XTLS_API_PORT=61000
```

`getXtlsApiPort()` 从环境变量读取，缺省也返回 `61000`。它有两个用途：

1. `generateApiConfig()` 把 `REMNAWAVE_API_INBOUND` 注入 Xray 配置，监听 `127.0.0.1:${XTLS_API_PORT}`，协议是 `dokodemo-door`，并启用 TLS/mTLS。
2. `XtlsSdkNestjsModule` 创建 gRPC client，连接 `127.0.0.1:${XTLS_API_PORT}`，使用 `getClientCerts()` 返回的临时 client 证书，并设置 `grpc.ssl_target_name_override = internal.remnawave.local`。

该端口不是 Panel 访问端口，而是 Node 进程本地访问 Xray gRPC API 的端口。

## 2. Panel -> Node HTTP API 路由清单

全局前缀是 `/node`。除特殊说明外，以下外部路由都要求 HTTPS mTLS 和 Bearer JWT。

“第一阶段”列按 Go MVP 建议划分：

- `必须`：要真实实现，否则 Panel 无法可靠启动/识别节点。
- `可 stub`：建议先实现路由和响应 shape，但可返回空数据、false 或 success，占位不做真实副作用。
- `暂不实现`：第一阶段不建议实现；如果 Panel 会调用，至少要明确降级策略。

### Xray 路由

| method | path | request body DTO | response body DTO | 第一阶段 |
|---|---|---|---|---|
| POST | `/node/xray/start` | `StartXrayRequestDto` | `StartXrayResponseDto` | 必须 |
| GET | `/node/xray/stop` | 无 | `StopXrayResponseDto` | 必须 |
| GET | `/node/xray/healthcheck` | 无 | `GetNodeHealthCheckResponseDto` | 必须 |

来源：

- 路径常量：`libs/contract/api/controllers/xray.ts`、`libs/contract/api/routes.ts`
- Controller：`src/modules/xray-core/xray.controller.ts` 的 `startXray()`、`stopXray()`、`getNodeHealthCheck()`
- Schema：`libs/contract/commands/xray/*.command.ts`

DTO 摘要：

- `StartXrayRequestDto`：`{ internals: { forceRestart, hashes: { emptyConfig, inbounds[] } }, xrayConfig }`
- `StartXrayResponseDto`：`{ response: { isStarted, version, error, nodeInformation: { version }, system } }`
- `StopXrayResponseDto`：`{ response: { isStopped } }`
- `GetNodeHealthCheckResponseDto`：`{ response: { isAlive, xrayInternalStatusCached, xrayVersion, nodeVersion } }`

### Stats 路由

| method | path | request body DTO | response body DTO | 第一阶段 |
|---|---|---|---|---|
| POST | `/node/stats/get-user-online-status` | `GetUserOnlineStatusRequestDto` | `GetUserOnlineStatusResponseDto` | 可 stub |
| GET | `/node/stats/get-system-stats` | 无 | `GetSystemStatsResponseDto` | 必须 |
| POST | `/node/stats/get-users-stats` | `GetUsersStatsRequestDto` | `GetUsersStatsResponseDto` | 可 stub |
| POST | `/node/stats/get-inbound-stats` | `GetInboundStatsRequestDto` | `GetInboundStatsResponseDto` | 可 stub |
| POST | `/node/stats/get-outbound-stats` | `GetOutboundStatsRequestDto` | `GetOutboundStatsResponseDto` | 可 stub |
| POST | `/node/stats/get-all-inbounds-stats` | `GetAllInboundsStatsRequestDto` | `GetAllInboundsStatsResponseDto` | 可 stub |
| POST | `/node/stats/get-all-outbounds-stats` | `GetAllOutboundsStatsRequestDto` | `GetAllOutboundsStatsResponseDto` | 可 stub |
| POST | `/node/stats/get-combined-stats` | `GetCombinedStatsRequestDto` | `GetCombinedStatsResponseDto` | 可 stub |
| POST | `/node/stats/get-user-ip-list` | `GetUserIpListRequestDto` | `GetUserIpListResponseDto` | 可 stub |
| GET | `/node/stats/get-users-ip-list` | 无 | `GetUsersIpListResponseDto` | 可 stub |

来源：

- 路径常量：`libs/contract/api/controllers/stats.ts`、`libs/contract/api/routes.ts`
- Controller：`src/modules/stats/stats.controller.ts`
- Service：`src/modules/stats/stats.service.ts`
- Schema：`libs/contract/commands/stats/*.command.ts`

DTO 摘要：

- `GetUserOnlineStatusRequestDto`：`{ username }` -> `{ response: { isOnline } }`
- `GetSystemStatsResponseDto`：`{ response: { xrayInfo, plugins: { torrentBlocker: { reportsCount } }, system: { stats } } }`
- `GetUsersStatsRequestDto`：`{ reset }` -> `{ response: { users: [{ username, downlink, uplink }] } }`
- `GetInboundStatsRequestDto`：`{ tag, reset }` -> `{ response: { inbound, downlink, uplink } }`
- `GetOutboundStatsRequestDto`：`{ tag, reset }` -> `{ response: { outbound, downlink, uplink } }`
- `GetAllInboundsStatsRequestDto`：`{ reset }` -> `{ response: { inbounds: [{ inbound, downlink, uplink }] } }`
- `GetAllOutboundsStatsRequestDto`：`{ reset }` -> `{ response: { outbounds: [{ outbound, downlink, uplink }] } }`
- `GetCombinedStatsRequestDto`：`{ reset }` -> `{ response: { inbounds, outbounds } }`
- `GetUserIpListRequestDto`：`{ userId }` -> `{ response: { ips: [{ ip, lastSeen }] } }`
- `GetUsersIpListResponseDto`：`{ response: { users: [{ userId, ips: [{ ip, lastSeen }] }] } }`

### Handler 路由

| method | path | request body DTO | response body DTO | 第一阶段 |
|---|---|---|---|---|
| POST | `/node/handler/add-user` | `AddUserRequestDto` | `AddUserResponseDto` | 可 stub |
| POST | `/node/handler/remove-user` | `RemoveUserRequestDto` | `RemoveUserResponseDto` | 可 stub |
| POST | `/node/handler/get-inbound-users-count` | `GetInboundUsersCountRequestDto` | `GetInboundUsersCountResponseDto` | 可 stub |
| POST | `/node/handler/get-inbound-users` | `GetInboundUsersRequestDto` | `GetInboundUsersResponseDto` | 可 stub |
| POST | `/node/handler/add-users` | `AddUsersRequestDto` | `AddUsersResponseDto` | 可 stub |
| POST | `/node/handler/remove-users` | `RemoveUsersRequestDto` | `RemoveUsersResponseDto` | 可 stub |
| POST | `/node/handler/drop-users-connections` | `DropUsersConnectionsRequestDto` | `DropUsersConnectionsResponseDto` | 可 stub |
| POST | `/node/handler/drop-ips` | `DropIpsRequestDto` | `DropIpsResponseDto` | 可 stub |

来源：

- 路径常量：`libs/contract/api/controllers/handler.ts`、`libs/contract/api/routes.ts`
- Controller：`src/modules/handler/handler.controller.ts`
- Service：`src/modules/handler/handler.service.ts`
- Schema：`libs/contract/commands/handler/*.command.ts`

DTO 摘要：

- `AddUserRequestDto`：`{ data: [trojan|vless|shadowsocks|shadowsocks22|hysteria], hashData: { vlessUuid, prevVlessUuid? } }`
- `AddUsersRequestDto`：`{ affectedInboundTags, users: [{ inboundData, userData }] }`
- `RemoveUserRequestDto`：`{ username, hashData: { vlessUuid } }`
- `RemoveUsersRequestDto`：`{ users: [{ userId, hashUuid }] }`
- `GetInboundUsersCountRequestDto`：`{ tag }`
- `GetInboundUsersRequestDto`：`{ tag }`
- `DropUsersConnectionsRequestDto`：`{ userIds }`
- `DropIpsRequestDto`：`{ ips }`
- Add/remove 响应统一类似 `{ response: { success, error } }`；查询 count/users 和 drop 响应见对应 command schema。

注意：如果第一阶段只 stub add/remove，Panel 侧显示可能成功，但 Xray 运行时不会热更新用户；用户变更需要等下一次全量 `/node/xray/start` 重启才生效。

### Plugin 路由

| method | path | request body DTO | response body DTO | 第一阶段 |
|---|---|---|---|---|
| POST | `/node/plugin/sync` | `SyncRequestDto` | `SyncResponseDto` | 可 stub |
| POST | `/node/plugin/torrent-blocker/collect` | 无 | `CollectReportsResponseDto` | 可 stub |
| POST | `/node/plugin/nftables/block-ips` | `BlockIpsRequestDto` | `BlockIpsResponseDto` | 暂不实现 |
| POST | `/node/plugin/nftables/unblock-ips` | `UnblockIpsRequestDto` | `UnblockIpsResponseDto` | 暂不实现 |
| POST | `/node/plugin/nftables/recreate-tables` | 无 | `RecreateTablesResponseDto` | 暂不实现 |

来源：

- 路径常量：`libs/contract/api/controllers/plugin.ts`、`libs/contract/api/routes.ts`
- Controller：`src/modules/_plugin/plugin.controller.ts`
- Service：`src/modules/_plugin/plugin.service.ts`、`src/modules/_plugin/services/nft.service.ts`
- Schema：`libs/contract/commands/plugin/**/*.ts`

DTO 摘要：

- `SyncRequestDto`：`{ plugin: { config, uuid, name } | null }` -> `{ response: { accepted } }`
- `CollectReportsResponseDto`：`{ response: { reports } }`
- `BlockIpsRequestDto`：`{ ips: [{ ip, timeout }] }` -> `{ response: { accepted } }`
- `UnblockIpsRequestDto`：`{ ips }` -> `{ response: { accepted } }`
- `RecreateTablesResponseDto`：`{ response: { accepted } }`

### Vision 路由

| method | path | request body DTO | response body DTO | 第一阶段 |
|---|---|---|---|---|
| POST | `/vision/block-ip` | `BlockIpRequestDto` | `BlockIpResponseDto` | 暂不实现 |
| POST | `/vision/unblock-ip` | `UnblockIpRequestDto` | `UnblockIpResponseDto` | 暂不实现 |

来源：

- 路径常量：`libs/contract/api/controllers/vision.ts`、`libs/contract/api/routes.ts`
- Controller：`src/modules/vision/vision.controller.ts`
- Schema：`libs/contract/commands/vision/*.command.ts`

注意：

- 这两个路由在 `src/main.ts` 的 global prefix exclude 中按 `/vision/...` 处理，不带 `/node`。
- `VisionController` 没有 `JwtDefaultGuard`。
- 但当前 `src/modules/remnawave-node.modules.ts` 没有 import `VisionModule`，因此在该快照中它看起来不是实际启用的外部 Panel 路由。Go MVP 第一阶段不应优先实现。

### Internal 路由，非 Panel -> Node

| method | path | request body DTO | response body DTO | 第一阶段 |
|---|---|---|---|---|
| GET | `/internal/get-config?token=...` | 无 | `Record<string, unknown>` Xray JSON config | 必须 |
| POST | `/internal/webhook?token=...` | 任意 Xray webhook body | 无固定响应 | 可 stub |

来源：

- 常量：`libs/contract/constants/internal/internal.constants.ts`
- Controller：`src/modules/internal/internal.controller.ts`
- Service：`src/modules/internal/internal.service.ts`

这组路由只监听 Unix socket，不走外部 HTTPS 端口。

## 3. Node -> Xray 交互方式

### rw-core 启动命令

官方容器最终通过 supervisord 执行：

```sh
/usr/local/bin/rw-core -config http+unix://$INTERNAL_SOCKET_PATH/internal/get-config?token=$INTERNAL_REST_TOKEN -format json
```

相关点：

- `rw-core` 是 `/usr/local/bin/xray` 的符号链接。
- `autostart=false`，只有 `XrayService.startXray()` 被 Panel 调用后才通过 supervisord 启动。
- Xray 配置不是文件路径，而是 `http+unix://` URL。

Go MVP 可以选择继续使用 `supervisord`，也可以由 Go 进程直接管理子进程；但外部行为必须等价：`/node/xray/start` 后 core 使用最新 `/internal/get-config` 返回的完整 JSON 配置启动。

### `/internal/get-config` 的用途

`InternalController.getXrayConfig()` 调用 `InternalService.getXrayConfig()`。如果还没有配置则返回 `{}`，否则返回 `XrayService.startXray()` 之前保存的完整配置。

`XrayService.startXray()` 不直接保存 Panel 原始配置，而是先通过 `generateApiConfig()` 注入：

- `stats: {}`
- `api.services = ['HandlerService', 'StatsService', 'RoutingService']`
- `api.tag = 'REMNAWAVE_API'`
- 监听 `127.0.0.1:${XTLS_API_PORT}` 的 `REMNAWAVE_API_INBOUND`
- routing 规则，把 `REMNAWAVE_API_INBOUND` 转到 `REMNAWAVE_API`
- policy 中开启用户、入站、出站统计
- torrent blocker 启用时追加 blackhole outbound、webhook routing rule

这意味着 Go 实现不能只把 Panel 的 `xrayConfig` 原样交给 core，否则后续 stats/handler/router gRPC API 不会可用。

### `127.0.0.1:61000` gRPC API 的用途

`src/app.module.ts` 使用 `XtlsSdkNestjsModule` 连接 `127.0.0.1:${getXtlsApiPort()}`。TLS 证书来自 `initializeMTLSCerts()` 生成的内部证书，不来自 `SECRET_KEY`。

用途按服务分：

- `XrayService.getXrayInternalStatus()`：调用 `xtlsSdk.stats.getSysStats()` 判断 core 是否真的可用。
- `StatsService`：
  - `getSysStats()`
  - `getAllUsersStats(reset)`
  - `getInboundStats(tag, reset)`
  - `getOutboundStats(tag, reset)`
  - `getAllInboundsStats(reset)`
  - `getAllOutboundsStats(reset)`
  - raw client 的 `getStatsOnlineIpList()`、`getAllOnlineUsers()`
- `HandlerService`：
  - `addTrojanUser()`
  - `addVlessUser()`
  - `addShadowsocksUser()`
  - `addShadowsocks2022User()`
  - `addHysteriaUser()`
  - `removeUser()`
  - `getInboundUsers()`
  - `getInboundUsersCount()`
  - raw client 的 `removeOutbound()`
- `VisionService`：
  - `router.addSrcIpRule()`
  - `router.removeRuleByRuleTag()`

Go 实现真正支持 stats/handler/vision 时，需要复刻 `@remnawave/xtls-sdk` 封装的 protobuf/gRPC 行为。第一阶段可以只做 sys stats 健康检查，其他接口先返回兼容空响应。

## 4. `SECRET_KEY` 格式

相关文件：

- `src/common/utils/decode-node-payload/decode-node-payload.ts`
- `src/common/config/app-config/config.schema.ts`
- `src/main.ts`
- `src/common/guards/jwt-guards/strategies/validate-token.ts`

### base64 JSON 字段

`SECRET_KEY` 是 base64 编码的 UTF-8 JSON。解码后必须包含四个 string 字段：

```json
{
  "caCertPem": "...",
  "jwtPublicKey": "...",
  "nodeCertPem": "...",
  "nodeKeyPem": "..."
}
```

缺字段或字段不是 string 会被视为 invalid payload。

### PEM 归一化方式

`normalizePem()` 做这些处理：

1. 把字面量 `\n` 替换为真实换行。
2. 把 CRLF 归一成 LF。
3. 在 `-----BEGIN ...-----` 后补换行。
4. 在 `-----END ...-----` 前补换行。
5. 合并多个连续换行。
6. `trim()`。

Go 实现要复刻这套归一化，否则某些 Panel 发来的单行 PEM 或带转义换行的 PEM 会解析失败。

### mTLS 使用方式

`src/main.ts` 的 HTTPS server 使用：

- server private key：`nodeKeyPem`
- server certificate：`nodeCertPem`
- trusted client CA：`caCertPem`
- `requestCert=true`
- `rejectUnauthorized=true`

所以 `SECRET_KEY` 中的 `caCertPem` 是用来验证 Panel 客户端证书的 CA，不是内部 Xray gRPC 的 CA。

### JWT public key 使用方式

`jwtPublicKey` 经 `config.schema.ts` 写入 `JWT_PUBLIC_KEY`。`JwtStrategy` 用它按 `RS256` 验证 Bearer JWT。

Go 实现需要在每个受保护外部 API 上同时校验：

- TLS 客户端证书链是否由 `caCertPem` 信任。
- JWT 是否为 `RS256` 且通过 `jwtPublicKey` 验签，过期时间有效。

## 5. Go MVP 实现范围

### 必须实现

1. 环境配置解析：
   - `NODE_PORT`
   - `SECRET_KEY`
   - `XTLS_API_PORT`，默认 `61000`
   - 内部 socket path 和 token；可以由 Go 自己生成，也可以复用 entrypoint 环境变量。
2. `SECRET_KEY` base64 JSON 解析和 PEM 归一化。
3. 外部 HTTPS mTLS server。
4. 外部 Bearer JWT `RS256` 验证。
5. `/node/xray/start`：
   - 接收 `StartXrayCommand.Request` shape。
   - 保存 Panel 下发的 `xrayConfig`。
   - 注入 Remnawave API inbound、stats、api、policy、routing。
   - 启动或重启 `rw-core`。
   - 返回 `StartXrayCommand.Response` shape。
6. `/node/xray/stop`：
   - 停止 core。
   - 清空当前 config/cache。
   - 返回 `{ response: { isStopped } }`。
7. `/node/xray/healthcheck`：
   - 返回 Node alive、cached Xray 状态、xrayVersion、nodeVersion。
8. Unix socket internal server：
   - `GET /internal/get-config?token=...`
   - `POST /internal/webhook?token=...` 可以先 no-op。
9. 内部 Xray API mTLS 证书生成：
   - 需要一套内部 CA/server/client 证书。
   - Xray API inbound 使用 server cert。
   - Go gRPC client 使用 client cert。
10. `/node/stats/get-system-stats`：
    - 至少返回真实宿主系统 stats、`xrayInfo` 可先为 null、`torrentBlocker.reportsCount=0`。

### 可以 stub，但建议保留兼容响应

这些端点第一阶段建议实现 HTTP 路由和 response envelope，但可不做真实 gRPC 副作用：

- stats：
  - online status 返回 `false`
  - users/inbounds/outbounds stats 返回空数组或 0
  - IP list 返回空数组
- handler：
  - add/remove/drop 返回 `{ success: true, error: null }`
  - get inbound users/count 返回 `[]` 或 `0`
- plugin：
  - sync 返回 `{ accepted: false }` 或无插件时的安全值
  - torrent reports 返回 `[]`

这些 stub 的代价是：Panel 可能显示操作成功，但运行中的 Xray 不会热更新用户、不会收集精细统计。若用户量、流量统计或在线状态是第一阶段验收目标，就不能 stub 对应 stats/handler。

### 暂时不实现

- 动态用户热更新：
  - `HandlerService.addUser()`
  - `HandlerService.addUsers()`
  - `HandlerService.removeUser()`
  - `HandlerService.removeUsers()`
- Xray router 动态规则：
  - `VisionService.blockIp()`
  - `VisionService.unblockIp()`
- nftables 插件：
  - `NftService.blockIpsController()`
  - `NftService.unblockIpsController()`
  - `NftService.recreateTablesController()`
- torrent blocker：
  - Xray webhook 事件处理
  - report 缓存与 flush
  - blackhole outbound 动态注入之外的完整行为
- hashed-set restart 优化：
  - `InternalService.isNeedRestartCore()` 第一阶段可以简化为每次 start 都重启 core。
- supervisord 完全兼容：
  - Go MVP 可以不用 supervisord，但需要达到等价的 start/stop/log/cleanup 行为。

## 6. 风险点

### AGPL 许可证

`package.json` 标注 `license: AGPL-3.0-only`。Go 版本应避免复制官方源码实现，尤其是大段函数、模型或生成逻辑。只按公开接口和行为做兼容分析与独立实现。若项目会分发或提供网络服务，需要单独确认 AGPL 对派生作品、网络交互和源码提供义务的影响。

### Panel 版本兼容

本分析基于参考仓库当前快照，`package.json` 为 `2.7.0`。Panel 可能与 Node contract 强绑定：

- 路由路径来自 `libs/contract/api/routes.ts`。
- 请求/响应 shape 来自 `libs/contract/commands/**/*.ts`。
- Panel 可能依赖 stub 之外的真实副作用，例如用户热更新或流量 reset。

建议后续把 `libs/contract/commands` 中的 schema 手工转成 Go contract tests，避免升级 Panel 后 silent break。

### Xray gRPC protobuf

官方通过 `@remnawave/xtls-sdk` 和 `@remnawave/xtls-sdk-nestjs` 访问 Xray gRPC。Go 要真实实现 stats/handler/router，需要确认：

- 使用的是 stock Xray API 还是 Remnawave `rw-core` 扩展 API。
- `getStatsOnlineIpList()`、`getAllOnlineUsers()`、webhook routing 等能力是否有公开 proto。
- TLS/mTLS、ALPN `h2`、`internal.remnawave.local` SNI/target name override 是否必须完全一致。

这是 Go 版从“能启动”走向“完整兼容”的核心风险。

### nftables/CAP_NET_ADMIN

官方 `HandlerService.onModuleInit()` 和 `NftService.onModuleInit()` 都检查 `hasCapNetAdmin()`。没有 `CAP_NET_ADMIN` 时：

- 用户在线 IP 获取会降级。
- connection drop、ingress filter、egress filter、torrent blocker、nftables block/unblock 都不可用或返回 false。

Go 容器如果要实现这些功能，需要 Linux capability、nftables 依赖和权限模型配套；Windows/macOS 本地开发环境通常无法验证完整行为。

### stats/handler 后续实现难点

stats 和 handler 不是简单 HTTP CRUD：

- `InternalService.extractUsersFromConfig()` 会从 inbound clients 提取用户 UUID，维护 `HashedSet` 和 inbound tag 集合。
- `isNeedRestartCore()` 用 Panel 下发的 hash 判断是否可以跳过 core 重启。
- `HandlerService.addUser()` 会先从所有已知 inbound 删除旧用户，再按协议重新添加。
- 支持协议包括 `trojan`、`vless`、`shadowsocks`、`shadowsocks22`、`hysteria`，每种映射到不同 gRPC 方法和字段。
- remove 用户后会查询在线 IP 并发布 drop connections 事件。
- stats 的 reset 语义会改变 Xray 内部计数器，需要和 Panel 轮询节奏匹配。
- 大规模用户 stats 可能很大，官方 JSON body limit 到 `1000mb`，并引入 zstd body parser。

建议 Go MVP 先稳定启动链路和 contract 响应，再逐步补齐 gRPC stats、动态 handler、connection drop 和插件。

---

## 附录 A：lite-go v0.8.2 实现对照

> 以下对照 **当前代码库** 相对本文档早期「MVP / stub / 暂不实现」建议的落地情况。正文第 5–6 节保留为历史规划参考。

| 分析项（正文章节） | 官方要求 | lite-go v0.8.2 | 代码位置 |
|-------------------|----------|----------------|----------|
| mTLS + JWT | Panel 双向 TLS + Bearer | ✅ | `internal/httpserver`, `internal/auth` |
| zstd + 1000MB body | `@kastov/body-parser-with-zstd` | ✅（低内存 64MB 可选） | `internal/bodylimit` |
| Unix get-config / webhook | internal socket + token | ✅ | `internal/unixconfig` |
| Xray start/stop/healthcheck | supervisord 等价 | ✅ | `internal/xray/manager.go` |
| 内部 mTLS 三件套 | CA + server + **client** cert | ✅ | `internal/xray/certs.go` |
| HashedSet 重启优化 | `isNeedRestartCore()` | ✅ | `internal/xray/hashedset.go` |
| Stats 10 路由 | gRPC 真实数据 + 失败 HTTP 错误 | ✅ A010–A017 | `internal/stats/` |
| Handler 8 路由 | 5 协议热更新 | ✅ | `internal/nodehandler`, `internal/xtls/handler.go` |
| Plugin sync + schema | `NodePluginSchema` | ✅ 0.4.4 对齐 | `internal/plugin/schema_validate.go` |
| nftables | CAP_NET_ADMIN + nft | ✅ | `internal/plugin/nft_linux.go` |
| torrent-blocker | webhook + outbound | ✅ | `internal/plugin/torrent.go` |
| Vision block/unblock | Router gRPC | ✅ | `internal/vision`, `internal/xtls/router.go` |
| drop connections / drop-ips | `ss -K` | ✅ | `internal/connections`, `internal/netadmin` |
| CAP_NET_ADMIN 部署 | docker `cap_add: NET_ADMIN` | ✅ systemd `AmbientCapabilities` | `deploy/remnawave-node.service` |
| contract golden tests | DTO shape | ✅ 28 路由 | `internal/contract/` |
| contract-sync CI | 跟踪 upstream | ✅ | `.github/workflows/contract-sync.yml` |
| 部署自检 | — | ✅ `remnanode-lite doctor` | `internal/doctor/` |
| 错误码 errorCode | 统一 A00x | ✅ JWT A003 + Stats A009–A017 + Handler A001/A014 | `internal/auth`, `internal/stats/errors.go`, `internal/nodehandler/errors.go` |
| compression / helmet | Express 中间件 | ❌ 未做 | 无影响 |
| CUSTOM_CORE_URL | entrypoint 下载 | ❌ 未做 | 可选 |
| Docker 镜像 | 官方主分发 | ❌ 未做 | 可选 |
| geo-zapret.dat | volume 挂载 | ❌ 未做 | 可选 |

**Panel 主流程结论**：v0.8.2 已覆盖正文第 5 节「必须实现」与「可 stub」项中的生产必需部分；第 5 节「暂时不实现」列表在 v0.8.2 中 **均已实现**（除上表标记 ❌ 的可选项）。

**验证命令**：

```bash
go test ./...                      # 全量测试（含 stats 回归）
go test ./internal/contract/...      # 28 路由 DTO
sudo remnanode-lite doctor         # 部署环境
grep AmbientCapabilities /etc/systemd/system/remnawave-node.service
```

---

## 附录 B：v0.8.2 审计与修复记录

> 2026-06 对照官方 v2.7.0 全量代码审计结论；下列 **已修复** 项在本分支代码中落地。

### 功能缺失（仍可选 / 设计不做）

| 项 | 说明 |
|----|------|
| Docker / supervisord / entrypoint | lite 单二进制 + systemd，有意不做 |
| `CUSTOM_CORE_URL` | 需手动安装 rw-core |
| CLI `dump-config` / `kill-sockets` | 有 `doctor` + `release-url` 子命令替代部分能力 |
| compression / helmet | 无 Panel 影响 |

### 已修复 BUG

| 项 | 修复 |
|----|------|
| Stats gRPC 失败静默返回 200+空数据 | 返回 HTTP 500 + `errorCode` A010–A017 |
| `get-users-stats` 未过滤零流量 | 对齐官方 `uplink/downlink !== 0` 过滤 |
| Xray `Start` 并发 TOCTOU | `startProcessing` 检查后立即置位 + defer 释放 |
| `add-users` 批量失败语义 | 对齐官方始终 `success: true` |
| plugin config hash | 对齐 `node-object-hash`（trim+sort:false）+ node 向量测试 |

### 已清理孤立代码

- 删除 `pluginUUID()`、`discardBody()`（httpserver/stats）
- `version.ReleaseAssetURL` / `InstallScriptURL` 接入 CLI：`remnanode-lite release-url`；`upgrade.sh` 优先调用

### 仍建议后续改进

- 256MB VPS 生产联调实测

---

## 附录 C：2026-06-08 官方对比审计（upstream main @ v2.7.0）

> 对照 [remnawave/node](https://github.com/remnawave/node) `main` 分支 `package.json` version **2.7.0**；本地执行 contract-sync 路由 diff：**28/28 无缺失、无多余**。

### contract-sync 结果

| 项 | 结果 |
|----|------|
| upstream 版本 | 2.7.0（与 lite-go 参考版本一致） |
| 官方路由数 | 28 |
| lite-go 覆盖 | 28/28 ✅ |
| CI 工作流 | `.github/workflows/contract-sync.yml` 每周一自动对比 |

### 功能缺失（相对官方，有意不做）

| 类别 | 说明 | Panel 影响 |
|------|------|-----------|
| Docker / supervisord | lite 用 systemd + 单二进制 | 无 |
| `CUSTOM_CORE_URL` | 需手动安装 rw-core | 无 |
| CLI `dump-config` / `kill-sockets` | 有 `doctor` 替代 | 无 |
| compression / helmet | Express 中间件 | 无 |
| geo-zapret.dat volume | Docker 专用 | 无 |
| sockdestroy → `ss -K` | 踢连接实现不同 | 需 CAP_NET_ADMIN |

### 行为差异（已评估）

| 端点/模块 | 官方 | lite-go（审计后） | 说明 |
|-----------|------|-------------------|------|
| Stats 流量类 | gRPC 失败 → 500 A010–A017 | 同 | 已对齐 |
| Stats 在线/IP | NOT_FOUND → 200 空/false；其他错误 → 200 空/false | NOT_FOUND → 200；其他 → **500 A009** | **混合策略**，比官方更可观测 |
| Handler get-inbound-* | gRPC 失败 → 500 **A014** | 同 | 已对齐 |
| Handler add/remove catch | 异常 → 500 **A001** | panic recover → 500 **A001** | 已对齐 |
| Handler add/remove 全失败 | 200 + `success:false` | 同 | 已对齐 |
| plugin torrent 禁用 | 看请求体 `includeRuleTags` | 看状态 `nowIncludeTags` | lite 避免无谓全停 Xray |
| 进程管理 | supervisord | exec + monitorProcess | 等价 |

### 本次修复（审计落地）

| 项 | 修复 |
|----|------|
| Stats 在线/IP 静默失败 | 混合策略：NOT_FOUND 静默，其他 gRPC 错误 → 500 A009 |
| Stats 错误码 A014 冲突 | A014 归还官方 Handler；Stats 在线/IP 改用 A009 |
| Handler get-inbound 失败 | 500 + errorCode A014 |
| Handler panic/异常 | recover → 500 + A001 |
| torrent 禁用 + 残留 tags | `nowIncludeTags` 判断 → RemoveOutbound 轻量路径 |
| zstd decoder 泄漏 | `zstdReadCloser` 正确 Close |
| 孤立代码 | 删除 `HandlerRemoveUserFromAllInbounds`；`.gitignore` 排除 `node_modules` |

### Plugin / Xray 模块评估

| 模块 | 对齐度 | 备注 |
|------|--------|------|
| Plugin sync/hash/nft/torrent | ~95% | torrent 禁用逻辑优于官方 |
| Xray Start/Stop/HashedSet | ~92% | gRPC 就绪检测策略略不同（500ms 轮询 vs pRetry 2s） |
| REST Contract | 100% | 28 路由全覆盖 |

### 总体结论

**无阻塞性 BUG**。lite-go v0.8.2 可替代官方 Docker 节点完成 Panel 主流程。关注 upstream 版本升级时重跑 contract-sync，以及 256MB VPS 生产联调。
