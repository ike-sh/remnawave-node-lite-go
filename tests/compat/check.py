#!/usr/bin/env python3
"""Check wire-significant fields in a black-box differential report.

The official fixture preserves raw bodies and full header observations. This
checker intentionally ignores framework wording and Zod issue internals that
the pinned Panel backend does not read.
"""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


ISO_TIME = re.compile(r"\b\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z\b")


def media_type(headers: dict[str, str]) -> str:
    return headers.get("content-type", "").split(";", 1)[0].strip().lower()


def check(report: list[dict], fixture: dict) -> tuple[list[str], list[str]]:
    expected = {item["case"]: item for item in fixture["cases"]}
    failures: list[str] = []
    incidental: list[str] = []
    for row in report:
        name = row["case"]
        baseline = expected.get(name)
        if baseline is None:
            failures.append(f"{name}: missing official fixture")
            continue
        official, go = row["official"], row["go"]
        if official.get("bodyShape") != baseline.get("bodyShape"):
            failures.append(f"{name}: official body shape drifted from fixture")
        if ISO_TIME.sub("<ISO8601>", official.get("rawBody", "")) != baseline.get("rawBodyTemplate"):
            failures.append(f"{name}: official raw body drifted from fixture")
        if media_type(official.get("responseHeaders", {})) != media_type(baseline.get("responseHeaders", {})):
            failures.append(f"{name}: official content type drifted from fixture")
        for field in ("connection", "status"):
            if official.get(field) != baseline.get(field):
                failures.append(f"{name}: official {field} drifted from fixture")
            if go.get(field) != baseline.get(field):
                failures.append(f"{name}: Go {field}={go.get(field)!r}, official={baseline.get(field)!r}")
        if media_type(official.get("responseHeaders", {})) != media_type(go.get("responseHeaders", {})):
            failures.append(f"{name}: JSON/non-JSON content type differs")
        if official.get("responseHeaders", {}).get("allow") != go.get("responseHeaders", {}).get("allow"):
            failures.append(f"{name}: Allow header differs")
        official_body, go_body = official.get("jsonBody"), go.get("jsonBody")
        if isinstance(official_body, dict):
            if not isinstance(go_body, dict):
                failures.append(f"{name}: Go did not return a JSON object")
                continue
            if set(official_body) != set(go_body):
                failures.append(f"{name}: top-level JSON keys differ")
                continue
            status = official.get("status")
            if status == 400:
                if official_body.get("statusCode") != go_body.get("statusCode"):
                    failures.append(f"{name}: statusCode differs")
                if "errors" in official_body:
                    if not isinstance(go_body.get("errors"), list) or not go_body["errors"]:
                        failures.append(f"{name}: validation errors missing")
                    if official_body.get("message") != go_body.get("message"):
                        failures.append(f"{name}: validation message differs")
                elif official_body.get("error") != go_body.get("error"):
                    failures.append(f"{name}: parser error category differs")
            elif status == 500:
                for field in ("errorCode", "message", "path"):
                    if official_body.get(field) != go_body.get(field):
                        failures.append(f"{name}: coded error {field} differs")
                if not isinstance(go_body.get("timestamp"), str) or not ISO_TIME.fullmatch(go_body["timestamp"]):
                    failures.append(f"{name}: timestamp is not ISO8601")
            elif status in (200, 201):
                official_response = official_body.get("response")
                go_response = go_body.get("response")
                if not isinstance(official_response, dict) or not isinstance(go_response, dict):
                    failures.append(f"{name}: response envelope differs")
                elif set(official_response) != set(go_response):
                    failures.append(f"{name}: response keys differ")
                else:
                    for field in official_response:
                        if field == "xrayVersion":
                            continue  # Test Go container has no Xray binary.
                        if official_response[field] != go_response[field]:
                            failures.append(f"{name}: response.{field} differs")
        if official.get("bodyShape") != go.get("bodyShape"):
            incidental.append(f"{name}: nested body shape differs")
        if ISO_TIME.sub("<ISO8601>", official.get("rawBody", "")) != ISO_TIME.sub("<ISO8601>", go.get("rawBody", "")):
            incidental.append(f"{name}: raw bytes differ")
    if set(expected) != {row["case"] for row in report}:
        failures.append("case inventory differs from pinned fixture")
    return failures, incidental


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("report", type=Path)
    parser.add_argument("--fixture", type=Path, default=Path(__file__).parent / "testdata/upstream-v3.4.1/errors.json")
    args = parser.parse_args()
    report = json.loads(args.report.read_text(encoding="utf-8"))
    fixture = json.loads(args.fixture.read_text(encoding="utf-8"))
    failures, incidental = check(report, fixture)
    print(f"cases={len(report)} wire_failures={len(failures)} incidental_differences={len(incidental)}")
    for failure in failures:
        print("FAIL", failure)
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
