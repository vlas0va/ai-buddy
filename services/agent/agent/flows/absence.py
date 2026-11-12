"""S3 «Оформить отсутствие»: explain → collect dates → validate → draft (action_proposal) → confirm turn → MCP write.

One request with a type (отгул, отпуск, отсутствие, декрет), optional documents from the chat attachments, approval by
the lead and then by the HR admin; until the HR admin approves, the dates can be moved (checkpoint 11.11).
Spec: openspec/specs/absence-request/spec.md (S3-01 … S3-29). Tool shapes: contracts/schemas/absence.json.
"""

from __future__ import annotations

import re
from collections.abc import Iterator
from datetime import date, timedelta
from typing import Any

from .. import clock
from ..dates import DateRange, human_range, parse_range, working_days
from ..errors import ToolError
from ..retrieval import TEST_BADGE
from ..sse import Event, status
from ..text import normalize
from .common import CANCEL, CONFIRM, Turn, first_name, items, say

OTHER_TYPES = re.compile(r"больничн")
TYPE_PATTERNS = [  # order matters: «отпуск за свой счёт» is an absence, not a vacation
    ("parental_leave", re.compile(r"\bдекрет|по\s+уходу\s+за\s+реб|по\s+беременност|по\s+родам")),
    ("absence", re.compile(r"\bотсутстви|за\s+свой\s+сч[её]т|без\s+сохранени|\bучеб|\bсесси|экзамен|семейн")),
    ("vacation", re.compile(r"\bотпуск")),
    ("day_off", re.compile(r"\bотгул|выходн")),
]
TYPE_LABELS = {"day_off": "Отгул", "vacation": "Отпуск", "absence": "Отсутствие", "parental_leave": "Декрет"}
TYPE_GENITIVE = {"day_off": "отгула", "vacation": "отпуска", "absence": "отсутствия", "parental_leave": "декрета"}
INFO_QUESTION = re.compile(r"сколько|положен|как\s+(взять|оформ|уйти|получ)|можно\s+ли|когда\s+можно|правил|порядок")
RESCHEDULE = re.compile(
    r"перенес|перенос|сдвин|подвин|(измен|поменя|помен|смен)\w*\s+(\w+\s+){0,2}(дат|отгул|отпуск|заявк|отсутств|декрет)"
)
ABS_ID = re.compile(r"\babs-(\d+)\b", re.IGNORECASE)
WITHDRAW = re.compile(r"(отмен|отзов|отозв|аннулир)\w*\s+(\w+\s+){0,2}(о+тгул|отпуск|заявк|отсутств|декрет|abs-)")
OPEN = ("pending_approval", "pending_hr")
MAX_DOCUMENTS = 3
DATE_HINT = "Например: «в пятницу», «завтра» или «с 16 по 17 ноября»."
NOTICE_WORKING_DAYS = 3  # feedback 12.11: «отгул берётся не позже чем за 3 дня»
BARE_DAY = re.compile(r"^\s*(\d{1,2})\s*$")  # «13» answering «на какие даты?»
STATUS_LABELS = {
    "pending_approval": "На согласовании",
    "pending_hr": "Ждёт HR-администратора",
    "approved": "Согласовано",
    "rejected": "Отклонено",
    "cancelled": "Отменено",
}


def bare_day(text: str) -> DateRange | None:
    """«13» → the nearest 13th from tomorrow on (this month or the next)."""
    m = BARE_DAY.match(text)
    if not m or not 1 <= int(m.group(1)) <= 31:
        return None
    today, day = clock.today(), int(m.group(1))
    for months in range(0, 3):
        y, mo = today.year + (today.month - 1 + months) // 12, (today.month - 1 + months) % 12 + 1
        try:
            d = date(y, mo, day)
        except ValueError:
            continue
        if d > today:
            return DateRange(d, d)
    return None


def busy_ranges(turn: Turn) -> list[dict[str, str]]:
    """The user's open and approved requests: the calendar marks those days (feedback 12.13)."""
    try:
        reqs = items(turn.gateway.read("absence.list_my_requests"))
    except Exception:  # noqa: BLE001 - the calendar works without them
        return []
    return [
        {"start": r["start_date"], "end": r["end_date"], "label": f"{type_label(r.get('type', 'day_off'))} {r['id']}"}
        for r in reqs
        if r.get("status") in (*OPEN, "approved")
    ][:20]


def date_picker(
    kind: str, purpose: str = "absence", ref_id: str = "", busy: list[dict[str, str]] | None = None, label: str = ""
) -> Event:
    """A calendar under the answer (feedback 12.11): the user picks the dates instead of typing them."""
    today = clock.today()
    earliest = {"vacation": today + timedelta(days=14), "parental_leave": today}.get(kind, today + timedelta(days=1))
    labels = {"day_off": "отгула", "vacation": "отпуска", "absence": "отсутствия", "parental_leave": "декрета"}
    return Event(
        "date_picker",
        {
            "purpose": purpose,
            "kind": kind,
            "ref_id": ref_id,
            "min": earliest.isoformat(),
            "max": (today + timedelta(days=365)).isoformat(),
            "label": label or ("Новые даты " if purpose == "reschedule" else "Даты ") + labels.get(kind, "отгула"),
            "busy": busy or [],
        },
    )


def looks_like_slot_answer(text: str) -> bool:
    t = normalize(text)
    return bool(
        bare_day(t)
        or parse_range(t, clock.today())
        or CANCEL.search(t)
        or CONFIRM.search(t)
        or any(rx.search(t) for _, rx in TYPE_PATTERNS)
    )


def detect_type(text: str) -> str | None:
    t = normalize(text)
    return next((kind for kind, rx in TYPE_PATTERNS if rx.search(t)), None)


def type_label(kind: str) -> str:
    return TYPE_LABELS.get(kind or "day_off", "Отгул")


def documents_of(turn: Turn) -> list[dict[str, Any]]:
    """Documents attached to this message (metadata only, never read), as absence.create_request takes them."""
    docs = []
    for a in turn.meta.get("documents") or []:
        name = str(a.get("name") or "документ")[:200]
        docs.append(
            {"name": name, "mime": str(a.get("mime") or "application/octet-stream"), "size": int(a.get("size") or 0)}
        )
    return docs[:MAX_DOCUMENTS]


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


PROCESS = (
    "Заявка одна для всех видов: даты → вид (отгул, отпуск, отсутствие, декрет) → по желанию документы (приложи файл к "
    "сообщению) → согласует руководитель → затем HR-администратор, и заявка согласована. Пока HR-администратор не "
    "согласовал, даты можно перенести: напиши «перенеси заявку на …»."
)
VACATION_FACTS = (
    "По общим нормам ТК РФ ежегодный оплачиваемый отпуск — 28 календарных дней (ст. 115). Право на отпуск за первый "
    "год появляется после 6 месяцев работы, раньше — по соглашению с работодателем (ст. 122). Отпуск можно делить на "
    "части, одна — не меньше 14 дней (ст. 125). Это нормы закона, а не регламент компании: условия своего договора "
    "уточни у HR BP."
)
TYPE_RULES = {
    "vacation": "В прототипе заявку на отпуск подают минимум за 14 календарных дней, за один раз — до 28 дней.",
    "absence": "«Отсутствие» — до 5 дней: учёба, сессия, семейные обстоятельства, отпуск за свой счёт. Можно приложить "
    "подтверждающий документ (например, справку-вызов из вуза).",
    "parental_leave": "Для декрета нужен подтверждающий документ (заявление) — приложи его к сообщению с датами. "
    "Детали оформления подскажет HR BP.",
}
DATE_HINTS = {
    "vacation": "Например: «с 1 по 14 декабря».",
    "absence": "Например: «с 20 по 21 ноября».",
    "parental_leave": "Например: «с 1 декабря по 30 марта».",
}


def _kb_source(turn: Turn, query: str) -> Iterator[Event]:
    hits = items(turn.gateway.read("kb.search", query=query, k=1))
    if hits:
        yield _source({**hits[0], "doc_id": hits[0]["doc_id"]})


def _explain_type(turn: Turn, kind: str) -> Iterator[Event]:
    yield status("searching_kb", f"Смотрю порядок оформления {TYPE_GENITIVE[kind]}")
    yield from _kb_source(turn, "отпуск во время стажировки")
    approver = (turn.contact("lead") or {}).get("name") or "руководитель команды"
    facts = f"{VACATION_FACTS}\n\n" if kind == "vacation" else ""
    yield from say(
        f"{facts}Как оформить: {PROCESS} Первым согласует {approver}. {TYPE_RULES[kind]}\n\n"
        f"Выбери даты {TYPE_GENITIVE[kind]} в календаре ниже или напиши их. {DATE_HINTS[kind]}"
    )
    yield date_picker(kind, busy=busy_ranges(turn))


def _explain(turn: Turn) -> Iterator[Event]:
    yield status("searching_kb", "Смотрю порядок оформления отгула")
    doc = turn.gateway.read("absence.get_policy")
    yield _source(doc)
    lead = turn.contact("lead")
    mentor = first_name(turn.contact("mentor"), "")
    who = f"наставника ({mentor})" if mentor else "наставника"
    approver = (lead or {}).get("name") or "руководитель команды"
    yield from say(
        f"Отгул оформляют заранее — не позже чем за {NOTICE_WORKING_DAYS} рабочих дня, и не больше 3 рабочих дней "
        f"подряд. Предупреди {who}. Согласует {approver}, затем HR-администратор. Пока HR-администратор не согласовал "
        f"(даже если руководитель уже согласовал), даты можно поменять.\n\n"
        f"Выбери даты в календаре ниже или напиши, например, «в пятницу»."
    )
    yield date_picker("day_off", busy=busy_ranges(turn))


def _other_type(turn: Turn, text: str) -> Iterator[Event]:
    yield status("searching_kb")
    hits = items(turn.gateway.read("kb.search", query="больничный", k=1))
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
        "Больничный через меня не оформляется: медицинские данные в чат не отправляй. Сообщи руководителю, что "
        "заболел, а оформление подскажет HR BP. "
        + (f"Подробнее — в тестовой инструкции «{hits[0]['title']}»." if hits else "")
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
        yield from say(
            "У тебя пока нет заявок на отсутствие. Хочешь оформить отгул или отпуск? Напиши даты, например «в пятницу»."
        )
        return
    lines = []
    for r in reqs:
        rng = DateRange(date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"]))
        approver = f", согласующий — {r['approver_display_name']}" if r.get("approver_display_name") else ""
        label = type_label(r.get("type", "day_off")).lower()
        lines.append(
            f"- {r['id']}: {label} {human_range(rng)} — {STATUS_LABELS.get(r['status'], r['status'])}{approver}"
        )
        if r.get("steps") and r["status"] != "pending_approval":
            lines.append(f"  Шаги: {steps_line(r['steps'])}")
    movable = any(r["status"] in ("pending_approval", "pending_hr") for r in reqs)
    tail = (
        "\n\nПока HR-администратор не согласовал заявку, её можно перенести: «перенеси заявку на …»." if movable else ""
    )
    yield from say("Твои заявки:\n" + "\n".join(lines + trip_lines) + tail)


def handle(turn: Turn, text: str) -> Iterator[Event]:
    state = turn.state()
    st: dict[str, Any] = state.get("absence") or {"slots": {}, "step": "collect", "explained": False}
    t = normalize(text)

    if WITHDRAW.search(t):
        yield from handle_withdraw(turn, text)
        return
    if CANCEL.search(t):
        turn.clear_state()
        yield from say("Хорошо, черновик отменил. Если понадобится отгул или отпуск — просто напиши даты.")
        return
    if OTHER_TYPES.search(t):
        yield from _other_type(turn, text)
        return
    if RESCHEDULE.search(t):
        yield from handle_reschedule(turn, text)
        return

    detected = detect_type(text)
    kind = detected or st.get("type") or "day_off"
    type_changed = detected is not None and detected != st.get("type", "day_off")
    st["type"] = kind
    docs = documents_of(turn) or st.get("documents") or []
    st["documents"] = docs

    rng = parse_range(t, clock.today()) or bare_day(t)
    if st["step"] == "proposed" and rng is None and CONFIRM.search(t) and not type_changed:
        yield from say(
            "Чтобы отправить заявку, нажми «Отправить» на карточке черновика. Без твоего подтверждения я заявку "
            "не создаю. Если нужно поменять даты — просто напиши новые."
        )
        return
    if rng is None and documents_of(turn) and st.get("slots"):
        s0 = st["slots"]  # a document sent after the dates: build the draft with it
        rng = DateRange(date.fromisoformat(s0["start_date"]), date.fromisoformat(s0["end_date"]))
    if rng is None:
        explain_again = not st.get("explained") or type_changed or INFO_QUESTION.search(t)
        st["explained"] = True
        st["step"] = "collect"
        turn.save_state({**state, "absence": st})
        if explain_again:
            yield from (_explain(turn) if kind == "day_off" else _explain_type(turn, kind))
        else:
            yield from say("Не получилось понять даты — выбери их в календаре ниже.")
            yield date_picker(kind, busy=busy_ranges(turn))
        return

    label = type_label(kind)
    replaces: tuple[str, str] | None = None
    yield status("calling_service", "Проверяю даты в сервисе отсутствий")
    args: dict[str, Any] = {"type": kind, "start_date": rng.start.isoformat(), "end_date": rng.end.isoformat()}
    if docs:
        args["documents"] = docs
    check = turn.gateway.read("absence.validate_request", **args)
    st["slots"] = {"start_date": args["start_date"], "end_date": args["end_date"]}
    if not check.get("valid"):
        st["step"] = "collect"
        turn.save_state({**state, "absence": st})
        problems = _sentences(check.get("problems", []))
        overlap = ABS_ID.search(problems)
        if overlap:
            reqs = items(turn.gateway.read("absence.list_my_requests"))
            ref = f"ABS-{overlap.group(1)}"
            other = next((r for r in reqs if r["id"].upper() == ref), None)
            inside = (
                other is not None
                and rng.start.isoformat() <= other["start_date"]
                and other["end_date"] <= rng.end.isoformat()
            )
            o_days = (
                (date.fromisoformat(other["end_date"]) - date.fromisoformat(other["start_date"])).days if other else 0
            )
            longer = other is not None and (other.get("type", "day_off") != kind or (rng.end - rng.start).days > o_days)
            if other is not None and other.get("status") in OPEN and inside and longer:
                # feedback 12.13: a longer absence covers a pending request — it replaces that request in one step
                check2 = turn.gateway.read("absence.validate_request", **args, exclude_id=ref)
                o_label = f"{type_label(other.get('type', 'day_off')).lower()} {_period(other)}"
                if check2.get("valid"):
                    replaces = (ref, o_label)
                    check = check2
                else:
                    rest = _sentences(check2.get("problems", []))
                    yield from say(
                        f"{label} {human_range(rng)} пока не оформить: {rest} Заявка {ref} ({o_label}) попадает внутрь "
                        f"новых дат — когда всё будет готово, я её отменю автоматически, отдельно отменять не нужно."
                    )
                    if kind == "parental_leave" and not docs:
                        yield from say(" Приложи заявление скрепкой и снова отправь даты.")
                    else:
                        yield date_picker(kind, busy=busy_ranges(turn))
                    return
            elif other is not None and other.get("status") == "approved":
                yield from say(
                    f"{label} {human_range(rng)} оформить не получится: {problems} Заявка {ref} уже согласована — "
                    "отменить или изменить её может только HR-администратор, напиши ему. Или выбери другие даты ниже."
                )
                yield date_picker(kind, busy=busy_ranges(turn))
                return
            else:  # a partial overlap with an open request: move that request, or pick other dates
                o_kind = (other or {}).get("type", "day_off")
                yield from say(
                    f"{label} {human_range(rng)} оформить не получится: {problems} Можно перенести заявку {ref} "
                    f"({type_label(o_kind).lower()}) — выбери для неё новые даты ниже: пока HR-администратор не "
                    "согласовал, перенос возможен. Или напиши другие даты для новой заявки."
                )
                yield date_picker(
                    o_kind,
                    "reschedule",
                    ref,
                    busy=busy_ranges(turn),
                    label=f"Новые даты: {type_label(o_kind).lower()} {ref}",
                )
                return
        if not overlap or check.get("valid") is not True:
            yield from say(
                f"{label} {human_range(rng)} оформить не получится: {problems} "
                f"Выбери другие даты{' или приложи документ' if kind == 'parental_leave' and not docs else ''} "
                "в календаре ниже."
            )
            yield date_picker(kind, busy=busy_ranges(turn))
            return

    approver_name = check.get("approver_display_name") or (turn.contact("lead") or {}).get("name") or ""
    days = working_days(rng.start, rng.end)
    calendar_days = (rng.end - rng.start).days + 1
    write_args = {**args, "comment": ""}
    if replaces:
        write_args["replaces"] = replaces[0]
    dates = rng.start.strftime("%d.%m.%Y") if rng.single else f"{rng.start:%d.%m.%Y}–{rng.end:%d.%m.%Y}"
    display: dict[str, Any] = {
        "approver_display_name": approver_name,
        "dates": dates,
        "period_label": human_range(rng),
        "working_days": days,
        "type": kind,
        "type_label": label,
        "documents": [d["name"] for d in docs],
    }
    if kind != "day_off":
        display["calendar_days"] = calendar_days
    if replaces:
        display["replaces"] = f"{replaces[0]} ({replaces[1]}) — будет отменена"
    summary = f"{label} {human_range(rng)}"
    proposal = turn.make_proposal(
        "absence_request",
        "absence.create_request",
        write_args,
        f"Заявка: {label.lower()}",
        summary,
        display,
        requires_approval=bool(approver_name),
    )
    st.update(step="proposed", proposal_id=proposal["proposal_id"])
    turn.save_state({**state, "absence": st})

    notice = ""
    if kind == "day_off" and working_days(clock.today(), rng.start) - 1 < NOTICE_WORKING_DAYS:
        notice = (
            f" Отгул оформляют не позже чем за {NOTICE_WORKING_DAYS} рабочих дня — обязательно предупреди руководителя "
            "лично."
        )
    length = f"рабочих дней: {days}" if kind == "day_off" else f"календарных дней: {calendar_days}"
    doc_line = f" Документы: {', '.join(d['name'] for d in docs)}." if docs else ""
    mentor = first_name(turn.contact("mentor"), "")
    replace_line = (
        f" Заявка {replaces[0]} ({replaces[1]}) попадает внутрь — после отправки она отменится сама."
        if replaces
        else ""
    )
    yield from say(
        f"Подготовил черновик: {label.lower()} {human_range(rng)}, {length}.{doc_line}{replace_line} "
        f"Согласует {approver_name or 'руководитель'}, затем HR-администратор. Проверь и нажми «Отправить».{notice} "
        f"Если отсутствие влияет на задачи, предупреди наставника{(' (' + mentor + ')') if mentor else ''} — могу "
        f"подготовить сообщение."
    )
    clash = _plan_clashes(turn, rng)
    if clash:
        yield from say(f"\n\n{clash}")
    yield Event("action_proposal", proposal)


def _sentences(problems: list[str]) -> str:
    """«a; b» → «A. B.»: problems from the service read as sentences (feedback 12.13)."""
    out = [p.strip().rstrip(".") for p in problems if p.strip()]
    return " ".join(f"{p[0].upper()}{p[1:]}." for p in out)


def _plan_clashes(turn: Turn, rng: DateRange) -> str:
    """Feedback 12.13: plan tasks due within the absence — say so before the request goes out."""
    hit = [
        t
        for t in turn.user.tasks
        if t.due_at
        and t.status != "done"
        and not t.parent_task_id
        and rng.start.isoformat() <= t.due_at[:10] <= rng.end.isoformat()
    ]
    if not hit:
        return ""
    names = "; ".join(f"«{t.title}» (до {date.fromisoformat(t.due_at[:10]):%d.%m})" for t in hit[:3])
    return f"Обрати внимание: в эти даты срок задач плана — {names}. Обсуди с наставником, перенести срок или закрыть раньше."


def handle_withdraw(turn: Turn, text: str) -> Iterator[Event]:
    """«отмени заявку на отгул» (feedback 12.13): a request the HR admin has not approved can be withdrawn."""
    yield status("calling_service", "Смотрю заявки в сервисе отсутствий")
    reqs = items(turn.gateway.read("absence.list_my_requests"))
    m = ABS_ID.search(text)
    kind = detect_type(text) or ("day_off" if re.search(r"о+тгул", normalize(text)) else None)
    if m:
        cands = [r for r in reqs if r["id"].upper() == f"ABS-{m.group(1)}"]
    else:
        cands = [r for r in reqs if r["status"] in OPEN and kind in (None, r.get("type", "day_off"))]
    if not cands:
        yield from say(
            "Не нашёл заявку, которую можно отменить: отменить можно заявку, которую HR-администратор ещё не согласовал. "
            "Напиши «мои заявки», чтобы посмотреть статусы."
        )
        return
    if len(cands) > 1:
        lines = "\n".join(
            f"- {r['id']}: {type_label(r.get('type', 'day_off')).lower()} {_period(r)}" for r in cands[:5]
        )
        yield from say(f"Какую заявку отменить? Напиши номер, например «отмени заявку {cands[0]['id']}»:\n{lines}")
        return
    target = cands[0]
    t_label = type_label(target.get("type", "day_off"))
    if target["status"] not in OPEN:
        yield from say(
            f"Заявку {target['id']} уже нельзя отменить через меня: {STATUS_LABELS.get(target['status'], target['status']).lower()}. "
            "Согласованную заявку отменяет HR-администратор — напиши ему."
        )
        return
    display = {
        "ref_id": target["id"],
        "type": target.get("type", "day_off"),
        "type_label": t_label,
        "period_label": _period(target),
    }
    proposal = turn.make_proposal(
        "absence_cancel",
        "absence.cancel_request",
        {"id": target["id"]},
        f"Отмена заявки {target['id']}",
        f"Отмена: {t_label.lower()} {_period(target)}",
        display,
        requires_approval=False,
    )
    yield from say(
        f"Подготовил отмену заявки {target['id']}: {t_label.lower()} {_period(target)}. Согласование у руководителя "
        "и HR-администратора тоже отменится. Нажми «Отправить»."
    )
    yield Event("action_proposal", proposal)


def handle_reschedule(turn: Turn, text: str) -> Iterator[Event]:
    """«перенеси отпуск на 8–21 декабря»: a request not yet approved by the HR admin gets new dates (checkpoint 11.11)."""
    yield status("calling_service", "Смотрю заявки в сервисе отсутствий")
    reqs = items(turn.gateway.read("absence.list_my_requests"))
    open_reqs = [r for r in reqs if r["status"] in ("pending_approval", "pending_hr")]
    m = ABS_ID.search(text)
    kind = detect_type(text)
    if m:
        target = next((r for r in reqs if r["id"].upper() == f"ABS-{m.group(1)}"), None)
    else:
        same_kind = [r for r in open_reqs if kind in (None, r.get("type", "day_off"))]
        target = same_kind[-1] if same_kind else None
    if target is None:
        yield from say(
            "Не нашёл заявку, которую можно перенести. Перенести можно заявку, которую HR-администратор ещё не "
            "согласовал. Напиши «мои заявки», чтобы посмотреть статусы."
        )
        return
    if target["status"] not in ("pending_approval", "pending_hr"):
        yield from say(
            f"Заявку {target['id']} уже нельзя перенести: {STATUS_LABELS.get(target['status'], target['status']).lower()}. "
            "Перенести можно только до согласования HR-администратором — оформи новую заявку на нужные даты."
        )
        return
    t_kind = target.get("type", "day_off")
    rng = parse_range(normalize(text), clock.today())
    if rng is None:
        yield from say(
            f"На какие даты перенести заявку {target['id']}? Выбери их в календаре ниже — после переноса заявку снова "
            "согласуют руководитель и HR-администратор."
        )
        yield date_picker(
            t_kind,
            "reschedule",
            target["id"],
            busy=busy_ranges(turn),
            label=f"Новые даты: {type_label(t_kind).lower()} {target['id']}",
        )
        return
    args = {"id": target["id"], "start_date": rng.start.isoformat(), "end_date": rng.end.isoformat()}
    check = turn.gateway.read(
        "absence.validate_request",
        type=t_kind,
        start_date=args["start_date"],
        end_date=args["end_date"],
        documents=target.get("documents") or [],
        exclude_id=target["id"],
    )
    label = type_label(t_kind)
    if not check.get("valid"):
        yield from say(
            f"Перенести {label.lower()} на {human_range(rng)} не получится: {_sentences(check.get('problems', []))} "
            "Выбери другие даты."
        )
        return
    old = DateRange(date.fromisoformat(target["start_date"]), date.fromisoformat(target["end_date"]))
    display = {
        "ref_id": target["id"],
        "type": t_kind,
        "type_label": label,
        "old_period_label": human_range(old),
        "period_label": human_range(rng),
        "approver_display_name": target.get("approver_display_name", ""),
    }
    proposal = turn.make_proposal(
        "absence_reschedule",
        "absence.reschedule_request",
        args,
        f"Перенос заявки {target['id']}",
        f"{label}: {human_range(old)} → {human_range(rng)}",
        display,
        requires_approval=True,
    )
    yield from say(
        f"Подготовил перенос заявки {target['id']}: {label.lower()} {human_range(old)} → {human_range(rng)}. "
        "После переноса заявку снова согласует руководитель, затем HR-администратор. Нажми «Отправить»."
    )
    yield Event("action_proposal", proposal)


def _period(r: dict[str, Any]) -> str:
    return human_range(DateRange(date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"])))


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    label = type_label(r.get("type", "day_off"))
    return {
        "external_id": r["id"],
        "status": r["status"],
        "status_label": STATUS_LABELS.get(r["status"], r["status"]),
        "approver_display_name": r.get("approver_display_name", ""),
        "summary": f"{label} {_period(r)}",
        "followup": "Заявка отправлена на согласование. Сначала решение примет руководитель, затем HR-администратор — "
        "статус обновится здесь, а HR BP получит уведомление. Пока HR-администратор не согласовал, даты можно "
        "перенести. Не забудь лично предупредить наставника, если отсутствие влияет на задачи.",
    }


def reschedule_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    label = type_label(r.get("type", "day_off"))
    return {
        "external_id": r["id"],
        "status": r["status"],
        "status_label": STATUS_LABELS.get(r["status"], r["status"]),
        "approver_display_name": r.get("approver_display_name", ""),
        "summary": f"{label} {_period(r)} (перенос)",
        "followup": "Даты перенесены. Заявку снова согласует руководитель, затем HR-администратор.",
    }


def cancel_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    label = type_label(r.get("type", "day_off"))
    return {
        "external_id": r["id"],
        "status": r["status"],
        "status_label": STATUS_LABELS.get(r["status"], "Отменена"),
        "approver_display_name": r.get("approver_display_name", ""),
        "summary": f"{label} {_period(r)} (отменена)",
        "followup": "Заявка отменена, согласование у руководителя и HR-администратора тоже. Задача ушла из плана.",
    }
