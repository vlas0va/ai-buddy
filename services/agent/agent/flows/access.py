"""S4 «Доступы и каналы»: обязательные доступы со статусами, заявка через access MCP (повышенные права → согласование
руководителя), каналы команды с советом. Заявка называет конкретный ресурс (проект Jira, репозиторий Bitbucket, базу
данных), окружение и права (чекпоинт 11.11). Спецификация: openspec/specs/access-and-channels/spec.md (S4-01 … S4-17).
Формы инструментов: contracts/schemas/access.json.
"""

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
    "bitbucket": ["bitbucket", "битбакет", "git", "гит"],
}
KIND_SYNONYMS = {
    "space": ["пространств"],
    "channel": ["канал"],
    "project": ["проект"],
    "repo": ["репозитор", "репо ", "код"],
    "database": ["бд", "базе данных", "база данных", "базу данных", "database", "postgres", "постгрес"],
}
LEVELS = [  # порядок важен: «редактирование» — это запись, «полные права» — администрирование
    ("admin", re.compile(r"админ|admin|полн\w*\s+(права|доступ)|владел")),
    ("write", re.compile(r"запис|write|редакт|измен|пуш|push|мерж|merge|коммит")),
    ("read", re.compile(r"чтени|read|просмотр|смотреть|только\s+чита")),
]
ENVS = [
    ("prod", re.compile(r"\bprod\w*|\bпрод\w*|production|боев")),
    ("stage", re.compile(r"\bstag\w*|\bстейдж\w*|\bстэйдж\w*|\bпредпрод|\bтестов\w+\s+стенд")),
    ("dev", re.compile(r"\bdev\b|\bдев\w*|разработ\w*\s+стенд|dev-стенд")),
]
LEVEL_LABEL = {"read": "чтение", "write": "редактирование", "admin": "администрирование"}
KIND_LABEL = {
    "tool": "сервис",
    "project": "проект Jira",
    "repo": "репозиторий",
    "space": "пространство",
    "database": "база данных",
}
STATUS_LABEL = {
    "granted": "есть",
    "pending_approval": "ждёт согласования руководителя",
    "rejected": "отклонён",
    "not_requested": "не запрошен",
}
RESULT_LABEL = {"granted": "Доступ выдан", "pending_approval": "На согласовании", "rejected": "Отклонено"}


def parse_level(text: str) -> str | None:
    t = normalize(text)
    return next((lvl for lvl, rx in LEVELS if rx.search(t)), None)


def parse_env(text: str) -> str | None:
    t = normalize(text)
    return next((env for env, rx in ENVS if rx.search(t)), None)


def scope_label(env: str, level: str) -> str:
    if env:
        return f" ({env}, {LEVEL_LABEL[level]})"
    return f" ({LEVEL_LABEL[level]})" if level != "read" else ""


def _aliases(r: dict[str, Any]) -> list[str]:
    tail = r["name"].lower().split(":")[-1].strip()  # «проект dlv», «репозиторий delivery-api»
    out = [r["name"].lower(), r["resource_id"].lower(), tail, *SYNONYMS.get(r["resource_id"], [])]
    out += [w for w in re.findall(r"[a-z][a-z0-9_-]{2,}", tail) if w not in ("postgresql",)]
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


# Организаторы 12.11: заявка называет проект, уровень прав и обоснование; прежде чем просить доступ, стажёр может
# уточнить у наставника, нужен ли он вообще.
REASON = re.compile(
    r"(?:обосновани\w*|причин\w*|зачем)\s*[:—–-]?\s*(.+)$|[,—–-]\s*(?:потому\s+что|так\s+как|нужно?\s+для|для\s+задач\w*)\s+(.+)$",
    re.I,
)
ASK_IF_NEEDED = re.compile(
    r"нуж\w+\s+ли\s+(мне\s+)?(\w+\s+){0,2}доступ|спроси\w*\s+(у\s+)?наставник\w*.{0,40}доступ", re.I
)


def parse_reason(text: str) -> str:
    m = REASON.search(text.strip())
    if not m:
        return ""
    reason = (m.group(1) or m.group(2) or "").strip().rstrip(".")
    return reason[:200]


def needs_approval(r: dict[str, Any], env: str, level: str) -> bool:
    """= сервис доступов: ресурсы с повышенными правами, права на запись и администрирование и prod уходят руководителю
    (чекпоинт 11.11).
    """
    return bool(r["elevated"]) or level != "read" or env == "prod"


def _proposal(turn: Turn, r: dict[str, Any], env: str = "", level: str = "read", reason: str = "") -> dict[str, Any]:
    lead = turn.contact("lead") or {}
    reason = reason or (
        "Онбординг стажёра: доступы первой недели (чек-лист)" if turn.user.role == "intern" else "Работа в команде"
    )
    approval = needs_approval(r, env, level)
    display = {
        "resource_name": r["name"],
        "elevated": approval,
        "kind": r.get("kind", ""),
        "environment": env,
        "level": level,
        "level_label": LEVEL_LABEL[level],
        "approver_label": r.get("approver_label", "") if approval else "",
        "approver_display_name": lead.get("name", "") if approval else "",
        "reason": reason,
    }
    args: dict[str, Any] = {"resource_id": r["resource_id"], "reason": reason, "level": level}
    if env:
        args["environment"] = env
    title = "Заявка на доступ с согласованием" if approval else "Заявка на доступ"
    return turn.make_proposal(
        "access_request",
        "access.request_access",
        args,
        title,
        f"Доступ: {r['name']}{scope_label(env, level)}",
        display,
        requires_approval=approval,
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

    if target is not None and ASK_IF_NEEDED.search(text):
        person = turn.contact("mentor")
        mentor = person_name(person, "наставник")
        yield from say(
            f"Хороший вопрос до заявки: лишние доступы запрашивать не стоит. Ниже текст вопроса наставнику ({mentor}) — "
            "скопируй его в личные сообщения. Если доступ нужен, напиши, например: "
            f"«запроси доступ к {target['name']}, обоснование: задача из плана»."
        )
        hello = f"{first_name(person)}, привет!" if person else "Привет!"
        yield Event(
            "escalation_draft",
            {
                "to_role": "mentor",
                "to_user_id": (person or {}).get("user_id"),
                "to_name": mentor,
                "topic": "access",
                "text": f"{hello} Нужен ли мне доступ «{target['name']}»? Если да — с какими правами и для каких задач, "
                "чтобы я указал это в обосновании заявки.",
            },
        )
        return

    if target is not None:
        envs, levels = target.get("environments") or [], target.get("levels") or ["read"]
        level = parse_level(text) or "read"
        env = parse_env(text) or ""
        note = ""
        if envs and not env:
            env, note = envs[0], f" Окружение не указано — взял {envs[0]}; для другого напиши, например, «… на stage»."
        if env and not envs:
            env, note = "", " У этого ресурса нет окружений — окружение не указываю."
        if level not in levels:
            yield from say(
                f"Для ресурса «{target['name']}» можно запросить: {', '.join(LEVEL_LABEL[x] for x in levels)}. "
                "Уточни, какие права нужны."
            )
            return
        same_scope = target.get("level", "read") == level and target.get("environment", "") == env
        if target["status"] == "pending_approval" or (target["status"] == "granted" and same_scope):
            yield from say(f"{target['name']}: {STATUS_LABEL[target['status']]}. Повторная заявка не нужна.")
            return
        reason = parse_reason(text)
        proposal = _proposal(turn, target, env, level, reason)
        who = target.get("approver_label") or "руководитель"
        approval = (
            f" Это повышенные права — согласует {who} ({person_name(turn.contact('lead'), 'руководитель')})."
            + (
                f" Обоснование: «{reason}»."
                if reason
                else " Добавь обоснование, чтобы согласующему было понятно, зачем доступ: например, "
                f"«доступ к {target['name']}, обоснование: задача из плана»."
            )
            if needs_approval(target, env, level)
            else " Чтение выдаётся сразу после заявки."
        )
        yield from say(
            f"Подготовил заявку на доступ: {target['name']}{scope_label(env, level)}.{approval}{note} "
            "Нажми «Отправить», чтобы создать её."
        )
        yield Event("action_proposal", proposal)
        return

    if REQUEST_VERB.search(t) and re.search(r"доступ\w*\s+(к|в|на)\s+\w+", t) and not ALL_WORDS.search(t):
        names = ", ".join(r["name"] for r in required)
        yield from say(
            f"Не нашёл такой ресурс среди доступов твоей команды. Доступные: {names}. Если нужен другой — "
            "уточни у наставника."
        )
        return

    rows, missing = [], []
    for r in required:
        envs = ", ".join(r.get("environments") or []) or "—"
        levels = ", ".join(LEVEL_LABEL.get(x, x) for x in r.get("levels") or ["read"])
        st = STATUS_LABEL.get(r["status"], r["status"])
        if r.get("optional") and r["status"] == "not_requested":
            st = "по запросу"
        who = "согласование: " + (r.get("approver_label") or "руководитель") if r["elevated"] else "чтение — сразу"
        rows.append(f"| {r['name']} | {envs} | {levels} | {st} | {who} |")
        if r["status"] == "not_requested" and not r.get("optional"):
            missing.append(r)  # с повышенными правами тоже: чат показывает один список с галочками (отзыв 12.11)
    intro = "Для работы в команде нужны такие доступы:" if turn.user.role == "intern" else "Доступы команды:"
    table = "| Ресурс | Окружения | Права | Статус | Кто согласует |\n|---|---|---|---|---|\n" + "\n".join(rows)
    rules = (
        "\n\nЧтение выдаётся сразу. Редактирование, администрирование и любой доступ к prod согласует руководитель. "
        "Пример: «доступ на редактирование в репозиторий delivery-api» или «доступ к БД на stage на чтение»."
    )
    approval = [r for r in missing if r["elevated"]]
    tail = (
        f"\n\nНиже список недостающих доступов ({len(missing)}): отметь галочками нужные и нажми «Запросить выбранные». "
        "Чтение выдаётся сразу"
        + (", а доступы с согласованием уйдут руководителю — они по умолчанию не отмечены." if approval else ".")
        if missing
        else "\n\nВсе нужные доступы уже есть или запрошены."
    )
    yield from say(f"{intro}\n\n{table}{rules}{tail}")
    for r in missing[:8]:
        yield Event("action_proposal", _proposal(turn, r))


def handle_channels(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю каналы команды")
    channels = items(turn.gateway.read("access.list_team_channels"))
    lines = [f"- {c['name']} — {c.get('purpose', '')}{' (закрытый)' if c.get('closed') else ''}" for c in channels]
    yield from say("Каналы и чаты команды:\n" + "\n".join(lines))
    if turn.user.role != "intern":
        return
    mentor = turn.contact("mentor")
    who = f" ({person_name(mentor)})" if mentor else ""
    # чекпоинт 11.11: о добавлении в чаты просят в личном сообщении, а не через buddy — только совет, без черновика
    yield from say(
        "\n\nОткрытые каналы найди по названию в мессенджере и вступи сам. В закрытые добавляет наставник"
        f"{who} — напиши ему в личные сообщения, какие каналы нужны."
    )


def result_card(r: dict[str, Any], contacts: dict[str, Any]) -> dict[str, Any]:
    granted = r["status"] == "granted"
    lead = contacts.get("lead") or {}
    return {
        "external_id": r["id"],
        "status": r["status"],
        "status_label": RESULT_LABEL.get(r["status"], r["status"]),
        "approver_display_name": "" if granted else lead.get("name", ""),
        "summary": f"Доступ: {r.get('resource_name') or r['resource_id']}"
        + scope_label(r.get("environment") or "", r.get("level") or "read"),
        "followup": "Доступ выдан — можно пользоваться."
        if granted
        else "Заявка отправлена руководителю на согласование. Статус обновится здесь после решения.",
    }
