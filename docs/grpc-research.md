# Xray gRPC 调研笔记

## 连接参数

与官方 `@remnawave/xtls-sdk-nestjs` 一致：

| 参数 | 值 |
|------|-----|
| 地址 | `127.0.0.1:${XTLS_API_PORT}`（默认 61000） |
| TLS SNI | `internal.remnawave.local` |
| ALPN | `h2` |
| 客户端证书 | 启动时生成的 internal client cert |
| CA | 同一套 internal CA |

## Go 依赖

使用 stock Xray proto（与 rw-core 同版本对齐）：

```
github.com/xtls/xray-core/app/stats/command
google.golang.org/grpc
```

当前锁定：`xray-core v1.260327.0`（对应 rw-core v26.3.27）

## StatsService 方法映射

| Panel 路由 | xtls-sdk 方法 | gRPC 调用 |
|-----------|--------------|----------|
| get-system-stats | getSysStats | GetSysStats |
| get-users-stats | getAllUsersStats | QueryStats(pattern=`user>>>`) |
| get-user-online-status | getUserOnlineStatus | GetStatsOnline(name=`user>>>X>>>online`) |
| get-inbound-stats | getInboundStats | QueryStats(pattern=`inbound>>>tag>>>`) |
| get-outbound-stats | getOutboundStats | QueryStats(pattern=`outbound>>>tag>>>`) |
| get-all-inbounds-stats | getAllInboundsStats | QueryStats(pattern=`inbound>>>`) |
| get-all-outbounds-stats | getAllOutboundsStats | QueryStats(pattern=`outbound>>>`) |

## 统计名格式

Xray 计数器命名（`>>>` 分隔）：

```
user>>>{email}>>>traffic>>>uplink
user>>>{email}>>>traffic>>>downlink
user>>>{email}>>>online
inbound>>>{tag}>>>uplink
inbound>>>{tag}>>>downlink
outbound>>>{tag}>>>uplink
outbound>>>{tag}>>>downlink
```

## 已实现

- `get-user-ip-list`：`GetStatsOnlineIpList(name=user>>>X>>>online)`
- `get-users-ip-list`：`GetAllOnlineUsers` + 并发 IP 查询（50 worker）
- HandlerService gRPC（5 协议 + removeOutbound）
- RoutingService gRPC（Vision IP 封禁）

## 尚未实现
- rw-core 扩展 `GetUsersStats` RPC（新版才有，UNIMPLEMENTED 时走 legacy `QueryStats`）

## 健康检查

`start` 完成后通过 mTLS gRPC `GetSysStats` 探测可达性，替代原先纯 TCP 端口检测。
