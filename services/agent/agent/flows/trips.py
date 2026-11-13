"""Командировка в другой офис Авито: объяснить → собрать город, даты, цель → проверить → черновик → подтверждение → запись
в MCP.

Спецификация: openspec/specs/trips-consents/spec.md (TR-xx). Формы инструментов: contracts/schemas/trips.json. Билеты и
гостиницу после согласования бронирует служба командировок: buddy никогда не бронирует и не платит (R72).
"""

from __future__ import annotations

import re
from collections.abc import Iterator
from datetime import date
from typing import Any

from .. import clock
from ..dates import DateRange, human_range, parse_range
from ..sse import Event, status
from ..text import normalize
from .common import CANCEL, CONFIRM, Turn, first_name, items, say

CITIES = [
    ("Москва", re.compile(r"\bмоскв\w*|\bмск\b")),
    ("Санкт-Петербург", re.compile(r"петербург\w*|\bпитер\w*|\bспб\b")),
    ("Казань", re.compile(r"\bказан\w*")),
    ("Екатеринбург", re.compile(r"екатеринбург\w*|\bекб\b")),
    ("Новосибирск", re.compile(r"новосибирск\w*|\bнск\b")),
]
PURPOSE = re.compile(r"(?:цель\s*[:—-]?\s*|чтобы\s+|для\s+(?=\w))(.{3,200})", re.IGNORECASE)
BOOKING = re.compile(r"билет|гостиниц|отел[ья]|жиль|суточн|компенсац|аванс")
DATE_HINT = "Например: «с 23 по 25 ноября»."
STATUS_LABELS = {
    "pending_approval": "На согласовании",
    "approved": "Согласовано",
    "rejected": "Отклонено",
    "cancelled": "Отменено",
}
SLOT_QUESTIONS = {
    "city": "В какой офис едешь? Офисы: {offices}.",
    "dates": f"На какие даты? {DATE_HINT}",
    "purpose": "Какая цель поездки? Например: «встреча с командой поиска» или «воркшоп по архитектуре».",
}


def find_city(text: str) -> str | None:
    t = normalize(text)
    return next((city for city, rx in CITIES if rx.search(t)), None)


def _purpose(text: str, asking: str | None) -> str:
    m = PURPOSE.search(text)
    if m:
        return m.group(1).strip(" .,!")
    if asking == "purpose" and not find_city(text) and not parse_range(normalize(text), clock.today()):
        return text.strip(" .,!")[:200]
    return ""


def looks_like_slot_answer(text: str, st: dict[str, Any]) -> bool:
    t = normalize(text)
    if CANCEL.search(t) or CONFIRM.search(t) or re.search(r"командировк", t):
        return True
    if find_city(text) or parse_range(t, clock.today()):
        return True
    return st.get("asking") == "purpose" and len(t) >= 3


def _explain(turn: Turn) -> Iterator[Event]:
    yield status("calling_service", "Смотрю правила командировок")
    pol = turn.gateway.read("trips.get_policy")
    lead = (turn.contact("lead") or {}).get("name") or "руководитель команды"
    yield from say(
        f"{pol['text']} Согласует {lead}.\n\n"
        "Чтобы подготовить заявку, напиши город, даты и цель — можно одним сообщением: "
        "«командировка в Казань с 23 по 25 ноября, цель — встреча с командой»."
    )


def handle(turn: Turn, text: str) -> Iterator[Event]:
    state = turn.state()
    st: dict[str, Any] = state.get("trip") or {"slots": {}, "step": "collect", "explained": False, "asking": None}
    t = normalize(text)
    if CANCEL.search(t):
        state.pop("trip", None)
        turn.save_state(state)
        yield from say("Хорошо, черновик командировки отменил.")
        return
    if BOOKING.search(t) and not re.search(r"оформ|заявк|командировк\w*\s+в", t):
        yield from say(
            "Билеты и гостиницу бронирует служба командировок после того, как руководитель согласует поездку — я их не "
            "бронирую и не оплачиваю. Могу подготовить саму заявку на командировку: напиши город, даты и цель."
        )
        return

    slots = st["slots"]
    found_city = find_city(text)
    rng = parse_range(t, clock.today())
    purpose = _purpose(text, st.get("asking"))
    if found_city:
        slots["city"] = found_city
    if rng:
        slots["start_date"], slots["end_date"] = rng.start.isoformat(), rng.end.isoformat()
    if purpose:
        slots["purpose"] = purpose

    if st["step"] == "proposed" and not (found_city or rng or purpose) and CONFIRM.search(t):
        yield from say(
            "Чтобы отправить заявку, нажми «Отправить» на карточке черновика — без подтверждения я её не создаю."
        )
        return
    if not slots and not st.get("explained"):
        st["explained"] = True
        turn.save_state({**state, "trip": st})
        yield from _explain(turn)
        return

    missing = [
        k
        for k, have in (("city", "city" in slots), ("dates", "start_date" in slots), ("purpose", "purpose" in slots))
        if not have
    ]
    if missing:
        st.update(step="collect", asking=missing[0])
        turn.save_state({**state, "trip": st})
        offices = ", ".join(c for c, _ in CITIES)
        got = []
        if "city" in slots:
            got.append(slots["city"])
        if "start_date" in slots:
            got.append(
                human_range(DateRange(date.fromisoformat(slots["start_date"]), date.fromisoformat(slots["end_date"])))
            )
        prefix = f"Записал: {', '.join(got)}. " if got else ""
        yield from say(prefix + " ".join(SLOT_QUESTIONS[m].format(offices=offices) for m in missing))
        return

    yield status("calling_service", "Проверяю заявку в сервисе командировок")
    args = {k: slots[k] for k in ("city", "start_date", "end_date", "purpose")}
    check = turn.gateway.read("trips.validate_request", **args)
    rng = DateRange(date.fromisoformat(args["start_date"]), date.fromisoformat(args["end_date"]))
    if not check.get("valid"):
        problems = [p.rstrip(".") for p in check.get("problems", [])]
        if any("город" in p for p in problems):
            slots.pop("city", None)
        if any(("дат" in p or "дней" in p or "начало" in p) for p in problems):
            slots.pop("start_date", None)
            slots.pop("end_date", None)
        if any("цель" in p for p in problems):
            slots.pop("purpose", None)
        st.update(step="collect", asking=None)
        turn.save_state({**state, "trip": st})
        yield from say(
            f"Командировку в {args['city']} ({human_range(rng)}) оформить не получится: "
            + "; ".join(problems)
            + ". Поправь, и я проверю снова."
        )
        return

    approver = check.get("approver_display_name") or (turn.contact("lead") or {}).get("name") or ""
    days = (rng.end - rng.start).days + 1
    dates = rng.start.strftime("%d.%m.%Y") if rng.single else f"{rng.start:%d.%m.%Y}–{rng.end:%d.%m.%Y}"
    display = {
        "approver_display_name": approver,
        "city": args["city"],
        "dates": dates,
        "period_label": human_range(rng),
        "days": days,
        "purpose": args["purpose"],
        "type_label": "Командировка",
    }
    proposal = turn.make_proposal(
        "trip_request",
        "trips.create_request",
        args,
        "Заявка на командировку",
        f"Командировка: {args['city']}, {human_range(rng)}",
        display,
        requires_approval=True,
    )
    st.update(step="proposed", proposal_id=proposal["proposal_id"], asking=None)
    turn.save_state({**state, "trip": st})
    mentor = first_name(turn.contact("mentor"), "")
    yield from say(
        f"Подготовил черновик: командировка в {args['city']}, {human_range(rng)} (дней: {days}), цель — {args['purpose']}. "
        f"Согласующий — {approver or 'руководитель'}. Проверь и нажми «Отправить». Билеты и гостиницу после согласования "
        f"забронирует служба командировок. Предупреди наставника{(' (' + mentor + ')') if mentor else ''}, если поездка "
        "сдвигает задачи."
    )
    yield Event("action_proposal", proposal)


def handle_status(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю заявки в сервисе командировок")
    reqs = items(turn.gateway.read("trips.list_my_requests"))
    if not reqs:
        yield from say("Заявок на командировку у тебя нет. Чтобы оформить, напиши город, даты и цель поездки.")
        return
    lines = []
    for r in reqs:
        rng = DateRange(date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"]))
        approver = f", согласующий — {r['approver_display_name']}" if r.get("approver_display_name") else ""
        lines.append(
            f"- {r['id']}: {r['city']}, {human_range(rng)} — {STATUS_LABELS.get(r['status'], r['status'])}{approver}"
        )
    yield from say("Твои командировки:\n" + "\n".join(lines))


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    rng = DateRange(date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"]))
    return {
        "external_id": r["id"],
        "status": r["status"],
        "status_label": STATUS_LABELS.get(r["status"], r["status"]),
        "approver_display_name": r.get("approver_display_name", ""),
        "summary": f"Командировка: {r['city']}, {human_range(rng)}",
        "followup": "Заявка на командировку отправлена руководителю. Когда он согласует, служба командировок "
        "забронирует билеты и гостиницу — статус обновится здесь.",
    }
