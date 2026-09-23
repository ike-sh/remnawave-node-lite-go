# Official 3.4.1 HTTP differential harness

This is a test-only harness. It uses the pinned official image and disposable
RSA CA, server/client certificates and JWT keys. Never point it at production.

On a machine with Go, Python 3 and Docker, from the repository root:

```bash
tmp="$(mktemp -d)"
go run ./tests/compat/fixturegen -out "$tmp"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$tmp/remnanode-lite" ./cmd/remnanode-lite
docker run -d --rm --name rnl-official-341 --env-file "$tmp/official.env" \
  -p 127.0.0.1:12441:2222 \
  ghcr.io/remnawave/node:3.4.1@sha256:0cdf386dd49f360fc885bb34bde21132e478e40f0deac62d616086ec0fa9257e
docker run -d --rm --name rnl-lite-341 --env-file "$tmp/go.env" \
  -p 127.0.0.1:12442:2222 \
  -v "$tmp/remnanode-lite:/audit/remnanode-lite:ro" \
  debian:bookworm-slim sh -c \
  'cp /audit/remnanode-lite /usr/local/bin/remnanode-lite && chmod 755 /usr/local/bin/remnanode-lite && exec /usr/local/bin/remnanode-lite'
python3 tests/compat/compare.py --fixture-dir "$tmp" --output "$tmp/differential.json"
python3 tests/compat/check.py "$tmp/differential.json"
docker rm -f rnl-official-341 rnl-lite-341
```

The comparison file contains raw HTTP responses, headers, parsed JSON,
request metadata with symbolic Authorization labels, and connection outcomes.
It contains no JWT signing key or real bearer token. The checked-in official
fixture is `testdata/upstream-v3.4.1/errors.json`; dynamic timestamps and HTTP
header values are represented by matchers. `check.py` fails on status,
connection behavior, JSON media type, top-level keys, known coded errors, and
response envelope differences. It separately counts raw wording, header and
nested Zod issue differences for review.

The fixture generator writes private keys only to `tmp`; remove that temporary
directory when finished. The official and Go test containers use only loopback
published ports and their own disposable Docker network namespaces.
