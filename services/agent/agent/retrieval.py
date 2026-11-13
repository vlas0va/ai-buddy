"""База знаний: загрузка, фильтр ACL ДО ранжирования, BM25 (+ необязательные эмбеддинги, RRF) и извлечение фрагментов
(ADR-008).
"""

from __future__ import annotations

import math
import re
from collections import Counter
from collections.abc import Callable, Iterable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

from .text import stems

TEST_BADGE = "Тестовая инструкция прототипа"
_INJECTION_LINE = re.compile(
    r"(игнорируй|ignore|забудь|disregard)\s+(все\s+|all\s+|предыдущ\w*\s+|previous\s+)*(инструкц|instruction|правил)",
    re.IGNORECASE,
)


@dataclass
class Doc:
    doc_id: str
    title: str
    body: str
    roles: list[str]
    teams: list[str]
    source_type: str = "test"
    checklist: list[str] = field(default_factory=list)
    tags: list[str] = field(default_factory=list)
    url: str = ""
    meta: dict[str, Any] = field(default_factory=dict)

    def readable_by(self, role: str, team_id: str) -> bool:
        role = "hrbp" if role == "hr_admin" else role  # = kb.aclRole у B: HR-администратор читает документы HR BP
        return role in self.roles and ("*" in self.teams or team_id in self.teams)

    @property
    def team(self) -> str:
        return "*" if "*" in self.teams else (self.teams[0] if self.teams else "*")

    def blocks(self) -> list[str]:
        """Блоки по абзацам: строка-заголовок сливается со следующим за ней блоком."""
        raw = [b.strip() for b in re.split(r"\n\s*\n", self.body) if b.strip()]
        merged: list[str] = []
        carry = ""
        for b in raw:
            if b.startswith("#") and "\n" not in b:
                carry = b.lstrip("# ").strip()
                if carry.lower() == self.title.lower():
                    carry = ""
                continue
            b = re.sub(r"(?m)^#{1,6}\s+(.+)$", r"\1:", b)
            if b.lower().startswith(self.title.lower() + ":\n"):
                b = b[len(self.title) + 2 :]
            merged.append(f"{carry}:\n{b}" if carry else b)
            carry = ""
        return merged


@dataclass
class Hit:
    doc: Doc
    score: float
    snippet: str

    def source(self) -> dict[str, Any]:
        return {
            "doc_id": self.doc.doc_id,
            "title": self.doc.title,
            "source_type": self.doc.source_type,
            "url": self.doc.url,
            "badge": TEST_BADGE,
        }


def parse_doc(text: str, fallback_id: str = "") -> Doc:
    meta: dict[str, Any] = {}
    body = text
    if text.startswith("---"):
        _, fm, body = text.split("---", 2)
        meta = yaml.safe_load(fm) or {}
    acl = meta.get("acl") or {}
    doc_id = str(meta.get("doc_id") or fallback_id)
    return Doc(
        doc_id=doc_id,
        title=str(meta.get("title") or doc_id),
        body=body.strip(),
        roles=[str(r) for r in acl.get("roles", [])],
        teams=[str(t) for t in acl.get("teams", [])],
        source_type=str(meta.get("source_type", "test")),
        checklist=[str(c) for c in meta.get("checklist", []) or []],
        tags=[str(t) for t in meta.get("tags", []) or []],
        url=str(meta.get("url") or f"/kb/{doc_id}"),
        meta=meta,
    )


def load_docs(kb_dir: Path) -> list[Doc]:
    docs = [
        parse_doc(p.read_text(encoding="utf-8"), p.stem)
        for p in sorted(Path(kb_dir).glob("*.md"))
        if p.name.lower() != "readme.md"
    ]
    for d in docs:
        if d.source_type != "test":
            raise ValueError(f"{d.doc_id}: every KB document must have source_type: test (R69)")
        if not d.roles or not d.teams:
            raise ValueError(f"{d.doc_id}: frontmatter with acl.roles and acl.teams is required (R71)")
    return docs


def sanitize_snippet(text: str) -> str:
    """Убрать строки, похожие на инструкции модели (косвенная промпт-инъекция в документах)."""
    return "\n".join(line for line in text.splitlines() if not _INJECTION_LINE.search(line)).strip()


class BM25:
    def __init__(self, corpus: list[list[str]], k1: float = 1.4, b: float = 0.75) -> None:
        self.k1, self.b = k1, b
        self.tf = [Counter(doc) for doc in corpus]
        self.len = [len(doc) for doc in corpus]
        self.avg = (sum(self.len) / len(self.len)) if corpus else 0.0
        df: Counter[str] = Counter()
        for doc in corpus:
            df.update(set(doc))
        n = len(corpus)
        self.idf = {t: math.log(1 + (n - f + 0.5) / (f + 0.5)) for t, f in df.items()}

    def score(self, query: list[str], idx: int) -> float:
        tf, dl = self.tf[idx], self.len[idx]
        s = 0.0
        for t in query:
            f = tf.get(t)
            if not f:
                continue
            s += self.idf.get(t, 0.0) * f * (self.k1 + 1) / (f + self.k1 * (1 - self.b + self.b * dl / (self.avg or 1)))
        return s


Embedder = Callable[[list[str]], list[list[float]]]


def _cosine(a: list[float], b: list[float]) -> float:
    num = sum(x * y for x, y in zip(a, b, strict=False))
    den = math.sqrt(sum(x * x for x in a)) * math.sqrt(sum(y * y for y in b))
    return num / den if den else 0.0


class KnowledgeBase:
    """Индекс базы знаний в процессе. В compose mcp-kb делает тот же поиск через полнотекстовый поиск PostgreSQL; этот
    класс обслуживает мок `kb.search` в процессе и перепроверку ACL на стороне агента.
    """

    def __init__(self, docs: Iterable[Doc], embedder: Embedder | None = None) -> None:
        self.docs = list(docs)
        self.by_id = {d.doc_id: d for d in self.docs}
        # Заголовок и теги весят x3 — они описывают, о чём документ.
        self._tokens = [
            stems(f"{d.title} {d.title} {d.title} {(' '.join(d.tags) + ' ') * 2} {d.body}") for d in self.docs
        ]
        self._bm25 = BM25(self._tokens)
        self._embedder = embedder
        self._vectors: list[list[float]] | None = None
        if embedder is not None:
            try:
                self._vectors = embedder([f"{d.title}\n{d.body[:1500]}" for d in self.docs])
            except Exception:  # noqa: BLE001 - embeddings are optional; BM25 still works
                self._vectors = None

    @classmethod
    def from_dir(cls, kb_dir: Path, embedder: Embedder | None = None) -> KnowledgeBase:
        return cls(load_docs(kb_dir), embedder)

    @property
    def hybrid(self) -> bool:
        return self._vectors is not None

    def coverage(self, query: str, doc_id: str) -> float:
        """Взвешенная по IDF доля слов запроса, которые есть в документе: защищает от ответов, совпадающих только по общим
        словам («работа», «команда»). Неизвестные слова получают максимальный IDF.
        """
        idx = next((i for i, d in enumerate(self.docs) if d.doc_id == doc_id), None)
        q = set(stems(query))
        if idx is None or not q:
            return 1.0
        max_idf = math.log(1 + (len(self.docs) + 0.5) / 0.5)
        total = sum(self._bm25.idf.get(t, max_idf) for t in q)
        present = sum(self._bm25.idf.get(t, max_idf) for t in q if self._bm25.tf[idx].get(t))
        return present / total if total else 0.0

    def get(self, doc_id: str, role: str, team_id: str) -> Doc | None:
        doc = self.by_id.get(doc_id)
        return doc if doc and doc.readable_by(role, team_id) else None

    def search(
        self,
        query: str,
        role: str,
        team_id: str,
        k: int = 4,
        boost_checklist: Iterable[str] = (),
        team_boost: float = 1.25,
        stage_boost: float = 1.15,
    ) -> list[Hit]:
        q = stems(query)
        allowed = [i for i, d in enumerate(self.docs) if d.readable_by(role, team_id)]  # ACL ДО ранжирования
        if not q or not allowed:
            return []
        boost = set(boost_checklist)
        scored: list[tuple[float, int]] = []
        for i in allowed:
            s = self._bm25.score(q, i)
            if s <= 0:
                continue
            d = self.docs[i]
            if team_id in d.teams:
                s *= team_boost
            if boost and boost.intersection(d.checklist):
                s *= stage_boost
            scored.append((s, i))
        scored.sort(reverse=True)
        if self._vectors is not None and self._embedder is not None:
            scored = self._fuse(query, scored, allowed)
        return [Hit(self.docs[i], round(s, 3), self.snippet(self.docs[i], q)) for s, i in scored[:k]]

    def _fuse(self, query: str, bm25: list[tuple[float, int]], allowed: list[int]) -> list[tuple[float, int]]:
        try:
            qv = self._embedder([query])[0]  # type: ignore[misc]
        except Exception:  # noqa: BLE001
            return bm25
        vec = sorted(((_cosine(qv, self._vectors[i]), i) for i in allowed), reverse=True)  # type: ignore[index]
        rank: dict[int, float] = {}
        best: dict[int, float] = {i: s for s, i in bm25}
        for r, (_, i) in enumerate(bm25):
            rank[i] = rank.get(i, 0.0) + 1.0 / (60 + r)
        for r, (cos, i) in enumerate(vec[:10]):
            if cos > 0.3:
                rank[i] = rank.get(i, 0.0) + 1.0 / (60 + r)
                best.setdefault(i, cos * 3)
        return sorted(((best[i] * (1 + rank[i]), i) for i in rank), reverse=True)

    @staticmethod
    def snippet(doc: Doc, q: list[str], max_blocks: int = 2) -> str:
        qs = set(q)
        blocks = doc.blocks()
        if not blocks:
            return ""
        scored = sorted(((len(qs.intersection(stems(b))), -n, b) for n, b in enumerate(blocks)), reverse=True)
        top = [b for score, _, b in scored[:max_blocks] if score > 0] or [blocks[0]]
        if len(top) == 1 and len(top[0]) < 160:
            # короткая заметка («все цифры — тестовые») сама по себе ни на что не отвечает: добавить блок после неё
            # (таблицу, список)
            i = blocks.index(top[0])
            top += blocks[i + 1 : i + 2]
        ordered = [b for b in blocks if b in top]
        return sanitize_snippet("\n\n".join(ordered))[:1200]
