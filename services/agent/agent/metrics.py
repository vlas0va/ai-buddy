"""Tiny Prometheus-text metrics registry (no external dependency). Scraped at GET /metrics."""

from __future__ import annotations

import threading
from collections import defaultdict

BUCKETS = (0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0)


class Metrics:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self.counters: dict[tuple[str, tuple[tuple[str, str], ...]], float] = defaultdict(float)
        self.hist: dict[str, list[int]] = defaultdict(lambda: [0] * (len(BUCKETS) + 1))
        self.hist_sum: dict[str, float] = defaultdict(float)

    def inc(self, name: str, value: float = 1.0, **labels: str) -> None:
        with self._lock:
            self.counters[(name, tuple(sorted(labels.items())))] += value

    def observe(self, name: str, seconds: float) -> None:
        with self._lock:
            h = self.hist[name]
            for i, b in enumerate(BUCKETS):
                if seconds <= b:
                    h[i] += 1
            h[-1] += 1
            self.hist_sum[name] += seconds

    def render(self) -> str:
        lines: list[str] = []
        with self._lock:
            for (name, labels), value in sorted(self.counters.items()):
                lbl = ",".join(f'{k}="{v}"' for k, v in labels)
                lines.append(f"{name}{{{lbl}}} {value}" if lbl else f"{name} {value}")
            for name, h in sorted(self.hist.items()):
                for i, b in enumerate(BUCKETS):
                    lines.append(f'{name}_bucket{{le="{b}"}} {h[i]}')
                lines.append(f'{name}_bucket{{le="+Inf"}} {h[-1]}')
                lines.append(f"{name}_count {h[-1]}")
                lines.append(f"{name}_sum {round(self.hist_sum[name], 6)}")
        return "\n".join(lines) + "\n"
