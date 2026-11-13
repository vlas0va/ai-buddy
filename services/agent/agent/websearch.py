"""Веб-поиск по общим техническим вопросам, которых нет в базе знаний (git, Docker, SQL, Python …).

Провайдеры (`web_search` в agent.yaml): `off`; `curated` (по умолчанию, без сети: короткий список страниц публичной
документации в fixtures/web_curated.yaml); `searxng` (живой поиск в интернете через экземпляр SearXNG,
`WEB_SEARCH_URL`). Из агента уходит только замаскированный вопрос: секреты удалены, ничего о пользователе не
отправляется. Результаты — внешние источники (`source_type: external`), никогда не выдаются за правила Авито.
Спецификация: openspec/specs/attachments-websearch (WS-xx).
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import httpx
import yaml

from .text import normalize, stems

log = logging.getLogger("agent")

EXTERNAL_BADGE = "Внешний источник"
TECH = re.compile(
    r"\b(git|гит\w*|docker\w*|докер\w*|kubernetes|k8s|кубер\w*|python|питон\w*|golang|go\b|java\b|kotlin|котлин"
    r"|typescript|javascript|\bjs\b|\bts\b|react|реакт\w*|node|sql|postgres\w*|постгрес\w*|redis|kafka|кафк\w*|linux|линукс"
    r"|bash|shell|терминал\w*|консол\w*|regex|регуляр\w*|http|rest|grpc|json|yaml|nginx|ssh|curl|vim|pytest|unit|юнит"
    r"|тест\w*|рефактор\w*|алгоритм\w*|рекурси\w*|массив\w*|хеш\w*|индекс\w*|транзакци\w*|ооп|solid|паттерн\w*|многопоточ\w*"
    r"|асинхрон\w*|корутин\w*|горутин\w*|интерфейс\w*|класс\w*|функци\w*|исключени\w*|merge|rebase|commit|коммит\w*|ребейз\w*"
    r"|ветк\w*|stash|cherry|pip|npm|venv|ide|vscode|goland|pycharm)",
    re.IGNORECASE,
)
COMPANY = re.compile(
    r"авито|наш\w*|команд[аеуы]\b|у\s+нас|наставник|руководител|отгул|отпуск|доступ|стажировк|офис|бенефит|зарплат|hr\b"
)


@dataclass
class WebResult:
    title: str
    url: str
    snippet: str

    def source(self) -> dict[str, Any]:
        return {
            "doc_id": "web:" + self.url,
            "title": self.title,
            "source_type": "external",
            "url": self.url,
            "badge": EXTERNAL_BADGE,
        }


def is_general_tech(question: str) -> bool:
    """Общий инженерный вопрос (не про Авито, команду или HR), на который может ответить публичная документация."""
    t = normalize(question)
    return bool(TECH.search(t)) and not COMPANY.search(t)


class WebSearch:
    def __init__(self, cfg: dict[str, Any], base_dir: Path, client: httpx.Client | None = None) -> None:
        self.provider = str(cfg.get("provider") or "off").strip().lower()
        self.url = str(cfg.get("url") or "").rstrip("/")
        self.timeout = float(cfg.get("timeout_s", 4))
        self.max_results = int(cfg.get("max_results", 3))
        self.client = client
        self.curated: list[dict[str, Any]] = []
        if self.provider == "curated":
            path = base_dir / str(cfg.get("curated_path") or "fixtures/web_curated.yaml")
            if path.exists():
                self.curated = yaml.safe_load(path.read_text(encoding="utf-8")) or []
        if self.provider == "searxng" and not self.url:
            log.warning("WEB_SEARCH_PROVIDER=searxng without WEB_SEARCH_URL: web search is off")
            self.provider = "off"

    @property
    def enabled(self) -> bool:
        return self.provider in ("curated", "searxng")

    def search(self, query: str) -> list[WebResult]:
        if self.provider == "curated":
            return self._curated(query)
        if self.provider == "searxng":
            return self._searxng(query)
        return []

    def _curated(self, query: str) -> list[WebResult]:
        q = set(stems(normalize(query)))
        scored = []
        for item in self.curated:
            keys = set(stems(normalize(" ".join(item.get("keywords", [])) + " " + item.get("title", ""))))
            score = len(q & keys)
            if score:
                scored.append((score, item))
        scored.sort(key=lambda p: -p[0])
        best = scored[0][0] if scored else 0
        return [
            WebResult(i["title"], i["url"], i.get("snippet", ""))
            for s, i in scored[: self.max_results]
            if s >= max(1, best // 2)
        ]

    def _searxng(self, query: str) -> list[WebResult]:
        client = self.client or httpx.Client(timeout=self.timeout)
        try:
            resp = client.get(
                f"{self.url}/search", params={"q": query, "format": "json", "language": "ru"}, timeout=self.timeout
            )
            resp.raise_for_status()
            data = resp.json()
        except (httpx.HTTPError, ValueError) as exc:
            log.warning("web search failed: %s", exc)
            return []
        finally:
            if self.client is None:
                client.close()
        out = []
        for r in data.get("results", []):
            url = str(r.get("url") or "")
            if not url.startswith(("https://", "http://")):
                continue
            out.append(WebResult(str(r.get("title") or url)[:200], url, str(r.get("content") or "")[:400]))
            if len(out) >= self.max_results:
                break
        return out
