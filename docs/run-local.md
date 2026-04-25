# Run Local

This project is still an MVP. It can start `rw-core` and serve the minimal Node routes, but it does not yet implement stats, handler hot updates, nftables, or torrent blocker behavior.

## Prepare `.env`

Create `.env` in the repository root:

```env
NODE_PORT=2222
SECRET_KEY=base64-json-payload-from-panel
XTLS_API_PORT=61000
XRAY_BIN=/usr/local/bin/rw-core
GEO_DIR=/usr/local/share/xray
LOG_DIR=./logs
INTERNAL_SOCKET_PATH=
INTERNAL_REST_TOKEN=
```

Required values:

- `NODE_PORT`: HTTPS listen port for Panel -> Node traffic.
- `SECRET_KEY`: base64 JSON payload containing `caCertPem`, `jwtPublicKey`, `nodeCertPem`, and `nodeKeyPem`.

Optional values:

- `XTLS_API_PORT`: local Xray API inbound port, default `61000`.
- `XRAY_BIN`: path to `rw-core`, default `/usr/local/bin/rw-core`.
- `GEO_DIR`: Xray asset directory, passed as `XRAY_LOCATION_ASSET`, default `/usr/local/share/xray`.
- `LOG_DIR`: Xray stdout/stderr directory, default `./logs`.
- `INTERNAL_SOCKET_PATH`: Unix socket for `/internal/get-config`; generated automatically when empty.
- `INTERNAL_REST_TOKEN`: token for `/internal/get-config`; generated automatically when empty.

## Specify `XRAY_BIN`

For a local build of Xray/rw-core:

```env
XRAY_BIN=/absolute/path/to/rw-core
```

The process is launched as:

```sh
rw-core -config http+unix://<INTERNAL_SOCKET_PATH>/internal/get-config?token=<INTERNAL_REST_TOKEN> -format json
```

## Start

```sh
go run ./cmd/remnanode-lite
```

Or build and run:

```sh
go build ./cmd/remnanode-lite
./remnanode-lite
```

On Windows the binary will be `remnanode-lite.exe`.

## Check mTLS With `curl`

Panel-facing routes require both mTLS and Bearer JWT:

```sh
curl -vk \
  --cert panel-client.crt \
  --key panel-client.key \
  --cacert ca.crt \
  -H "Authorization: Bearer <panel-jwt>" \
  https://127.0.0.1:2222/node/xray/healthcheck
```

Without a trusted client certificate, the TLS handshake should fail. With a client certificate but without a valid JWT, the server should return `401`.

To start Xray, send the official `StartXrayCommand.Request` shape:

```sh
curl -vk \
  --cert panel-client.crt \
  --key panel-client.key \
  --cacert ca.crt \
  -H "Authorization: Bearer <panel-jwt>" \
  -H "Content-Type: application/json" \
  https://127.0.0.1:2222/node/xray/start \
  -d '{
    "internals": {
      "forceRestart": true,
      "hashes": {
        "emptyConfig": "local",
        "inbounds": []
      }
    },
    "xrayConfig": {
      "inbounds": [],
      "outbounds": [],
      "routing": { "rules": [] }
    }
  }'
```

## Logs

`rw-core` stdout and stderr are written to:

```text
logs/xray.out.log
logs/xray.err.log
```

Change the directory with `LOG_DIR`.
