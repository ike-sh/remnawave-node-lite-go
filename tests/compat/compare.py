#!/usr/bin/env python3
"""Black-box HTTP/TLS comparison against the pinned official Node 3.4.1 image.

Use disposable certificates from fixturegen. This script never writes key or JWT
material into its result JSON; only symbolic Authorization labels are recorded.
"""

from __future__ import annotations

import argparse
import http.client
import json
import re
import ssl
from pathlib import Path
from typing import Any


UUID = "00000000-0000-4000-8000-000000000001"
ISO_TIME = re.compile(r"\b\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z\b")


def cases() -> list[dict[str, str | None]]:
    sync = lambda config: json.dumps({"plugin": {"uuid": UUID, "name": "compat", "config": config}}, separators=(",", ":"))
    return [
        {"name": "valid-health", "method": "GET", "path": "/node/xray/healthcheck", "body": None},
        {"name": "malformed-json-open", "method": "POST", "path": "/node/stats/get-users-stats", "body": "{"},
        {"name": "malformed-json-value", "method": "POST", "path": "/node/stats/get-users-stats", "body": '{"foo":'},
        {"name": "malformed-json-without-jwt", "method": "POST", "path": "/node/stats/get-users-stats", "body": "{", "auth": "missing"},
        {"name": "empty-body", "method": "POST", "path": "/node/stats/get-users-stats", "body": ""},
        {"name": "wrong-type-string", "method": "POST", "path": "/node/stats/get-users-stats", "body": '{"reset":"invalid"}'},
        {"name": "wrong-type-null", "method": "POST", "path": "/node/stats/get-users-stats", "body": '{"reset":null}'},
        {"name": "missing-required", "method": "POST", "path": "/node/stats/get-users-stats", "body": "{}"},
        {"name": "extra-property", "method": "POST", "path": "/node/stats/get-user-online-status", "body": '{"username":"nobody","extra":"ignored"}'},
        {"name": "bad-uuid-malformed", "method": "POST", "path": "/node/handler/remove-user", "body": '{"username":"u","hashData":{"vlessUuid":"bad"}}'},
        {"name": "bad-uuid-empty", "method": "POST", "path": "/node/handler/remove-user", "body": '{"username":"u","hashData":{"vlessUuid":""}}'},
        {"name": "bad-uuid-null", "method": "POST", "path": "/node/handler/remove-user", "body": '{"username":"u","hashData":{"vlessUuid":null}}'},
        {"name": "invalid-user-enum", "method": "POST", "path": "/node/handler/add-user", "body": json.dumps({"data": [{"type": "unknown", "tag": "x", "username": "u"}], "hashData": {"vlessUuid": UUID}})},
        {"name": "invalid-flow-enum", "method": "POST", "path": "/node/handler/add-user", "body": json.dumps({"data": [{"type": "vless", "tag": "x", "username": "u", "uuid": UUID, "flow": "bad"}], "hashData": {"vlessUuid": UUID}})},
        {"name": "invalid-cipher-enum", "method": "POST", "path": "/node/handler/add-user", "body": json.dumps({"data": [{"type": "shadowsocks", "tag": "x", "username": "u", "password": "p", "cipherType": 99, "ivCheck": False}], "hashData": {"vlessUuid": UUID}})},
        {"name": "nested-add-users-missing-data", "method": "POST", "path": "/node/handler/add-users", "body": '{"affectedInboundTags":[],"users":[{"inboundData":[]}]}'},
        {"name": "nested-remove-users-bad-uuid", "method": "POST", "path": "/node/handler/remove-users", "body": '{"users":[{"userId":"u","hashUuid":"bad"}]}'},
        {"name": "xray-start-invalid-hash-entry", "method": "POST", "path": "/node/xray/start", "body": '{"internals":{"hashes":{"emptyConfig":"x","inbounds":[{"usersCount":"bad","hash":"h","tag":"t"}]}},"xrayConfig":{}}'},
        {"name": "invalid-ipv4", "method": "POST", "path": "/node/plugin/nftables/block-ips", "body": '{"ips":[{"ip":"999.0.0.1","timeout":10}]}'},
        {"name": "invalid-ipv6", "method": "POST", "path": "/node/plugin/nftables/block-ips", "body": '{"ips":[{"ip":"2001:db8::zz","timeout":10}]}'},
        {"name": "invalid-cidr", "method": "POST", "path": "/node/plugin/nftables/block-ips", "body": '{"ips":[{"ip":"192.0.2.0/24","timeout":10}]}'},
        {"name": "plugin-missing-required", "method": "POST", "path": "/node/plugin/sync", "body": '{"plugin":{"uuid":"' + UUID + '","name":"compat"}}'},
        {"name": "plugin-invalid-uuid", "method": "POST", "path": "/node/plugin/sync", "body": '{"plugin":{"uuid":"bad","name":"compat","config":{}}}'},
        {"name": "plugin-wrong-array", "method": "POST", "path": "/node/plugin/sync", "body": sync({"sharedLists": "invalid"})},
        {"name": "plugin-invalid-enum", "method": "POST", "path": "/node/plugin/sync", "body": sync({"sharedLists": [{"name": "ext:test", "type": "unknown", "items": []}]})},
        {"name": "plugin-invalid-ip", "method": "POST", "path": "/node/plugin/sync", "body": sync({"sharedLists": [{"name": "ext:test", "type": "ipList", "items": ["999.0.0.1"]}]})},
        {"name": "plugin-invalid-cidr", "method": "POST", "path": "/node/plugin/sync", "body": sync({"sharedLists": [{"name": "ext:test", "type": "ipList", "items": ["192.0.2.0/99"]}]})},
        {"name": "plugin-invalid-port", "method": "POST", "path": "/node/plugin/sync", "body": sync({"egressFilter": {"enabled": True, "blockedIps": [], "blockedPorts": [70000]}})},
        {"name": "jwt-missing", "method": "GET", "path": "/node/xray/healthcheck", "body": None, "auth": "missing"},
        {"name": "jwt-random", "method": "GET", "path": "/node/xray/healthcheck", "body": None, "auth": "random"},
        {"name": "jwt-malformed", "method": "GET", "path": "/node/xray/healthcheck", "body": None, "auth": "malformed"},
        {"name": "jwt-bad-signature", "method": "GET", "path": "/node/xray/healthcheck", "body": None, "auth": "badSignature"},
        {"name": "jwt-expired", "method": "GET", "path": "/node/xray/healthcheck", "body": None, "auth": "expired"},
        {"name": "jwt-wrong-claims", "method": "GET", "path": "/node/xray/healthcheck", "body": None, "auth": "wrongClaims"},
        {"name": "unknown-get", "method": "GET", "path": "/node/this-does-not-exist", "body": None},
        {"name": "unknown-post", "method": "POST", "path": "/node/this-does-not-exist", "body": "{}"},
        {"name": "wrong-method-stop", "method": "POST", "path": "/node/xray/stop", "body": "{}"},
        {"name": "internal-stats-failure", "method": "GET", "path": "/node/stats/get-system-stats", "body": None},
        {"name": "nonexistent-inbound", "method": "POST", "path": "/node/stats/get-inbound-stats", "body": '{"tag":"nonexistent","reset":false}'},
        {"name": "nft-unavailable", "method": "POST", "path": "/node/plugin/nftables/recreate-tables", "body": ""},
    ]


def shape(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: shape(item) for key, item in sorted(value.items())}
    if isinstance(value, list):
        return {"type": "array", "items": [shape(item) for item in value[:2]]}
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, (int, float)):
        return "number"
    if isinstance(value, str) and ISO_TIME.fullmatch(value):
        return "iso8601"
    return "string"


def request(port: int, fixture_dir: Path, tokens: dict[str, str], case: dict[str, str | None]) -> dict[str, Any]:
    context = ssl.create_default_context(cafile=str(fixture_dir / "ca.pem"))
    context.minimum_version = ssl.TLSVersion.TLSv1_3
    context.load_cert_chain(str(fixture_dir / "client.pem"), str(fixture_dir / "client.key"))
    conn = http.client.HTTPSConnection("localhost", port, context=context, timeout=8)
    auth = case.get("auth") or "valid"
    headers = {"Content-Type": "application/json"}
    if auth != "missing":
        token = tokens.get(auth, "random" if auth == "random" else "not.a.jwt")
        headers["Authorization"] = "Bearer " + token
    body = case["body"]
    if body is not None:
        headers["Content-Length"] = str(len(body.encode()))
    result: dict[str, Any] = {
        "method": case["method"], "path": case["path"],
        "requestHeaders": {key: ("Bearer <" + auth + ">" if key == "Authorization" else value) for key, value in headers.items()},
        "requestBody": body,
    }
    try:
        conn.request(case["method"], case["path"], body=body, headers=headers)
        response = conn.getresponse()
        raw = response.read()
        result.update({"connection": "response", "status": response.status,
                       "responseHeaders": {key.lower(): value for key, value in response.getheaders()},
                       "rawBody": raw.decode("utf-8", "replace")})
        try:
            parsed = json.loads(raw)
            result["jsonBody"] = parsed
            result["bodyShape"] = shape(parsed)
        except (ValueError, UnicodeDecodeError):
            result["jsonBody"] = None
            result["bodyShape"] = None
    except (OSError, ssl.SSLError, http.client.HTTPException) as error:
        result.update({"connection": "closed" if isinstance(error, (ConnectionResetError, ConnectionAbortedError, http.client.RemoteDisconnected, ssl.SSLEOFError)) else "error",
                       "exception": type(error).__name__, "exceptionText": str(error)[:200]})
    finally:
        conn.close()
    return result


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixture-dir", type=Path, required=True)
    parser.add_argument("--official-port", type=int, default=12441)
    parser.add_argument("--go-port", type=int, default=12442)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--official-fixture", type=Path, help="write sanitized observed official results")
    parser.add_argument("--only", help="regular expression selecting cases for a focused probe")
    args = parser.parse_args()
    tokens = json.loads((args.fixture_dir / "tokens.json").read_text(encoding="utf-8"))
    report = []
    for case in cases():
        if args.only and not re.search(args.only, case["name"]):
            continue
        official = request(args.official_port, args.fixture_dir, tokens, case)
        go = request(args.go_port, args.fixture_dir, tokens, case)
        report.append({"case": case["name"], "official": official, "go": go})
        print(f"{case['name']}: official={official['connection']}/{official.get('status')} go={go['connection']}/{go.get('status')}")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    if args.official_fixture:
        fixture = {
            "source": {"tag": "3.4.1", "commit": "44912631321664dbd5822e9bf8d96766ccff7c93",
                       "image": "ghcr.io/remnawave/node:3.4.1",
                       "digest": "sha256:0cdf386dd49f360fc885bb34bde21132e478e40f0deac62d616086ec0fa9257e"},
            "cases": [],
        }
        for row in report:
            observed = row["official"]
            headers = dict(observed.get("responseHeaders", {}))
            for key in ("date", "etag"):
                if key in headers:
                    headers[key] = "<dynamic>"
            fixture["cases"].append({
                "case": row["case"], "method": observed["method"], "path": observed["path"],
                "requestHeaders": observed["requestHeaders"], "requestBody": observed["requestBody"],
                "connection": observed["connection"], "status": observed.get("status"),
                "responseHeaders": headers,
                "rawBodyTemplate": ISO_TIME.sub("<ISO8601>", observed.get("rawBody", "")),
                "bodyShape": observed.get("bodyShape"),
            })
        args.official_fixture.parent.mkdir(parents=True, exist_ok=True)
        args.official_fixture.write_text(json.dumps(fixture, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"wrote {len(report)} differential cases to {args.output}")


if __name__ == "__main__":
    main()
