#!/usr/bin/env python3
"""Extract exact method/path inventory from an upstream tag or Go router."""

from __future__ import annotations

import importlib.util
import re
import sys
from pathlib import Path

sys.dont_write_bytecode = True


def official(upstream: Path) -> list[str]:
    helper = Path(__file__).with_name("extract-contract-routes.py")
    spec = importlib.util.spec_from_file_location("route_extractor", helper)
    if spec is None or spec.loader is None:
        raise ValueError("cannot load route extractor")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    symbols = module.controller_symbols(upstream / "libs/contract/api/controllers")
    inventory: set[str] = set()
    for controller in (upstream / "src/modules").rglob("*.controller.ts"):
        source = controller.read_text(encoding="utf-8")
        match = re.search(r"@Controller\(([A-Z_]+_CONTROLLER)\)", source)
        if not match or match.group(1) not in symbols:
            continue
        prefix = symbols[match.group(1)]
        for method, route in re.findall(r"@(Get|Post|Put|Patch|Delete)\(([A-Z_]+_ROUTES\.[A-Z_.]+)\)", source):
            inventory.add(f"{method.upper()} /node/{prefix}/{symbols[route]}")
    routes = set(module.extract_routes(upstream))
    if {line.split(" ", 1)[1] for line in inventory} != routes:
        raise ValueError("controller methods differ from libs/contract/api/routes.ts")
    return sorted(inventory)


def local(repo: Path) -> list[str]:
    source = (repo / "internal/httpserver/server.go").read_text(encoding="utf-8")
    inventory = re.findall(
        r'case r\.Method == http\.Method(Get|Post|Put|Patch|Delete) && path == "(/node/[^"]+)":',
        source,
    )
    return sorted(f"{method.upper()} {path}" for method, path in inventory)


def main() -> int:
    if len(sys.argv) != 3 or sys.argv[1] not in ("official", "local"):
        print(f"usage: {sys.argv[0]} official UPSTREAM_REPO | local LITE_GO_REPO", file=sys.stderr)
        return 2
    try:
        inventory = official(Path(sys.argv[2])) if sys.argv[1] == "official" else local(Path(sys.argv[2]))
    except (OSError, KeyError, ValueError) as error:
        print(f"extract methods: {error}", file=sys.stderr)
        return 1
    for line in inventory:
        print(line)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
