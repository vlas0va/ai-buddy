"""S4 «Доступы и каналы»: required accesses with statuses, request via access MCP (elevated → manager approval),
team channels + draft request to the mentor. Spec: openspec/specs/access-and-channels/spec.md (S4-01 … S4-09).
Tool shapes: contracts/schemas/access.json."""

from __future__ import annotations

import re
from collections.abc import Iterator
from typing import Any

from ..sse import Event, status
from ..text import normalize
from .common import Turn, first_name, items, person_name, say

REQUEST_VERB = re.compile(r"(запрос|оформ|дай|выда|нуж|подключ|открой|получ|хочу)")
ALL_WORDS = re.compile(r"\b(все|всё|недостающ\w*|остальн\w*)\b")
ELEVATED_HINT = re.compile(r"закрыт|приват|повышен|инцидент|дежурств")
TEAM_NAMES = {"delivery": re.compile(r"доставк"), "search": re.compile(r"\bпоиск")}
SYNONYMS = {
    "confluence": ["confluence", "конфлюенс", "вики", "wiki", "базе знаний", "база знаний"],
    "jira": ["jira", "джир", "жир", "трекер"],
    "bitbucket": ["bitbucket", "битбакет", "репозитор", "git", "гит", "код"],
}
KIND_SYNONYMS = {"space": ["пространств"], "channel": ["канал"]}
STATUS_LABEL = {
    "granted": "есть",
    "pending_approval": "ждёт согласования руководителя",
    "rejected": "отклонён",
    "not_requested": "не запрошен",
}
RESULT_LABEL = {"granted": "Доступ выдан", "pending_approval": "На согласовании", "rejected": "Отклонено"}


def _aliases(r: dict[str, Any]) -> list[str]:
    out = [r["name"].lower(), r["resource_id"].lower(), *SYNONYMS.get(r["resource_id"], [])]
    out += KIND_SYNONYMS.get(r.get("kind", ""), [])
    if "инцидент" in r["name"].lower():
        out.append("инцидент")
    return out


def resolve(text: str, resources: list[dict[str, Any]]) -> dict[str, Any] | None:
    t = normalize(text)
    best: tuple[int, dict[str, Any]] | None = None
    wants_elevated = bool(ELEVATED_HINT.search(t))
    for r in resources:
        hits = [a for a in _aliases(r) if a and a in t]
        if not hits:
            continue
        score = max(len(a) for a in hits)
        score += 20 if r["elevated"] == wants_elevated else -20
        if best is None or score > best[0]:
            best = (score, r)
    return best[1] if best else None


def _mentions_other_team(text: str, own_team: str) -> bool:
    t = normalize(text)
    own = TEAM_NAMES.get(own_team)
    return any(rx.search(t) for team, rx in TEAM_NAMES.items() if team != own_team) and not (own and own.search(t))


def _proposal(turn: Turn, r: dict[str, Any]) -> dict[str, Any]:
    lead = turn.contact("lead") or {}
    reason = "Онбординг стажёра: доступы первой недели (чек-лист)" if turn.user.role == "intern" else "Работа в команде"
    display = {
        "resource_name": r["name"],
        "elevated": r["elevated"],
        "kind": r.get("kind", ""),
        "approver_display_name": lead.get("name", "") if r["elevated"] else "",
    }
    title = "Заявка на доступ с согласованием" if r["elevated"] else "Заявка на доступ"
    return turn.make_proposal(
        "access_request",
        "access.request_access",
        {"resource_id": r["resource_id"], "reason": reason},
        title,
        f"Доступ: {r['name']}",
        display,
        requires_approval=r["elevated"],
        checklist_item_id=r.get("checklist_item_id") or None,
    )


def handle(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю доступы в сервисе доступов")
    required = items(turn.gateway.read("access.list_required"))
    target = resolve(text, required)
    t = normalize(text)

    if (
        _mentions_other_team(text, turn.user.team_id)
        and REQUEST_VERB.search(t)
        and (target is None or target.get("kind") == "space")
    ):
        lead = turn.contact("lead")
        msg = (
            "Доступы к ресурсам другой команды через меня не запрашиваются: их согласует руководитель той команды. "
            "Если это нужно для задачи — обсуди с руководителем."
        )
        yield Event(
            "refusal",
            {
                "reason": "no_rights",
                "redirect_role": "lead",
                "redirect_name": person_name(lead) or None,
                "detail": "other_team_resource",
                "text": msg,
            },
        )
        yield from say(msg)
        return

    if target is not None:
        if target["status"] in ("granted", "pending_approval"):
            yield from say(f"{target['name']}: {STATUS_LABEL[target['status']]}. Повторная заявка не нужна.")
            return
        proposal = _proposal(turn, target)
        approval = (
            f" Это доступ с повышенными правами — его согласует {person_name(turn.contact('lead'), 'руководитель')}."
            if target["elevated"]
            else " Обычный доступ выдаётся сразу после заявки."
        )
        yield from say(f"Подготовил заявку на доступ: {target['name']}.{approval} Нажми «Отправить», чтобы создать её.")
        yield Event("action_proposal", proposal)
        return

    if REQUEST_VERB.search(t) and re.search(r"доступ\w*\s+(к|в|на)\s+\w+", t) and not ALL_WORDS.search(t):
        names = ", ".join(r["name"] for r in required)
        yield from say(
            f"Не нашёл такой ресурс среди доступов твоей команды. Доступные: {names}. Если нужен другой — "
            "уточни у наставника."
        )
        return

    lines, missing = [], []
    for r in required:
        extra = " (повышенные права, нужно согласование руководителя)" if r["elevated"] else ""
        lines.append(f"- {r['name']} — {STATUS_LABEL.get(r['status'], r['status'])}{extra}")
        if r["status"] == "not_requested" and not r["elevated"]:
            missing.append(r)
    intro = "Для работы в команде нужны такие доступы:" if turn.user.role == "intern" else "Доступы команды:"
    tail = (
        f"\n\nПодготовил заявки на недостающие обычные доступы ({len(missing)}) — отправь нужные карточками. "
        "Доступ с повышенными правами запрошу по отдельной просьбе, например «запроси закрытое пространство»."
        if missing
        else "\n\nВсе обычные доступы уже есть или запрошены."
    )
    yield from say(f"{intro}\n" + "\n".join(lines) + tail)
    for r in missing[:5]:
        yield Event("action_proposal", _proposal(turn, r))


def handle_channels(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю каналы команды")
    channels = items(turn.gateway.read("access.list_team_channels"))
    lines = [f"- {c['name']} — {c.get('purpose', '')}{' (закрытый)' if c.get('closed') else ''}" for c in channels]
    yield from say("Каналы и чаты команды:\n" + "\n".join(lines))
    if turn.user.role != "intern":
        return
    mentor = turn.contact("mentor")
    names = ", ".join(c["name"] for c in channels)
    hello = f"{first_name(mentor)}, привет!" if mentor else "Привет!"
    who = f" ({person_name(mentor)})" if mentor else ""
    yield from say(
        f"\n\nДобавить в каналы может наставник{who}. Подготовил черновик сообщения — можешь отредактировать и "
        "отправить."
    )
    yield Event(
        "escalation_draft",
        {
            "to_role": "mentor",
            "to_user_id": (mentor or {}).get("user_id"),
            "to_name": person_name(mentor),
            "topic": "channels",
            "text": f"{hello} Добавь меня, пожалуйста, в каналы команды: {names} — включая закрытые. Спасибо!",
        },
    )


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    granted = r["status"] == "granted"
    lead = contacts.get("lead") or {}
    return {
        "external_id": r["id"],
        "status": r["status"],
        "status_label": RESULT_LABEL.get(r["status"], r["status"]),
        "approver_display_name": "" if granted else lead.get("name", ""),
        "summary": f"Доступ: {r.get('resource_name') or r['resource_id']}",
        "followup": "Доступ выдан — можно пользоваться."
        if granted
        else "Заявка отправлена руководителю на согласование. Статус обновится здесь после решения.",
    }
