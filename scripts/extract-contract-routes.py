#!/usr/bin/env python3
"""Expand remnawave/node TypeScript route constants into concrete REST paths."""

from __future__ import annotations

import re
import sys
from pathlib import Path


SIMPLE_CONST = re.compile(r"export const ([A-Z0-9_]+) = '([^']+)'(?: as const)?;")
OBJECT_START = re.compile(r"export const ([A-Z0-9_]+) = \{")
NESTED_START = re.compile(r"^([A-Z0-9_]+):\s*\{$")
PROPERTY = re.compile(r"^([A-Z0-9_]+):\s*(.+?),?$")
INTERPOLATION = re.compile(r"\$\{([A-Z0-9_]+)\}")
CONTROLLER_INTERPOLATION = re.compile(r"\$\{CONTROLLERS\.([A-Z0-9_.]+)\}")


def expand_value(raw: str, simple: dict[str, str]) -> str:
    value = raw.rstrip(",").strip()
    if len(value) < 2 or value[0] not in "'`" or value[-1] != value[0]:
        raise ValueError(f"unsupported TypeScript constant value: {raw}")
    value = value[1:-1]

    def replace(match: re.Match[str]) -> str:
        name = match.group(1)
        if name not in simple:
            raise ValueError(f"unknown constant {name} in {raw}")
        return simple[name]

    return INTERPOLATION.sub(replace, value)


def controller_symbols(controller_dir: Path) -> dict[str, str]:
    files = sorted(controller_dir.glob("*.ts"))
    simple: dict[str, str] = {}
    for path in files:
        simple.update(SIMPLE_CONST.findall(path.read_text(encoding="utf-8")))

    symbols = dict(simple)
    for path in files:
        current: str | None = None
        stack: list[str] = []
        for source_line in path.read_text(encoding="utf-8").splitlines():
            line = source_line.strip()
            if current is None:
                match = OBJECT_START.match(line)
                if match:
                    current = match.group(1)
                continue
            if line.startswith("}"):
                if stack:
                    stack.pop()
                else:
                    current = None
                continue
            nested = NESTED_START.match(line.rstrip(","))
            if nested:
                stack.append(nested.group(1))
                continue
            prop = PROPERTY.match(line)
            if not prop:
                continue
            key, raw = prop.groups()
            symbol = ".".join([current, *stack, key])
            symbols[symbol] = expand_value(raw, simple)
    return symbols


def extract_routes(upstream: Path) -> list[str]:
    api_dir = upstream / "libs" / "contract" / "api"
    symbols = controller_symbols(api_dir / "controllers")
    routes_source = (api_dir / "routes.ts").read_text(encoding="utf-8")
    root_match = re.search(r"export const ROOT = '([^']+)'", routes_source)
    if not root_match:
        raise ValueError("ROOT route constant not found")
    root = root_match.group(1)

    expanded: set[str] = set()
    for template in re.findall(r"`(\$\{ROOT\}/[^`]+)`", routes_source):
        value = template.replace("${ROOT}", root)

        def replace_controller(match: re.Match[str]) -> str:
            symbol = match.group(1)
            if symbol not in symbols:
                raise ValueError(f"unknown controller symbol {symbol}")
            return symbols[symbol]

        value = CONTROLLER_INTERPOLATION.sub(replace_controller, value)
        if "${" in value:
            raise ValueError(f"route was not fully expanded: {value}")
        expanded.add(value)
    return sorted(expanded)


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {Path(sys.argv[0]).name} UPSTREAM_REPO", file=sys.stderr)
        return 2
    try:
        routes = extract_routes(Path(sys.argv[1]))
    except (OSError, ValueError) as error:
        print(f"extract routes: {error}", file=sys.stderr)
        return 1
    for route in routes:
        print(route)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
