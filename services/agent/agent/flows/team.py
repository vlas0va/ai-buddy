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

    if not pending:
        yield from say("Заявок, ожидающих твоего согласования, нет.")
        return
    lines = [f"- {p['ref_id']}: {p.get('requester_display_name', '')} — {p['summary']}" for p in pending]
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
    blocks, missing = [], 0
    for m in selected:
        acc = m.get("accesses", [])
        missing += sum(1 for a in acc if a.get("status") == "not_requested")
        acc_lines = "\n".join(
            f"  - {a['name']}: {ACCESS_LABEL.get(a.get('status', ''), a.get('status', ''))}" for a in acc
        )
        tasks = m.get("open_tasks") or []
        task_line = f"\n  Открытых задач плана: {len(tasks)}" if tasks else ""
        blocks.append(f"{m.get('display_name', '')} — доступы:\n{acc_lines}{task_line}")
    tail = (
        f"\n\nНе запрошено доступов: {missing}. Чек-лист наставника (CL-01) просит проверить их в первую неделю — "
        "напомни стажёру или предложи написать мне «какие доступы мне нужны» — я подготовлю заявки."
        if missing
        else "\n\nВсе нужные доступы запрошены — шаг CL-01 «проверь доступы» можно отметить."
    )
    yield from say("\n".join(blocks) + tail)


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    label = "Согласовано" if r["status"] == "approved" else "Отклонено"
    return {
        "external_id": r.get("ref_id") or r["id"],
        "status": r["status"],
        "status_label": label,
        "approver_display_name": "",
        "summary": f"Заявка {r.get('ref_id', r['id'])} ({r.get('requester_display_name', '')}): {label.lower()}",
        "followup": "Стажёр увидит новый статус в AI-buddy.",
    }
