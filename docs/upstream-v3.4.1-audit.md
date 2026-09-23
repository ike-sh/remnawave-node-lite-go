# Official Node 3.4.1 compatibility audit

Source of truth: the three immutable `remnawave/node` tag commits, checked in a separate temporary clone:

| Tag | Commit |
| --- | --- |
| `3.3.2` | `afdfa2d837118efd95c317700e60e9429a169b48` |
| `3.4.0` | `c1fdad7656281c18d99f4e82045705fbfe32bab4` |
| `3.4.1` | `44912631321664dbd5822e9bf8d96766ccff7c93` |

## Source changes and Go mapping

| Upstream change | Official source / commit | Go Lite effect |
| --- | --- | --- |
| Optional derived SNI gate, default false | `84a7995`, `src/main.ts`, `src/common/config/app-config/config.schema.ts` | `config`, `httpserver`, `doctor`; TLS 1.3 and `RequireAndVerifyClientCert` stay enabled |
| Handler endpoint removal | `acfe034`, `libs/contract/api/routes.ts`, controller, commands, DTOs | Remove two routes, handlers, gRPC wrappers and old response tests |
| Result/error refactor | `acfe034`, `result.type.ts`, error helpers, services | Preserve known A-code HTTP errors; add `path` to coded errors; match socket abort on JWT/unknown route |
| Drop prior user connections when `prevVlessUuid` supplied | `5d993c8`, `src/modules/handler/handler.service.ts` | Query prior user IPs before removal, then drop connections before adding replacement |
| nft logging/reply switches | `f4f1b4b`, `src/modules/_plugin/services/nft.service.ts`, config schema | Add `NFTABLES_LOGGING=true`, `NFTABLES_ACCEPT_REPLY_TRAFFIC=false`; build matching ingress rules |
| Contract package version | `fe5ede1`, `libs/contract/package.json` | Wire contract is **3.4.1** (3.4.0 tag still has package version 3.2.3) |
| Zod 4.5.4 | `ad060a0`, `src/main.ts` | `zod/compile` is a Node implementation detail; only new boolean schema behavior affects Go config |
| Node.js, s6, Docker layout | `26d152d`, `6dd116c`, `docker/Dockerfile`, `docker/rootfs` | No Node runtime dependency in Go; existing systemd/OpenRC directory, socket, signal behavior retained |

The `3.3.2...3.4.0` diff changes the SNI gate, removes two REST commands, refactors result handling, adds nft config switches, changes Docker packaging and upgrades dependencies. The `3.4.0...3.4.1` diff changes the contract package version to 3.4.1, imports `zod/compile`, and adds the `prevVlessUuid` connection drop. No other route or command schema was changed in the latter diff.

## REST and error contract

The v3.4.1 `REST_API` has 25 routes: XRAY 3, STATS 11, HANDLER 6, PLUGIN 5. `GET /node/xray/stop` is the only stop method. The two removed Handler paths had been POST routes in the upstream source, despite some descriptions calling them GET. No routes were added. All surviving `libs/contract/commands` request and response schemas are unchanged relative to 3.3.2; the two removed commands and their DTOs are the command-level diff. Authentication still uses the RS256 JWT guard on all public controllers, with mTLS at HTTPS setup. Request query parameters were not added.

`internal/contract/routes.snapshot` records all 25 method/path pairs. `scripts/extract-contract-methods.py` checks the tagged `routes.ts` against controller decorators and extracts the Go router inventory. CI compares both against the snapshot and verifies the exact upstream commit. The weekly run also checks the newest official semver tag for changed routes, command/DTO trees and runtime config schema, failing when a new audit is needed.

The error refactor replaces `ICommandResponse` with a discriminated `TResult`; known error codes and HTTP status mapping remain in `error-handler.helper.ts`. Successful responses no longer fail merely because the response value is falsy. Known A-code failures still produce `timestamp`, `path`, `message`, and `errorCode`. Official POST business responses, including `success:false` and `accepted:false`, use HTTP **201**; Go now does the same. The official JWT guard and NotFound filter destroy the socket; Go uses `http.ErrAbortHandler` to close the connection or HTTP/2 stream.

## Official 3.4.1 HTTP differential acceptance

The actual `ghcr.io/remnawave/node:3.4.1` image (`sha256:0cdf386dd49f360fc885bb34bde21132e478e40f0deac62d616086ec0fa9257e`) was run alongside the current Go binary in disposable containers. Both used the same generated RSA CA, localhost server certificate, client certificate, RS256 JWT key and base64 Secret Key. HTTPS used TLS 1.3, verified CA/server identity and a valid client certificate; no production credentials or insecure TLS option were used. `tests/compat/fixturegen` creates disposable credentials outside the repository, `tests/compat/compare.py` records raw bytes, headers, parsed JSON, nested type shape and connection behavior, and `tests/compat/check.py` checks the pinned official fixture in `tests/compat/testdata/upstream-v3.4.1/errors.json`.

| Case group | Real official 3.4.1 | Go after differential fixes | Wire verdict |
| --- | --- | --- | --- |
| Malformed JSON (3, including missing JWT) | 400 JSON, `message/error/statusCode` | Same status and top-level shape | Match; parser runs before guard |
| Empty, missing, wrong type (4) | 400 JSON, `statusCode/message/errors` | Same status and top-level shape | Match |
| Extra property | 201, unused field stripped | 201, unused field ignored | Match |
| Bad UUID, enum, IP/CIDR (8) | 400 validation envelope | 400 validation envelope | Match for Panel consumer; nested issue details differ |
| Additional nested DTO, cipher enum and plugin UUID (5) | 400 validation envelope | 400 validation envelope | Match for Panel consumer |
| Plugin top-level missing config (1) | 400 validation envelope | 400 validation envelope | Match |
| Plugin config invalid (5) | 201 `accepted:false` | 201 `accepted:false` | Match |
| JWT missing/random/malformed/bad signature/expired | Connection closed without response | Connection closed without response | Match |
| Signed JWT with different identity claims | 200 accepted | 200 accepted | Match; official strategy constrains RS256/expiration, not issuer/audience/subject |
| Unknown GET/POST and wrong stop method | Connection closed | Connection closed | Match |
| Stats/RPC failure and nonexistent inbound | 500, A010/A012, timestamp/path/message | Same status, keys and coded message | Match |
| nft unavailable | 201 `accepted:false` | 201 `accepted:false` | Match |

**40/40 requests matched on connection behavior, HTTP status, JSON media type and top-level response keys.** The checker reported zero wire-significant failures and 45 incidental raw/nested differences. The official reference container had rw-core v26.7.28, while the minimal Go comparison container intentionally lacked Xray; its health-check `xrayVersion:null` is an environment difference rather than an error-contract difference.

**Exact-body difference:** malformed JSON uses a V8/body-parser position-specific message upstream and `Invalid JSON body` in Go. UUID/enum/IP validation issues contain Zod regex, union and punctuation details upstream; Go reports simpler issue objects. JSON field ordering, a trailing newline and dynamic timestamps also differ. The official image emits Helmet/security, keep-alive and ETag headers that the Go response does not; JSON media type and any `Allow` header were compared and matched.

**Consumer impact:** the pinned Panel backend `src/common/axios/axios.service.ts` reads `response.data.response` on 2xx. For handled 500 errors it extracts top-level `message` (or `error`); it does not inspect Zod's nested `errors` array or depend on parser punctuation. For 400 errors it uses the Axios failure path rather than parsing Zod issue details. The known A-code messages and paths matched, so the observed exact-body differences have no Panel consumer impact.

**Compatibility verdict:** wire-compatible for the 40 observed error/authentication vectors. The validation path uses the existing configured request body limit, with no separate small-body bypass. Because it must replay valid JSON to the existing handlers, large accepted requests may have higher transient memory use; this is a performance limit to monitor on low-memory hosts.

## Dependency behavior

| Dependency | Source comparison | Go behavior |
| --- | --- | --- |
| `@remnawave/node-plugins` 0.7.3 → 0.8.2 | npm tarballs: all 12 published `build/` files are byte identical | Existing `sharedLists`, `asList`, `rulePlacement`, webhook, pre-start, ingress/egress and torrent validation remain applicable |
| `nftables-napi` 0.5.0 → 0.7.1 | npm C++ `table_ops.cpp`: optional ingress log and conntrack reply accept; `set_ops.cpp` unchanged | New flags match official Node defaults; IPv4/IPv6 rules, interval sets, CIDR merge, duplicate ports, idempotent remove and shutdown cleanup audited |
| `sockdestroy` 1.4.0 → 2.0.1 | npm C socket/netlink implementation files are byte identical; addon adds Node-API v10 build guard | Go `ss -K` still matches TCP source and destination by family; input dedup and partial failure logging added without exposing socket errors as REST rejection |
| Zod 4.4.3 → 4.5.4 | only package versions and `zod/compile` import; surviving contract schemas unchanged | Official `true`/`false` boolean parsing applied to runtime switches; observed 400 validation envelope verified below |

The official ASN Docker stage now downloads `asn-prefixes.lmdb.zst` and opens `asn-prefixes.lmdb` as `{ipv4,ipv6}` prefix arrays keyed by ASN. The Go release build converts the corresponding official asn-index JSON asset into its compact database and ships that database in both Linux archives. Install and upgrade scripts place it only when no local database exists. The current official JSON snapshot contained 86,800 ASN records; every record's IPv4/IPv6 prefix arrays were compared against the generated Go database without mismatch. Multiple ASN entries, overlap and duplicate expansion are tested through the resolver and nft prefix normalization; malformed or missing databases degrade safely. The official Dockerfile still pins rw-core **v26.7.28** and GeoCheck **v0.3.0**.

## Deployment and verification limits

The upgrade script does not write `node.env`; existing ports, Secret Key, custom variables, data and rw-core remain in place. New install templates write `SNI_VERIFICATION=false`. On an old v1.3.0 `node.env` without that field, the Go loader defaults to false. The systemd and OpenRC files retain their service capabilities and launch paths.

The actual unit files were exercised with the current workspace binary and disposable Secret Key, not a downloaded release. A privileged Ubuntu 24.04 container running systemd as PID 1 completed `daemon-reload`, `start`, HTTPS/mTLS/JWT health request, `restart`, and `stop`: active on both starts, changed MainPID, inactive after stop, `ExecMainStatus=0`, `NRestarts=0`, correct WorkingDirectory and RuntimeDirectory, and no crash loop in `journalctl`. An Alpine 3.21 container running `openrc-init` as PID 1 completed `rc-service start/status/restart/stop`: health returned 200, PID changed, pidfile disappeared on stop, and OpenRC logs showed no service crash. These tests used temporary isolated containers and did not modify host services.

Linux nftables and CAP_NET_ADMIN execution needs an isolated Linux network namespace; the default suite skips that opt-in integration. It was also run in an isolated Docker network namespace with `nft` and `CAP_NET_ADMIN`, covering recreate twice, empty sets, IPv4/IPv6 block and unblock, CIDR/overlap, duplicate ports, repeated operations, removal of stale state, and opt-in conntrack reply acceptance with logging disabled. A separate no-capability container check passed. The Linux `go test -race ./...` suite passed in the Go 1.26.6 container. Socket integration in the Docker Desktop WSL2 kernel (`6.6.87.2-microsoft-standard-WSL2`, iproute2 6.15.0, root with `CAP_NET_ADMIN`) confirmed IPv4/IPv6 filter syntax and no-match behavior; that kernel silently skipped `SOCK_DESTROY` for actual TCP connections, and Go detected/logged remaining matching sockets. The success-path test now uses a separate iproute2 `ss -K` capability probe on an established test connection: this host reports an explicit `SKIP`, while the no-match and silent-failure tests pass. A privileged container was also tried, with the same kernel limitation. Successful destruction on a capable native Linux kernel remains unverified locally; an isolated Docker integration step in CI is set up to run it on the Ubuntu runner.
