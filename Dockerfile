# syntax=docker/dockerfile:1.7.0@sha256:dbbd5e059e8a07ff7ea6233b213b36aa516b4c53c645f1817a4dd18b83cbea56

# Multi-architecture manifest digests audited on 2026-07-19.
ARG GO_IMAGE=golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651
ARG DEBIAN_IMAGE=debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN set -eux; \
    install -d /out; \
    version="$(sed -n 's/^var Version = "\([^"]*\)"$/\1/p' internal/version/version.go)"; \
    contract_version="$(tr -d ' \n\r' < internal/version/contract.version)"; \
    test -n "$version"; \
    test -n "$contract_version"; \
    case "$TARGETARCH" in \
      amd64) arch_env='GOAMD64=v1' ;; \
      arm64) arch_env='GOARM64=v8.0' ;; \
      *) echo "unsupported target architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    env GOWORK=off GOFLAGS='' GOEXPERIMENT='' GOFIPS140=off \
      CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" $arch_env \
      go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/Luxiaba/remnawave-node-lite-go/internal/version.Version=$version -X github.com/Luxiaba/remnawave-node-lite-go/internal/version.ContractVersion=$contract_version" \
      -o /out/remnanode-lite ./cmd/remnanode-lite; \
    env GOWORK=off GOFLAGS='' GOEXPERIMENT='' GOFIPS140=off CGO_ENABLED=0 \
      go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags='-s -w' -o /out/asn-builder ./cmd/asn-builder

FROM --platform=$BUILDPLATFORM ${DEBIAN_IMAGE} AS assets

ARG TARGETARCH
ARG XRAY_CORE_VERSION=v26.6.27
ARG XRAY_AMD64_SHA256=b3e5902d06d6282fe53cfa2fc426058b9aeaa429b2c812e20887cd47f26d08bf
ARG XRAY_ARM64_SHA256=13a251379bea366c2cf10363ad71e75734193d401f26f518bf0c25e5c8f8c931
ARG ASN_SOURCE_URL=https://github.com/ipverse/as-ip-blocks/archive/56d021c7536afb15317155e45b57e7b5c87a4700.tar.gz
ARG ASN_SOURCE_SHA256=fc8be15bfbef3134f603276a26364935dbd2543d099dbaafa978a33b674a58ec

COPY --from=build /out/asn-builder /usr/local/bin/asn-builder

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl unzip; \
    rm -rf /var/lib/apt/lists/*; \
    case "$TARGETARCH" in \
      amd64) xray_asset='Xray-linux-64.zip'; xray_sha="$XRAY_AMD64_SHA256" ;; \
      arm64) xray_asset='Xray-linux-arm64-v8a.zip'; xray_sha="$XRAY_ARM64_SHA256" ;; \
      *) echo "unsupported target architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl --fail --location --silent --show-error \
      --proto '=https' --tlsv1.2 --retry 3 --retry-all-errors \
      "https://github.com/XTLS/Xray-core/releases/download/${XRAY_CORE_VERSION}/${xray_asset}" \
      -o /tmp/xray.zip; \
    printf '%s  %s\n' "$xray_sha" /tmp/xray.zip | sha256sum --check --strict; \
    install -d /assets/lib /assets/xray /assets/asn; \
    unzip -p /tmp/xray.zip xray > /assets/lib/rw-core; \
    unzip -p /tmp/xray.zip geoip.dat > /assets/xray/geoip.dat; \
    unzip -p /tmp/xray.zip geosite.dat > /assets/xray/geosite.dat; \
    chmod 0755 /assets/lib/rw-core; \
    chmod 0644 /assets/xray/geoip.dat /assets/xray/geosite.dat; \
    curl --fail --location --silent --show-error \
      --proto '=https' --tlsv1.2 --retry 3 --retry-all-errors \
      "$ASN_SOURCE_URL" -o /tmp/as-ip-blocks.tar.gz; \
    printf '%s  %s\n' "$ASN_SOURCE_SHA256" /tmp/as-ip-blocks.tar.gz | sha256sum --check --strict; \
    asn-builder -format ipverse-tar-gz \
      -in /tmp/as-ip-blocks.tar.gz -out /assets/asn/asn-prefixes.bin; \
    chmod 0644 /assets/asn/asn-prefixes.bin; \
    rm -f /tmp/xray.zip /tmp/as-ip-blocks.tar.gz /usr/local/bin/asn-builder

FROM ${DEBIAN_IMAGE} AS runtime

LABEL org.opencontainers.image.title="Remnawave Node Lite (Go)" \
      org.opencontainers.image.description="Low-memory Remnawave Node 2.8.0 compatible implementation" \
      org.opencontainers.image.source="https://github.com/Luxiaba/remnawave-node-lite-go" \
      org.opencontainers.image.licenses="AGPL-3.0-only"

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates nftables; \
    rm -rf /var/lib/apt/lists/*; \
    install -d -m 0755 \
      /usr/local/lib/remnanode \
      /usr/local/share/remnanode/xray \
      /usr/local/share/remnanode/asn \
      /run/remnanode \
      /var/log/remnanode

COPY --from=build --chmod=0755 /out/remnanode-lite /usr/local/bin/remnanode-lite
COPY --from=assets --chmod=0755 /assets/lib/rw-core /usr/local/lib/remnanode/rw-core
COPY --from=assets --chmod=0644 /assets/xray/geoip.dat /usr/local/share/remnanode/xray/geoip.dat
COPY --from=assets --chmod=0644 /assets/xray/geosite.dat /usr/local/share/remnanode/xray/geosite.dat
COPY --from=assets --chmod=0644 /assets/asn/asn-prefixes.bin /usr/local/share/remnanode/asn/asn-prefixes.bin

ENV XRAY_CORE_VERSION=v26.6.27 \
    XRAY_BIN=/usr/local/lib/remnanode/rw-core \
    GEO_DIR=/usr/local/share/remnanode/xray \
    ASN_DB_PATH=/usr/local/share/remnanode/asn/asn-prefixes.bin \
    LOG_DIR=/var/log/remnanode \
    INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock

EXPOSE 2222
STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/remnanode-lite"]
