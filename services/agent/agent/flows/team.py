"""Scenarios for mentor and lead in the same chat (R57, R67): approvals (lead), mentee progress (mentor).
Spec: openspec/specs/team-roles/spec.md (TR-01 … TR-07). Tool shapes: contracts/schemas/approvals.json."""

from __future__ import annotations

import re
from collections.abc import Iterator
from typing import Any

from ..nlu.router import NOT_NAMES
from ..sse import Event, status
from ..text import normalize, same_person
from .common import Turn, items, say

REQ_ID = re.compile(r"\b(abs|acc|apr|trp)-([0-9a-f]+)\b", re.IGNORECASE)
REJECT = re.compile(r"отклон|откаж|не\s+согласу|не\s+одобр")
APPROVE = re.compile(r"согласу|одобр|утверд|подтверд|разреш")
COMMENT = re.compile(r"(?:потому\s+что|причина:?|комментари\w*:?|так\s+как)\s+(.+)$", re.IGNORECASE)
ACCESS_LABEL = {
    "granted": "выдан",
    "pending_approval": "ждёт согласования",
    "rejected": "отклонён",
    "not_requested": "не запрошен",
}


def _mentions(text: str) -> list[str]:
    """Capitalised words that look like first names (not at the start of the message, not product names)."""
    words = re.findall(r"[А-ЯЁ][а-яё]+", text)
    first_word = (text.strip().split() or [""])[0]
    return [w for w in words if w not in NOT_NAMES and not (w == first_word and text.strip().startswith(w))]


def _stem_match(name: str, text: str) -> bool:
    return any(same_person(w, name) for w in _mentions(text))


def handle_approvals(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю заявки на согласование")
    pending = items(turn.gateway.read("approvals.list_pending"))
    t = normalize(text)
    listing = re.search(r"покаж|списо|какие|что\s+(ждет|висит|на)", t)
    decision = "rejected" if REJECT.search(t) else ("approved" if APPROVE.search(t) and not listing else None)
    target = None
    m = REQ_ID.search(text)
    if m:
        ref = f"{m.group(1).upper()}-{m.group(2)}"
        target = next((p for p in pending if ref in (p["ref_id"].upper(), p["id"].upper())), None)
        if target is None and decision:
            yield from say(f"Заявки {ref} нет среди ожидающих твоего согласования.")
            return
    elif decision:
        matches = [p for p in pending if _stem_match(p.get("requester_display_name", ""), text)]
        target = matches[0] if len(matches) == 1 else None

    if decision and target:
        cm = COMMENT.search(text)
        comment = cm.group(1).strip() if cm else ""
        verb = "Согласовать" if decision == "approved" else "Отклонить"
        who = target.get("requester_display_name", "")
        display = {
            "ref_id": target["ref_id"],
            "requester_display_name": who,
            "request_summary": target["summary"],
            "decision_label": verb,
            "comment": comment,
        }
        proposal = turn.make_proposal(
            "approval_decision",
            "approvals.decide",
            {"approval_id": target["id"], "decision": decision, "comment": comment},
            f"{verb} заявку {target['ref_id']}",
            f"{verb}: {who}, {target['summary']}",
            display,
            requires_approval=False,
        )
        yield from say(f"{verb} заявку {target['ref_id']} ({who}, {target['summary']})? Подтверди на карточке.")
        yield Event("action_proposal", proposal)
        return

    how = re.search(r"\bкак\b|порядок|правил", t) and not decision
    if how:  # «как согласовывать заявки стажёров?»: the process, then the queue (QA 12.11)
        stage = (
            "Ты — второй этап: заявки на отсутствие приходят после согласования руководителя. Согласованная заявка "
            "оформлена, HR BP получает уведомление."
            if turn.user.role == "hr_admin"
            else "Отсутствия после тебя согласует HR-администратор; пока он не согласовал, стажёр может перенести даты. "
            "Доступы на запись, администрирование и к prod выдаются только с твоего согласия."
        )
        yield from say(
            "Как согласовывать: открой раздел «Согласования» — там видно, кто, что и на какие даты, и кнопки "
            "«Согласовать» / «Отклонить» с комментарием. В чате можно написать «согласуй ABS-000101» или «отклони … "
            f"потому что …» — решение уйдёт после подтверждения на карточке. {stage}\n\n"
        )
    if not pending:
        yield from say("Заявок, ожидающих твоего согласования, нет.")
        return
    stage = {"hr_admin": " (этап HR-администратора)"}
    lines = [
        f"- {p['ref_id']}: {p.get('requester_display_name', '')} — {p['summary']}{stage.get(p.get('stage', ''), '')}"
        for p in pending
    ]
    yield from say(
        "Ожидают твоего согласования:\n"
        + "\n".join(lines)
        + f"\n\nНапиши, например, «согласуй {pending[0]['ref_id']}» или «отклони … потому что …». "
        "Решение можно принять и кнопками в разделе «Согласования»."
    )


def handle_mentee(turn: Turn, text: str) -> Iterator[Event]:
    """Mentor (and lead) see their interns' progress from user_context.mentees, filled by core (GET /api/team/interns)."""
    mentees = turn.user.mentees
    if not mentees:
        yield from say(
            "Данные о стажёрах сейчас недоступны в чате — открой раздел «Мои стажёры»: там видно план и "
            "заявки каждого стажёра."
        )
        return
    selected = [m for m in mentees if _stem_match(m.get("display_name", ""), text)]
    if not selected and _mentions(text):
        msg = "Среди твоих стажёров нет такого человека. Данные других стажёров я не показываю."
        yield Event(
            "refusal",
            {
                "reason": "no_rights",
                "detail": "not_my_mentee",
                "redirect_role": None,
                "redirect_name": None,
                "text": msg,
            },
        )
        yield from say(msg)
        return
    selected = selected or mentees
    blocks, missing, idle = [], 0, []
    for m in selected:
        acc = m.get("accesses", [])
        missing += sum(1 for a in acc if a.get("status") == "not_requested")
        acc_lines = (
            "\n".join(f"  - {a['name']}: {ACCESS_LABEL.get(a.get('status', ''), a.get('status', ''))}" for a in acc)
            or "  через buddy пока ничего не запрашивал(а)"
        )
        if not acc:
            idle.append(m.get("display_name", ""))  # nothing requested yet is not «all requested» (QA 12.11)
        tasks = m.get("open_tasks") or []
        task_line = f"\n  Открытых задач плана: {len(tasks)}" if tasks else ""
        blocks.append(f"{m.get('display_name', '')} — доступы:\n{acc_lines}{task_line}")
    if idle:
        tail = (
            f"\n\nЕщё не запрашивали доступы: {', '.join(idle)}. Чек-лист наставника (CL-01) просит проверить их в первую "
            "неделю — напомни стажёру написать мне «какие доступы мне нужны»: я покажу список и подготовлю заявки."
        )
        yield from say("\n".join(blocks) + tail)
        return
    tail = (
        f"\n\nНе запрошено доступов: {missing}. Чек-лист наставника (CL-01) просит проверить их в первую неделю — "
        "напомни стажёру или предложи написать мне «какие доступы мне нужны» — я подготовлю заявки."
        if missing
        else "\n\nВсе нужные доступы запрошены — шаг CL-01 «проверь доступы» можно отметить."
    )
    yield from say("\n".join(blocks) + tail)


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    label = "Согласовано" if r["status"] == "approved" else "Отклонено"
    followup = "Стажёр увидит новый статус в AI-buddy."
    to_hr = r.get("kind") == "absence_request" and r.get("stage", "lead") == "lead" and r["status"] == "approved"
    if to_hr:  # the lead's approval passes the absence request to the HR admin (checkpoint 11.11)
        label = "Согласовано руководителем"
        followup = "Заявка передана HR-администратору на второй этап. Стажёр увидит новый статус в AI-buddy."
    elif r.get("stage") == "hr_admin" and r["status"] == "approved":
        followup = "Заявка согласована и оформлена, HR BP получил уведомление. Сотрудник увидит статус в AI-buddy."
    return {
        "external_id": r.get("ref_id") or r["id"],
        "status": r["status"],
        "status_label": label,
        "approver_display_name": "",
        "summary": f"Заявка {r.get('ref_id', r['id'])} ({r.get('requester_display_name', '')}): {label.lower()}",
        "followup": followup,
    }


def handle_one_on_one(turn: Turn, text: str) -> Iterator[Event]:
    """Mentor or lead prepares a 1:1 with an intern: progress from user_context.mentees, an agenda, feedback tips (S1-16)."""
    from .coach import _doc_hit, _source_event

    mentees = turn.user.mentees
    selected = [m for m in mentees if _stem_match(m.get("display_name", ""), text)] or mentees[:1]
    tips = _doc_hit(turn, "mentor-feedback-guide")
    if tips:
        yield _source_event([tips])
    if not selected:
        yield from say(
            "Данных о стажёрах в чате нет — открой «Мои стажёры». Общая повестка 1:1: что получилось за период, "
            "где застрял, обратная связь по фактам, цели на следующий этап."
        )
        return
    m = selected[0]
    name = m.get("display_name", "стажёр")
    tasks = m.get("open_tasks") or []
    overdue = [t for t in tasks if t.get("status") == "overdue"]
    waiting = [a for a in m.get("accesses", []) if a.get("status") in ("not_requested", "pending_approval")]
    lines = [f"Повестка 1:1 с {name}:", "", "1. Что получилось за период — попроси рассказать самого стажёра."]
    if overdue:
        lines.append("2. Где застрял — просрочено: " + "; ".join(f"«{t.get('title', '')}»" for t in overdue[:3]) + ".")
    elif tasks:
        lines.append(
            f"2. Где нужна помощь — открытых задач плана: {len(tasks)}, ближайшая: «{tasks[0].get('title', '')}»."
        )
    else:
        lines.append("2. Где нужна помощь — открытых задач нет, обсудите следующий этап.")
    if waiting:
        lines.append("3. Доступы — ещё не готовы: " + ", ".join(a.get("name", "") for a in waiting[:4]) + ".")
    else:
        lines.append("3. Доступы — все нужные выданы или запрошены.")
    lines += [
        "4. Обратная связь — по фактам: что сделал, какой результат, что улучшить; начни с сильной стороны.",
        "5. Цели на следующий этап — 1–2 измеримые, запиши их в «Задать цели» в плане стажёра.",
        "",
        "Саму встречу и обратную связь проводишь ты — я только собрал данные из плана.",
    ]
    yield from say("\n".join(lines))
