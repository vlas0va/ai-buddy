"""Трассировка спецификаций в тесты (ADR-005): на каждый `#### Scenario: <ID> ...` в openspec/specs должен ссылаться тест.

python tools/spec_trace.py [--report ../../eval/reports/spec-trace.md]   → exit 1, если у сценария нет теста.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCENARIO = re.compile(r"^#### Scenario: ([A-Z0-9]+-\d+[a-z]?)\s+(.*)$", re.MULTILINE)


def collect() -> list[tuple[str, str, str, list[str]]]:
    tests = {p: p.read_text(encoding="utf-8") for p in (ROOT / "services" / "agent" / "tests").glob("test_*.py")}
    rows = []
    for spec in sorted((ROOT / "openspec" / "specs").glob("*/spec.md")):
        for sid, title in SCENARIO.findall(spec.read_text(encoding="utf-8")):
            keys = (sid, sid.replace("-", "_"))
            where = sorted(
                p.name
                for p, text in tests.items()
                if any(re.search(rf"\b{re.escape(k)}\b|{re.escape(k)}_", text) for k in keys)
            )
            rows.append((spec.parent.name, sid, title, where))
    return rows


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--report", default="")
    args = ap.parse_args(argv)
    rows = collect()
    missing = [r for r in rows if not r[3]]
    if args.report:
        lines = [
            "# OpenSpec scenario → test trace",
            "",
            f"Scenarios: {len(rows)} · traced: {len(rows) - len(missing)}",
            "",
            "| capability | scenario | test files |",
            "|---|---|---|",
        ]
        lines += [f"| {c} | {sid} {t} | {', '.join(w) or '**missing**'} |" for c, sid, t, w in rows]
        Path(args.report).write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"spec-trace: {len(rows) - len(missing)}/{len(rows)} scenarios have tests")
    for c, sid, t, _ in missing:
        print(f"  missing: {c} {sid} {t}")
    return 1 if missing else 0


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
