#!/usr/bin/env python3
"""Проверка контрактов: каждая схема инструмента MCP — корректная JSON Schema 2020-12, а openapi.yaml разбирается.

Запуск: python3 scripts/check_contracts.py   (нужно: pip install pyyaml jsonschema)
"""
import json
import pathlib
import sys

import yaml
from jsonschema import Draft202012Validator

ROOT = pathlib.Path(__file__).resolve().parent.parent
errors = 0
count = 0
for path in sorted((ROOT / "contracts" / "schemas").glob("*.json")):
    data = json.loads(path.read_text(encoding="utf-8"))
    for tool in data["tools"]:
        for key in ("inputSchema", "outputSchema"):
            try:
                Draft202012Validator.check_schema(tool[key])
            except Exception as exc:  # noqa: BLE001 - report every broken schema
                errors += 1
                print(f"{path.name}:{tool['name']}.{key}: {exc}")
        if tool["write"] and not {"idempotency_key", "confirmation_token"} <= set(tool["inputSchema"].get("required", [])):
            errors += 1
            print(f"{path.name}:{tool['name']}: write tool must require idempotency_key and confirmation_token")
        count += 1
spec = yaml.safe_load((ROOT / "contracts" / "openapi.yaml").read_text(encoding="utf-8"))
print(f"{count} tool schemas, {len(spec['paths'])} API paths checked, {errors} errors")
sys.exit(1 if errors else 0)
