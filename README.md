# remnawave-node-lite-go

Go MVP skeleton for a lightweight Remnawave Node compatible implementation.

This is not a complete replacement for the official Remnawave Node. The current MVP only implements:

- mTLS HTTPS server using `SECRET_KEY` node certificate material.
- RS256 Bearer JWT verification using `SECRET_KEY.jwtPublicKey`.
- `GET /node/xray/healthcheck`.
- `GET /node/xray/stop`.
- `POST /node/xray/start`, which injects the Remnawave API config and starts `rw-core`.
- Unix-socket `GET /internal/get-config`, which returns the generated in-memory Xray config to `rw-core`.

## Requirements

- Go 1.23 or newer.

## Configuration

Configuration is read from `.env`, then overridden by process environment variables.

Required:

- `NODE_PORT`
- `SECRET_KEY`

Optional:

- `XTLS_API_PORT`, default `61000`
- `XRAY_BIN`, default `/usr/local/bin/rw-core`
- `GEO_DIR`, default `/usr/local/share/xray`
- `LOG_DIR`, default `./logs`
- `INTERNAL_SOCKET_PATH`, generated automatically when empty
- `INTERNAL_REST_TOKEN`, generated automatically when empty

`SECRET_KEY` must be a base64-encoded JSON object with:

- `caCertPem`
- `jwtPublicKey`
- `nodeCertPem`
- `nodeKeyPem`

## Commands

```sh
go test ./...
go build ./cmd/remnanode-lite
```

## Compatibility Status

This MVP now manages the `rw-core` child process directly with Go `os/exec`, without supervisord. It still does not implement Xray gRPC stats, dynamic handler updates, nftables plugins, torrent blocker, or full official Node behavior. It is a starting point for incremental compatibility work based on `docs/compat-analysis.md`.

## License

This repository is licensed under AGPL-3.0-only. See `LICENSE`.
