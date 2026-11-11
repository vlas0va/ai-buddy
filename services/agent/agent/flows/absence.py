"""S3 «Оформить отгул»: explain → collect dates → validate → draft (action_proposal) → confirm turn → MCP write.

Spec: openspec/specs/absence-request/spec.md (scenarios S3-01 … S3-12). Tool shapes: contracts/schemas/absence.json.
"""

from __future__ import annotations

import re
from collections.abc import Iterator
from datetime import date
from typing import Any

from .. import clock
from ..dates import DateRange, human_range, parse_range, working_days
from ..errors import ToolError
from ..retrieval import TEST_BADGE
from ..sse import Event, status
from ..text import normalize
from .common import CANCEL, CONFIRM, Turn, first_name, items, say

OTHER_TYPES = re.compile(r"\bотпуск|за\s+свой\s+сч[её]т|без\s+сохранени|больничн")
DATE_HINT = "Например: «в пятницу», «завтра» или «с 16 по 17 ноября»."
NOTICE_WORKING_DAYS = 2
STATUS_LABELS = {
    "pending_approval": "На согласовании",
    "approved": "Согласовано",
    "rejected": "Отклонено",
    "cancelled": "Отменено",
}


def looks_like_slot_answer(text: str) -> bool:
    t = normalize(text)
    return bool(parse_range(t, clock.today()) or CANCEL.search(t) or CONFIRM.search(t) or re.search(r"\bотгул", t))


def _source(doc: dict[str, Any]) -> Event:
    return Event(
        "sources",
        {
            "items": [
                {
                    "doc_id": doc["doc_id"],
                    "title": doc["title"],
                    "source_type": "test",
                    "url": doc.get("url", ""),
                    "badge": TEST_BADGE,
                }
            ]
        },
    )


def _explain(turn: Turn) -> Iterator[Event]:
    yield status("searching_kb", "Смотрю порядок оформления отгула")
    doc = turn.gateway.read("absence.get_policy")
    yield _source(doc)
    lead = turn.contact("lead")
    mentor = first_name(turn.contact("mentor"), "")
    who = f"наставника ({mentor})" if mentor else "наставника"
    approver = (lead or {}).get("name") or "руководитель команды"
    yield from say(
        f"По тестовой инструкции «{doc['title']}»: заранее предупреди руководителя и {who}, затем создай заявку в "
        f"сервисе отсутствий — я подготовлю черновик. Согласует {approver}, затем HR-администратор оформит отгул в "
        f"кадровой системе, а HR BP получит уведомление. Отгул — не больше 3 рабочих дней подряд.\n\n"
        f"На какие даты оформить? {DATE_HINT}"
    )


def _other_type(turn: Turn, text: str) -> Iterator[Event]:
    yield status("searching_kb")
    query = "больничный" if "больнич" in normalize(text) else "отпуск во время стажировки"
    hits = items(turn.gateway.read("kb.search", query=query, k=1))
    if hits:
        yield Event(
            "sources",
            {
                "items": [
                    {
                        "doc_id": hits[0]["doc_id"],
                        "title": hits[0]["title"],
                        "source_type": "test",
                        "url": hits[0].get("url", ""),
                        "badge": TEST_BADGE,
                    }
                ]
            },
        )
    yield from say(
        "Через меня в прототипе оформляется только отгул — до 3 рабочих дней. "
        + (
            f"По тестовой инструкции «{hits[0]['title']}»:\n\n{hits[0]['snippet']}"
            if hits
            else "Отпуск и больничный оформляются в сервисе отсутствий и через HR BP."
        )
    )


STEP_ROLES = {"lead": "руководитель", "hr_admin": "HR-администратор", "hrbp": "HR BP"}
STEP_STATUS = {
    "waiting": "ждёт",
    "approved": "согласовал",
    "rejected": "отклонил",
    "registered": "оформил в кадровой системе",
    "notified": "уведомлён",
    "skipped": "не требуется",
}


def steps_line(steps: list[dict[str, Any]]) -> str:
    """lead → HR admin → HR BP (organizers' Q&A 10.11), e.g. «руководитель (Мария Лебедева): согласовал → …»."""
    parts = []
    for s in steps or []:
        who = STEP_ROLES.get(s.get("role", ""), s.get("role", ""))
        name = f" ({s['display_name']})" if s.get("display_name") else ""
        parts.append(f"{who}{name}: {STEP_STATUS.get(s.get('status', ''), s.get('status', ''))}")
    return " → ".join(parts)


def handle_status(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю заявки в сервисе отсутствий")
    reqs = items(turn.gateway.read("absence.list_my_requests"))
    trip_lines: list[str] = []
    if turn.config.is_allowed("trips.list_my_requests", turn.user.role):
        try:
            for r in items(turn.gateway.read("trips.list_my_requests")):
                rng = DateRange(date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"]))
                trip_lines.append(
                    f"- {r['id']}: командировка {r['city']}, {human_range(rng)} — {STATUS_LABELS.get(r['status'], r['status'])}"
                )
        except ToolError:
            pass  # the trip service is optional for a day-off status
    if not reqs and not trip_lines:
        yield from say("У тебя пока нет заявок на отгул. Хочешь оформить? Напиши даты, например «в пятницу».")
        return
    lines = []
    for r in reqs:
        rng = DateRange(date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"]))
        approver = f", согласующий — {r['approver_display_name']}" if r.get("approver_display_name") else ""
        lines.append(f"- {r['id']}: отгул {human_range(rng)} — {STATUS_LABELS.get(r['status'], r['status'])}{approver}")
        if r.get("steps") and r["status"] != "pending_approval":
            lines.append(f"  Шаги: {steps_line(r['steps'])}")
    yield from say("Твои заявки:\n" + "\n".join(lines + trip_lines))


def handle(turn: Turn, text: str) -> Iterator[Event]:
    state = turn.state()
    st: dict[str, Any] = state.get("absence") or {"slots": {}, "step": "collect", "explained": False}
    t = normalize(text)

    if CANCEL.search(t):
        turn.clear_state()
        yield from say("Хорошо, черновик отменил. Если понадобится отгул — просто напиши даты.")
        return
    if OTHER_TYPES.search(t) and not re.search(r"\bотгул", t):
        yield from _other_type(turn, text)
        return

    rng = parse_range(t, clock.today())
    if st["step"] == "proposed" and rng is None and CONFIRM.search(t):
        yield from say(
            "Чтобы отправить заявку, нажми «Отправить» на карточке черновика. Без твоего подтверждения я заявку "
            "не создаю. Если нужно поменять даты — просто напиши новые."
        )
        return
    if rng is None:
        if not st.get("explained"):
            st["explained"] = True
            turn.save_state({**state, "absence": st})
            yield from _explain(turn)
        else:
            turn.save_state({**state, "absence": st})
            yield from say(f"Не получилось понять даты. {DATE_HINT}")
        return

    yield status("calling_service", "Проверяю даты в сервисе отсутствий")
    args = {"type": "day_off", "start_date": rng.start.isoformat(), "end_date": rng.end.isoformat()}
    check = turn.gateway.read("absence.validate_request", **args)
    st["slots"] = {"start_date": args["start_date"], "end_date": args["end_date"]}
    if not check.get("valid"):
        st["step"] = "collect"
        turn.save_state({**state, "absence": st})
        yield from say(
            f"Отгул {human_range(rng)} оформить не получится: {' '.join(check.get('problems', []))} "
            f"Выбери другие даты. {DATE_HINT}"
        )
        return

    approver_name = check.get("approver_display_name") or (turn.contact("lead") or {}).get("name") or ""
    days = working_days(rng.start, rng.end)
    write_args = {**args, "comment": ""}
    dates = rng.start.strftime("%d.%m.%Y") if rng.single else f"{rng.start:%d.%m.%Y}–{rng.end:%d.%m.%Y}"
    display = {
        "approver_display_name": approver_name,
        "dates": dates,
        "period_label": human_range(rng),
        "working_days": days,
        "type_label": "Отгул",
    }
    summary = f"Отгул {human_range(rng)}"
    proposal = turn.make_proposal(
        "absence_request",
        "absence.create_request",
        write_args,
        "Заявка на отгул",
        summary,
        display,
        requires_approval=bool(approver_name),
    )
    st.update(step="proposed", proposal_id=proposal["proposal_id"])
    turn.save_state({**state, "absence": st})

    notice = ""
    if working_days(clock.today(), rng.start) - 1 < NOTICE_WORKING_DAYS:
        notice = " Обычно об отгуле предупреждают за 2 рабочих дня — обязательно предупреди руководителя лично."
    mentor = first_name(turn.contact("mentor"), "")
    yield from say(
        f"Подготовил черновик: отгул {human_range(rng)}, рабочих дней: {days}. "
        f"Согласующий — {approver_name or 'руководитель'}. Проверь и нажми «Отправить».{notice} "
        f"Если отгул влияет на задачи, предупреди наставника{(' (' + mentor + ')') if mentor else ''} — могу подготовить сообщение."
    )
    yield Event("action_proposal", proposal)


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    rng = DateRange(date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"]))
    return {
        "external_id": r["id"],
        "status": r["status"],
        "status_label": STATUS_LABELS.get(r["status"], r["status"]),
        "approver_display_name": r.get("approver_display_name", ""),
        "summary": f"Отгул {human_range(rng)}",
        "followup": "Заявка отправлена на согласование. Статус обновится здесь, когда руководитель примет решение; "
        "после согласования HR-администратор оформит отгул в кадровой системе, а HR BP получит уведомление. "
        "Не забудь лично предупредить наставника, если отгул влияет на задачи.",
    }
