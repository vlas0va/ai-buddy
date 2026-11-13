"""Файлы, приложенные к сообщению чата: логи, конфиги и код (текст, прочитанный в браузере), скриншоты и документы (только
метаданные). Спецификация: openspec/specs/attachments-websearch/spec.md (AT-xx).

Секреты в приложенном тексте маскируются раньше, чем его увидит что-либо ещё. Скриншоты в прототипе не распознаются —
buddy просит текст ошибки, а не гадает. Документы никогда не читаются: buddy говорит, куда их загрузить.
"""

from __future__ import annotations

import re
from collections.abc import Callable, Iterator
from typing import Any

from ..nlu.router import looks_like_log
from ..sse import Event, status
from . import absence
from .coach import _doc_hit, _source_event, handle_code_review, handle_troubleshoot, mask_secrets
from .common import Turn, say

LOG_EXT = re.compile(r"\.(log|out|err|trace)$", re.IGNORECASE)
CONFIG_EXT = re.compile(r"(\.(env|ya?ml|json|toml|ini|properties|conf|cfg)$|^\.env|^dockerfile$)", re.IGNORECASE)
CODE_EXT = re.compile(r"\.(py|go|ts|tsx|js|jsx|java|kt|sql|sh|rb|cs|cpp|c|h|swift|php)$", re.IGNORECASE)
MAX_TEXT = 16000  # символов приложенного текста, которые берутся в один ход


def classify(att: dict[str, Any]) -> str:
    """log | config | code | text | image | document."""
    kind = att.get("kind")
    if kind in ("image", "document"):
        return str(kind)
    name = str(att.get("name") or "").strip()
    body = str(att.get("text") or "")
    if LOG_EXT.search(name) or looks_like_log(body):
        return "log"
    if CONFIG_EXT.search(name):
        return "config"
    if CODE_EXT.search(name):
        return "code"
    return "text"


def describe(atts: list[dict[str, Any]]) -> str:
    return ", ".join(f"«{a.get('name', 'файл')}»" for a in atts)


def handle(
    turn: Turn, text: str, atts: list[dict[str, Any]], route_text: Callable[[str], Iterator[Event]]
) -> Iterator[Event]:
    """Ответить на сообщение с вложениями; `route_text` выполняет обычную маршрутизацию для (замаскированного) текста."""
    yield status("thinking", "Смотрю вложения")
    kinds = {i: classify(a) for i, a in enumerate(atts)}
    texts = {
        i: mask_secrets(str(a.get("text") or "")[:MAX_TEXT]) for i, a in enumerate(atts) if a.get("kind") == "text"
    }
    masked_total = sum(n for _, n in texts.values())
    turn.meta["attachments"] = [{"name": a.get("name"), "kind": kinds[i]} for i, a in enumerate(atts)]
    turn.meta["secrets_masked"] = masked_total
    by = lambda k: [i for i, v in kinds.items() if v == k]  # noqa: E731

    if by("log"):
        body = "\n".join(texts[i][0] for i in by("log"))
        yield from handle_troubleshoot(turn, (text + "\n" if text else "") + body)
        return
    if by("config"):
        yield from _config(turn, [atts[i] for i in by("config")], masked_total, text, route_text)
        return
    if by("code"):
        if masked_total:
            yield from say(_secrets_warning(masked_total) + "\n\n")
        yield from handle_code_review(turn, text or "проверь мой код")
        return
    docs = by("document")
    if docs:  # только метаданные: их несёт заявка на отсутствие (чекпоинт 11.11), файл никто не читает
        turn.meta["documents"] = [
            {"name": atts[i].get("name"), "mime": atts[i].get("mime"), "size": atts[i].get("size")} for i in docs
        ]
    st = turn.state().get("absence") or {}
    for_absence = docs and (absence.detect_type(text) or (st.get("slots") and not text) or st.get("step") == "proposed")
    allowed = turn.config.intent_allowed("absence", turn.user.role)
    if for_absence and allowed and not by("image") and not by("text"):
        yield from absence.handle(turn, text)
        return
    notes: list[str] = []
    if by("image"):
        notes.append(
            f"Скриншот ({describe([atts[i] for i in by('image')])}) я в прототипе не распознаю и не буду гадать, что на нём. "
            "Скопируй текст ошибки из консоли или опиши, что видишь на экране, — разберу. Если на скриншоте видны пароли "
            "или токены, не пересылай его в общие чаты."
        )
    if by("document"):
        hit = _doc_hit(turn, "common-documents-upload")
        if hit:
            yield _source_event([hit])
        notes.append(
            f"Документы ({describe([atts[i] for i in by('document')])}) я не открываю и не храню. Кадровые документы "
            "загружаются в кадровый портал («Мои документы»), документы для вуза — куратору стажировки, рабочие — в "
            "пространство команды. Паспорт и другие персональные документы в чат не отправляй."
        )
    plain = by("text")
    if plain and not text:
        if notes:
            yield from say("\n\n".join(notes) + "\n\n")
        yield from route_text("\n".join(texts[i][0] for i in plain)[:4000])
        return
    if notes:
        yield from say("\n\n".join(notes) + ("\n\n" if text else ""))
    if text:
        yield from route_text(text)


def _secrets_warning(n: int) -> str:
    return (
        f"В файле были секреты ({n}) — я их скрыл и дальше работаю только с замаскированным текстом. Секреты храни в "
        "хранилище секретов, а не в файлах репозитория."
    )


def _config(
    turn: Turn, atts: list[dict[str, Any]], masked: int, text: str, route_text: Callable[[str], Iterator[Event]]
) -> Iterator[Event]:
    hit = _doc_hit(turn, "eng-secrets")
    if hit:
        yield _source_event([hit])
    lines = [f"Посмотрел конфиг {describe(atts)}."]
    if masked:
        lines.append(_secrets_warning(masked))
        lines.append(
            "Если такой файл уже попал в git, сразу скажи наставнику: ключи нужно перевыпустить, удалить их из истории "
            "недостаточно."
        )
    else:
        lines.append("Явных секретов (паролей, токенов, ключей) не нашёл.")
    lines.append(
        "Проверь: значения для dev-стенда берутся из переменных окружения, пример лежит в `.env.example` без "
        "реальных значений, сам `.env` — в `.gitignore`."
    )
    yield from say("\n\n".join(lines) + ("\n\n" if text else ""))
    if text:
        yield from route_text(text)
