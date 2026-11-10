"""S1 in chat: current stage, statuses, overdue items and the next step; progress summary for the equator;
AI-cron reminder texts. Spec: openspec/specs/plan-assistant/spec.md (S1-01 … S1-10)."""

from __future__ import annotations

from collections.abc import Iterator
from datetime import date
from typing import Any

from .. import clock
from ..auth import PlanTask, User
from ..sse import Event, status
from .common import PERIOD_LABELS, PERIOD_ORDER, Turn, first_name, items, say

CTA = {
    "CL-01": ("ask", "Что запросил стажёр", "Какие доступы запросил мой стажёр?"),
    "CL-02": ("escalate", "Написать куратору", "Подготовь сообщение куратору: запросить методичку наставника"),
    "CL-03": ("ask", "Кто есть кто в команде", "Кто есть кто в моей команде?"),
    "CL-04": ("escalate", "Попросить добавить в каналы", "Какие каналы есть в команде?"),
    "CL-05": ("access", "Запросить доступы", "Какие доступы мне нужны?"),
    "CL-06": ("ask", "Как внести цели", "Как внести цели и ожидания в план стажёра?"),
    "CL-07": ("ask", "Документы команды", "Какие документы команды нужно изучить?"),
    "CL-08": ("ask", "Защита данных: главное", "Что главное в защите персональных данных?"),
    "CL-09": ("ask", "Как закрыть практику", "Как закрыть практику и писать учебные работы?"),
    "CL-10": ("ask", "Как подготовиться к экватору", "Как подготовиться к экваторной встрече?"),
    "CL-11": ("ask", "Сводка для презентации", "Подготовь сводку моих задач для экватора"),
    "CL-12": ("ask", "Сводка прогресса стажёра", "Подготовь сводку прогресса моего стажёра"),
    "CL-13": ("ask", "Структура презентации", "Какая структура презентации на экватор?"),
    "CL-14": ("ask", "Про выпускной", "Что нужно знать про выпускной?"),
    "CL-15": ("ask", "Про опрос", "Где пройти опрос о стажировке?"),
    "CL-16": ("ask", "Про опрос", "Где пройти опрос о стажировке?"),
}


def tasks_or_template(turn: Turn) -> list[PlanTask]:
    if turn.user.tasks:
        return [t for t in turn.user.tasks if not t.parent_task_id]
    try:
        role = "mentor" if turn.user.role in ("mentor", "lead") else "intern"
        tpl = turn.gateway.read("checklist.get_plan_template", role=role)
    except Exception:  # noqa: BLE001
        return []
    return [
        PlanTask(
            task_id=i["id"],
            title=i["title"],
            checklist_item_id=i["id"],
            period=i["period"],
            human_only=bool(i.get("human_only")),
        )
        for i in tpl.get("items", [])
    ]


def is_overdue(t: PlanTask, today: date) -> bool:
    if t.status == "overdue":
        return True
    return bool(t.due_at and t.status != "done" and date.fromisoformat(t.due_at[:10]) < today)


def current_period(user: User, tasks: list[PlanTask]) -> str:
    if user.stage in PERIOD_ORDER:
        return user.stage  # type: ignore[return-value]
    for p in PERIOD_ORDER:
        if any(t.period == p and t.status != "done" for t in tasks):
            return p
    return PERIOD_ORDER[-1]


def next_task(tasks: list[PlanTask], period: str, today: date) -> PlanTask | None:
    open_tasks = [t for t in tasks if t.status != "done"]
    overdue = [t for t in open_tasks if is_overdue(t, today)]
    if overdue:
        return overdue[0]
    order = {p: i for i, p in enumerate(PERIOD_ORDER)}
    cur = order.get(period, 0)
    candidates = sorted(
        (t for t in open_tasks if order.get(t.period or "", 99) <= cur),
        key=lambda t: (0 if t.status == "in_progress" else 1, t.human_only, order.get(t.period or "", 99)),
    )
    if candidates:
        return candidates[0]
    later = sorted(open_tasks, key=lambda t: order.get(t.period or "", 99))
    return later[0] if later else None


def next_step_event(t: PlanTask) -> Event:
    kind, label, message = CTA.get(t.checklist_item_id or "", ("ask", "Спросить buddy", f"Как выполнить: {t.title}?"))
    return Event(
        "next_step",
        {
            "task_id": t.task_id,
            "checklist_item_id": t.checklist_item_id,
            "title": t.title,
            "period": t.period,
            "status": t.status,
            "human_only": t.human_only,
            "cta": {"kind": kind, "label": label, "message": message},
        },
    )


def handle(turn: Turn, text: str) -> Iterator[Event]:
    if turn.user.role == "alumni":
        yield from say(
            "У тебя уже нет плана адаптации — стажировка завершена. Но я по-прежнему помогаю с рабочими "
            "вопросами: найти документ команды, узнать порядок отгула или понять, к кому обратиться."
        )
        return
    if turn.user.role == "lead":
        yield from _lead_overview(turn)
        return
    yield status("thinking", "Смотрю твой план адаптации")
    today = clock.today()
    tasks = tasks_or_template(turn)
    if not tasks:
        yield from say("План адаптации пока пуст — его собирает наставник. Попробуй позже или спроси наставника.")
        return
    period = current_period(turn.user, tasks)
    in_period = [t for t in tasks if t.period == period]
    done = sum(1 for t in in_period if t.status == "done")
    overdue = [t for t in tasks if is_overdue(t, today)]
    lines = [f"Сейчас этап «{PERIOD_LABELS.get(period, period)}»: готово {done} из {len(in_period)} задач."]
    if overdue:
        lines.append("Просрочено: " + "; ".join(f"«{t.title}»" for t in overdue[:3]) + ".")
    nxt = next_task(tasks, period, today)
    if nxt:
        human = " Этот шаг делаешь ты сам вместе с командой — я могу помочь подготовиться." if nxt.human_only else ""
        lines.append(f"Следующий шаг: «{nxt.title}».{human}")
    else:
        lines.append("Все задачи плана выполнены — отличная работа.")
    if turn.user.role == "mentor":
        lines.append(_mentee_line(turn))
    yield from say(" ".join(line for line in lines if line))
    if nxt:
        yield next_step_event(nxt)


def _mentee_line(turn: Turn) -> str:
    parts = []
    for m in turn.user.mentees:
        missing = sum(1 for a in m.get("accesses", []) if a.get("status") == "not_requested")
        parts.append(f"{first_name({'name': m.get('display_name', '')})}: не запрошено доступов — {missing}")
    return ("Твои стажёры: " + "; ".join(parts) + ".") if parts else ""


def _lead_overview(turn: Turn) -> Iterator[Event]:
    yield status("calling_service", "Смотрю согласования")
    pending = items(turn.gateway.read("approvals.list_pending"))
    yield from say(
        f"У тебя на согласовании заявок: {len(pending)}. Цели и ожидания для стажёров вносятся в их план "
        "адаптации (раздел «Цели»). Скажи «покажи заявки на согласование», чтобы разобрать их."
    )


def handle_summary(turn: Turn, text: str) -> Iterator[Event]:
    yield status("composing", "Собираю сводку")
    if turn.user.role == "mentor":
        out = []
        for m in turn.user.mentees:
            acc = m.get("accesses", [])
            got = sum(1 for a in acc if a.get("status") in ("granted", "pending_approval"))
            out.append(
                f"{m.get('display_name', '')}: доступы запрошены или выданы — {got} из {len(acc)}; "
                f"открытых задач плана — {len(m.get('open_tasks') or [])}."
            )
        if not out:
            yield from say("Сводка по стажёрам доступна в разделе «Мои стажёры».")
            return
        yield from say(
            "Сводка по стажёрам (для 1:1 и проверки презентации экватора; обратную связь даёшь ты):\n"
            + "\n".join(f"- {line}" for line in out)
        )
        return
    summary = build_summary(turn.user, tasks_or_template(turn), _safe_actions(turn))
    yield from say(summary["text"])


def _safe_actions(turn: Turn) -> list[dict[str, Any]]:
    actions: list[dict[str, Any]] = []
    for tool in ("absence.list_my_requests", "access.list_my_access"):
        try:
            actions.extend(items(turn.gateway.read(tool)))
        except Exception:  # noqa: BLE001
            continue
    return actions


def build_summary(user: User, tasks: list[PlanTask], actions: list[dict[str, Any]]) -> dict[str, Any]:
    done = [t for t in tasks if t.status == "done"]
    in_progress = [t for t in tasks if t.status == "in_progress"]
    granted = [
        a.get("name") or a.get("resource_name")
        for a in actions
        if a.get("status") == "granted" and (a.get("name") or a.get("resource_name"))
    ]
    bullets = [f"Выполнено задач плана: {len(done)} из {len(tasks)}"]
    bullets += [f"Готово: {t.title}" for t in done[:6]]
    bullets += [f"В работе: {t.title}" for t in in_progress[:4]]
    if granted:
        bullets.append("Получены доступы: " + ", ".join(granted))
    text = (
        "Сводка для презентации на экватор (это заготовка — результаты и выводы сформулируй сам и согласуй с "
        "наставником и куратором):\n" + "\n".join(f"- {b}" for b in bullets)
    )
    return {"text": text, "bullets": bullets, "sources": [{"doc_id": "common-equator-presentation"}]}


def reminder_text(item: dict[str, Any], user_name: str = "") -> dict[str, Any]:
    """Template texts for AI-cron reminders (R54). Human-only items: remind and help prepare, never 'buddy will do it'."""
    title = item.get("title", "")
    reason = item.get("reason", "period_start")
    hello = f"{user_name}, " if user_name else ""
    if item.get("human_only"):
        text = (
            f"{hello}напоминаю про «{title}». Этот шаг проходит вместе с людьми — могу помочь подготовиться: "
            "собрать сводку задач или набросать вопросы."
        )
        kind = "human_only"
    elif reason == "overdue":
        text = f"{hello}задача «{title}» просрочена. Помочь закрыть её сейчас? Напиши мне — подскажу следующий шаг."
        kind = "overdue"
    else:
        text = f"{hello}начинается этап с задачей «{title}». Если нужно — подскажу, с чего начать."
        kind = "period_start"
    return {
        "task_id": item.get("task_id"),
        "checklist_item_id": item.get("checklist_item_id"),
        "text": text,
        "kind": kind,
    }
