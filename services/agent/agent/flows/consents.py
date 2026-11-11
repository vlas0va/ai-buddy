"""Consents: show statuses, prepare the signature of a voluntary consent (photo and video, surveys) for confirmation.

Spec: openspec/specs/trips-consents/spec.md (CN-xx). Tool shapes: contracts/schemas/consents.json. Personal data and
NDA consents are signed in the HR portal at hire: buddy only shows their status. Revoking goes through the HR BP.
"""

from __future__ import annotations

import re
from collections.abc import Iterator
from datetime import datetime
from typing import Any

from ..sse import Event, status
from ..text import normalize
from .common import Turn, items, say

KINDS = [
    ("photo", re.compile(r"фото|видео|съемк|съёмк")),
    ("surveys", re.compile(r"опрос|анкет")),
    ("pdn", re.compile(r"персональн|\bпдн\b|обработк\w*\s+данн")),
    ("nda", re.compile(r"\bnda\b|конфиденциальн|неразглашен")),
]
SIGN = re.compile(r"подпи\w+|дать\s+согласие|даю\s+согласие|соглас(ен|на)\b|соглашаюсь|хочу\s+(дать|подписать)")
REVOKE = re.compile(r"отозв|отзыв|отказ\w*\s+от\s+соглас|забрать\s+согласие")
VIA = {"hr_portal": "в кадровом портале", "buddy": "через buddy"}


def _date(iso: str) -> str:
    try:
        return datetime.fromisoformat(iso.replace("Z", "+00:00")).strftime("%d.%m.%Y")
    except ValueError:
        return ""


def _kind(text: str) -> str | None:
    t = normalize(text)
    return next((k for k, rx in KINDS if rx.search(t)), None)


def handle(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю согласия")
    consents = items(turn.gateway.read("consents.list"))
    t = normalize(text)
    kind = _kind(text)
    target = next((c for c in consents if c["consent_id"] == kind), None)

    if REVOKE.search(t):
        hrbp = (turn.contact("hrbp") or {}).get("name")
        yield from say(
            "Отозвать согласие можно через HR BP"
            + (f" ({hrbp})" if hrbp else "")
            + ": напиши в #hr-questions, какое согласие отзываешь. Сам я согласия не отзываю."
        )
        return

    if target and SIGN.search(t):
        if target["status"] == "signed":
            when = _date(target.get("signed_at", ""))
            via = VIA.get(target.get("signed_via", ""), "")
            yield from say(
                f"Согласие «{target['title']}» уже подписано{(' ' + when) if when else ''}{(' ' + via) if via else ''}."
            )
            return
        if not target["signable"]:
            yield from say(
                f"«{target['title']}» подписывается только в кадровом портале при оформлении — через меня его подписать "
                "нельзя. Если в портале что-то не так, напиши HR BP."
            )
            return
        proposal = turn.make_proposal(
            "consent",
            "consents.sign",
            {"consent_id": target["consent_id"]},
            "Подписать согласие",
            f"Согласие: {target['title']}",
            {"consent_title": target["title"], "description": target.get("description", ""), "type_label": "Согласие"},
            requires_approval=False,
        )
        yield from say(
            f"Подготовил подпись согласия «{target['title']}»: {target.get('description', '')}. Это добровольно — "
            "отозвать можно через HR BP. Если согласен, нажми «Отправить» на карточке."
        )
        yield Event("action_proposal", proposal)
        return

    lines = []
    for c in consents:
        if c["status"] == "signed":
            when = _date(c.get("signed_at", ""))
            mark = f"подписано{(' ' + when) if when else ''} {VIA.get(c.get('signed_via', ''), '')}".rstrip()
        else:
            mark = "не подписано"
        tag = "обязательное" if c.get("required") else "добровольное"
        lines.append(f"- {c['title']} ({tag}) — {mark}")
    open_voluntary = [c for c in consents if c["signable"] and c["status"] != "signed"]
    tail = (
        "\n\nДобровольные согласия можно подписать через меня — напиши, например, «подпиши согласие на "
        + ("фото" if open_voluntary[0]["consent_id"] == "photo" else "опросы")
        + "». Обязательные подписываются в кадровом портале при оформлении."
        if open_voluntary
        else "\n\nВсе согласия подписаны. Обязательные подписываются в кадровом портале, отозвать добровольные можно через HR BP."
    )
    yield from say("Твои согласия:\n" + "\n".join(lines) + tail)


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    return {
        "external_id": r["id"],
        "status": "signed",
        "status_label": "Подписано",
        "approver_display_name": "",
        "summary": f"Согласие: {r['title']} — подписано",
        "followup": "Согласие подписано и сохранено в журнале действий. Отозвать его можно через HR BP.",
    }
