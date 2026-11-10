"""S2 «Спроси buddy» and conversational guard rails: grounded answers with sources and the test badge, «к кому идти»,
escalation drafts, «remind, don't replace the mentor», refusals. Specs: ask-buddy (S2-xx), rights-guardrails (RG-xx)."""

from __future__ import annotations

import re
from collections.abc import Iterator
from typing import Any

from ..retrieval import TEST_BADGE, sanitize_snippet
from ..sse import Event, status
from ..text import normalize, stems
from .common import CONTACT_LABELS, Turn, first_name, items, person_name, say

CURATOR_TOPICS = re.compile(r"практик|вуз|учебн|диплом|курсов|экватор|выпускн|методичк|опрос")
HR_TOPICS = re.compile(r"кадр|\bhr\b|больнич|справк|трудов|договор")
LEAD_TOPICS = re.compile(r"цел[ьи]|okr|окр|ожидани|оценк|испытательн")
GENITIVE = {"mentor": "наставника", "lead": "руководителя", "curator": "куратора стажировки", "hrbp": "HR BP"}
ROLE_WORDS = [
    ("mentor", re.compile(r"наставник|бадди|buddy|олег")),
    ("lead", re.compile(r"руководител|лид|марии|мария")),
    ("curator", re.compile(r"куратор")),
    ("hrbp", re.compile(r"\bhr\b|hr\s*bp|эйчар|кадр")),
]


def _addressee(text: str, default: str = "mentor") -> str:
    t = normalize(text)
    for role, rx in ROLE_WORDS:
        if rx.search(t):
            return role
    if CURATOR_TOPICS.search(t):
        return "curator"
    if HR_TOPICS.search(t):
        return "hrbp"
    if LEAD_TOPICS.search(t):
        return "lead"
    return default


def escalation_event(turn: Turn, question: str, role: str, topic: str = "question") -> Event:
    person = turn.contact(role) or (turn.contact("mentor") if role != "mentor" else None)
    if person is None:
        role = "mentor"
    hello = f"{first_name(person)}, привет!" if person else "Привет!"
    q = question.strip().rstrip("?") + "?" if question.strip() else "Можешь подсказать?"
    return Event(
        "escalation_draft",
        {
            "to_role": role,
            "to_user_id": (person or {}).get("user_id"),
            "to_name": person_name(person),
            "topic": topic,
            "text": f"{hello} Подскажи, пожалуйста: {q} Не нашёл ответа в базе знаний команды.",
        },
    )


CITY_WORDS = re.compile(r"москв|петербург|питер|спб|удал[её]н|казан|новосибирск|екатеринбург")


def _location_hint(turn: Turn, query: str) -> str:
    if not turn.user.location or CITY_WORDS.search(normalize(query)):
        return ""
    return turn.user.location


def _rerank(turn: Turn, raw: list[dict[str, Any]], query: str = "") -> list[dict[str, Any]]:
    """Defense in depth + context (ADR-003 layer 1, R02, R15): drop results the user may not read according to the
    agent's own copy of the KB ACL, then boost documents of the user's team and of open plan tasks."""
    kb = turn.kb
    open_items = {t.checklist_item_id for t in turn.user.tasks if t.status != "done" and t.checklist_item_id}
    team_boost = float(turn.config.retrieval.get("team_boost", 1.25))
    stage_boost = float(turn.config.retrieval.get("stage_boost", 1.15))
    min_coverage = float(turn.config.retrieval.get("min_coverage", 0.25))
    out = []
    for h in raw:
        if h.get("source_type", "test") != "test":
            continue
        doc = kb.by_id.get(h.get("doc_id")) if kb is not None else None
        score = float(h.get("score", 0) or 0)
        checklist: list[str] = []
        if doc is not None:
            if not doc.readable_by(turn.user.role, turn.user.team_id):
                continue
            if query and kb.coverage(query, doc.doc_id) < min_coverage:
                continue
            checklist = doc.checklist
            if turn.user.team_id in doc.teams:
                score *= team_boost
            if open_items.intersection(checklist):
                score *= stage_boost
        snippet = h.get("snippet", "")
        if doc is not None and query:
            # the local copy gives clean paragraphs (no ts_headline fragments or markdown) and lets the user's office
            # city pick the right section («Москва» / «Санкт-Петербург» / «Удалённо») when the question has none
            snippet = kb.snippet(doc, stems(f"{query} {_location_hint(turn, query)}"))
        out.append({**h, "snippet": sanitize_snippet(snippet), "score": score, "checklist": checklist})
    return sorted(out, key=lambda h: h["score"], reverse=True)


def handle_question(turn: Turn, text: str) -> Iterator[Event]:
    yield status("searching_kb")
    min_score = float(turn.config.retrieval.get("min_score", 1.2))
    raw = items(turn.gateway.read("kb.search", query=text[:500], k=int(turn.config.retrieval.get("k", 4))))
    hits = [h for h in _rerank(turn, raw, text) if h["score"] >= min_score]
    if hits:  # secondary sources only when they are close to the best one — fewer irrelevant chips
        hits = [h for h in hits if h["score"] >= 0.5 * hits[0]["score"]]
    if not hits:
        ranking = turn.meta.get("ranking", [])
        work_words = re.search(r"команд|работ|стажир|наставник|задач|проект|офис|сервис", normalize(text))
        if not work_words and any(
            lbl == "offtopic" and (i == 0 or prob >= 0.15) for i, (lbl, prob) in enumerate(ranking[:2])
        ):
            yield from handle_offtopic(turn, text)
            return
        role = _addressee(text)
        label = CONTACT_LABELS.get(role, "наставник")
        yield from say(
            f"В базе знаний команды я не нашёл ответа на этот вопрос. Лучше уточнить у человека — "
            f"подготовил черновик сообщения ({label}), можешь отредактировать и отправить."
        )
        yield escalation_event(turn, text, role)
        turn.meta["escalated"] = True
        return
    yield Event(
        "sources",
        {
            "items": [
                {
                    "doc_id": h["doc_id"],
                    "title": h["title"],
                    "source_type": "test",
                    "url": h.get("url", ""),
                    "badge": TEST_BADGE,
                }
                for h in hits[:3]
            ]
        },
    )
    task_hint = ""
    related = set(hits[0].get("checklist", []) or [])
    for t in turn.user.tasks:
        if t.status != "done" and t.checklist_item_id in related:
            task_hint = f"Это относится к задаче твоего плана «{t.title}»."
            break
    yield status("composing", "Формулирую ответ")
    for chunk in turn.writer.answer(
        text, hits[:3], turn.user, task_hint, gateway=turn.gateway, history=turn.history, meta=turn.meta
    ):
        yield from (say(chunk) if len(chunk) > 80 else iter([Event("token", {"text": chunk})]))
    if CURATOR_TOPICS.search(normalize(text)) and re.search(r"документ|справк|подпис|договор", normalize(text)):
        yield from say(
            "\n\nПо документам для вуза лучше сразу написать куратору стажировки — могу подготовить сообщение."
        )


def handle_whois(turn: Turn, text: str) -> Iterator[Event]:
    yield status("calling_service", "Смотрю справочник")
    contacts = turn.contacts()
    t = normalize(text)
    wanted = [r for r, rx in ROLE_WORDS if rx.search(t)] or ["mentor", "lead", "curator", "hrbp"]
    lines = []
    for role in wanted:
        p = contacts.get(role)
        if p:
            about = f" — {', '.join(p['ask_about'])}" if p.get("ask_about") else ""
            channel = f" ({p['channel']})" if p.get("channel") else ""
            lines.append(f"- {CONTACT_LABELS[role].capitalize()}: {p['name']}{channel}{about}")
    if not lines:
        yield from say("В справочнике не нашёл такого контакта. Спроси наставника или руководителя.")
        return
    yield from say("Твои контакты:\n" + "\n".join(lines))


def handle_escalate(turn: Turn, text: str) -> Iterator[Event]:
    role = _addressee(text)
    question = re.sub(
        r"^(пожалуйста\s+)?(спроси|передай|напиши|уточни)\w*\s+(\w+\s+)?(у\s+)?(наставник\w*|руководител\w*|куратор\w*|hr\w*)[,:]?\s*",
        "",
        text.strip(),
        flags=re.IGNORECASE,
    )
    if len(question) < 8:
        last = next((h["text"] for h in reversed(turn.history) if h.get("role") == "user"), "")
        question = last or question
    yield from say(f"Подготовил черновик для {GENITIVE.get(role, 'наставника')} — проверь и отправь, если всё верно.")
    yield escalation_event(turn, question, role, topic="manual")
    turn.meta["escalated"] = True


def handle_human_only(turn: Turn, text: str) -> Iterator[Event]:
    mentor = turn.contact("mentor")
    who = f" ({person_name(mentor)})" if mentor else ""
    yield from say(
        f"Это лучше сделать вместе с наставником{who}: обратную связь, "
        "встречи 1:1, оценку прогресса и экватор проводят люди. Я могу помочь подготовиться — собрать сводку "
        "твоих задач («подготовь сводку для экватора») или набросать вопросы к встрече. Ниже черновик просьбы о встрече."
    )
    hello = f"{first_name(mentor)}, привет!" if mentor else "Привет!"
    yield Event(
        "escalation_draft",
        {
            "to_role": "mentor",
            "to_user_id": (mentor or {}).get("user_id"),
            "to_name": person_name(mentor),
            "topic": "one_on_one",
            "text": f"{hello} Можем на ближайшей 1:1 обсудить мой прогресс и что стоит улучшить? Спасибо!",
        },
    )


REFUSAL_TEXT = {
    "prompt_injection": "Я не меняю свои правила и не выполняю служебные команды. Могу помочь с вопросами о команде, "
    "плане адаптации, отгулах и доступах.",
    "no_rights": "Я показываю и оформляю только твои собственные заявки и данные. Сведения о других сотрудниках не раскрываю "
    "и действия за них не выполняю.",
    "sensitive_data": "Вопросы о зарплате, выплатах, компенсациях, медицинских и персональных документах я не обсуждаю.",
    "out_of_scope": "Это вне моих задач.",
}
OUT_OF_SCOPE_TEXT = {
    "hr": "Найм и увольнения не входят в мои задачи — это кадровые вопросы.",
    "trip": "Командировки я не оформляю: решение принимает руководитель, оформление идёт через сервис командировок.",
    "benefit": "Бенефиты и компенсации я не оформляю — могу дать только общую справку.",
}


CONTRACT_REASON = {
    "no_rights": "no_rights",
    "prompt_injection": "no_rights",
    "sensitive_data": "out_of_scope",
    "out_of_scope": "out_of_scope",
}


def handle_refusal(turn: Turn, reason: str, redirect_role: str = "", topic: str = "") -> Iterator[Event]:
    base = (
        OUT_OF_SCOPE_TEXT.get(topic, REFUSAL_TEXT["out_of_scope"])
        if reason == "out_of_scope"
        else REFUSAL_TEXT.get(reason, "")
    )
    person = turn.contact(redirect_role) if redirect_role else None
    redirect = ""
    if person:
        channel = f" ({person['channel']})" if person.get("channel") else ""
        redirect = f" С этим поможет {CONTACT_LABELS.get(redirect_role, redirect_role)} — {person['name']}{channel}."
    elif redirect_role:
        redirect = f" С этим поможет {CONTACT_LABELS.get(redirect_role, redirect_role)}."
    text = base + redirect
    # contract enum: reason ∈ {no_rights, out_of_scope}; the precise category goes to `detail` (for analytics and eval)
    yield Event(
        "refusal",
        {
            "reason": CONTRACT_REASON.get(reason, "out_of_scope"),
            "detail": topic or reason,
            "redirect_role": redirect_role or None,
            "redirect_name": person_name(person) or None,
            "text": text,
        },
    )
    yield from say(text)


HELP = {
    "intern": "Я AI-buddy — помогаю освоиться в команде. Могу:\n- показать план адаптации и следующий шаг («что дальше?»);\n"
    "- ответить на вопросы о команде и инструментах со ссылкой на источник;\n- оформить отгул («хочу отгул в пятницу»);\n"
    "- запросить доступы (Confluence, Jira, Bitbucket) и подсказать каналы команды;\n"
    "- помочь с инженерным онбордингом: репозиторий, ветки и pull request, CI/CD, секреты, dev-стенд;\n"
    "- подготовить к код-ревью и подсказать, кого позвать («кого позвать на ревью?»);\n"
    "- разобрать ошибку из лога — пришли текст, секреты я скрою;\n"
    "- проверить понимание инструкции («проверь меня по защите данных»);\n"
    "- подсказать, к кому идти, и подготовить сообщение наставнику.\n"
    "Задачи за тебя я не делаю — помогаю разобраться, чтобы ты сделал их сам.",
    "mentor": "Я AI-buddy. Наставнику могу: показать твой план и пункты чек-листа, что запросили твои стажёры, подготовить "
    "сводку прогресса к 1:1 и экватору, ответить по методичке и базе знаний.",
    "lead": "Я AI-buddy. Руководителю могу: показать заявки на согласование и оформить решение («согласуй ABS-1001»), ответить "
    "по базе знаний, напомнить про цели адаптации стажёров.",
    "hrbp": "Я AI-buddy. Могу ответить по базе знаний и подсказать контакты участников стажировки.",
    "alumni": "Я AI-buddy. После стажировки я остаюсь точкой входа для рабочих вопросов: документы команды, отгулы, доступы, "
    "к кому обратиться.",
}


def handle_help(turn: Turn, text: str) -> Iterator[Event]:
    yield from say(HELP.get(turn.user.role, HELP["intern"]))


def handle_smalltalk(turn: Turn, text: str) -> Iterator[Event]:
    t = normalize(text)
    if re.match(r"(спасибо|благодарю)", t):
        yield from say("Пожалуйста! Если появятся вопросы — пиши.")
    elif re.match(r"(пока|до\s+свидания)", t):
        yield from say("Хорошего дня! Я на связи.")
    else:
        name = (turn.user.display_name or "").split(" ")[0]
        yield from say(
            f"Привет{', ' + name if name else ''}! Чем помочь? Например: «что мне делать дальше?», "
            "«как у нас проходит код-ревью?» или «хочу отгул в пятницу»."
        )


def handle_offtopic(turn: Turn, text: str) -> Iterator[Event]:
    yield from say(
        "Я помогаю с адаптацией в команде: план и следующие шаги, вопросы о процессах и инструментах, отгулы, "
        "доступы и контакты. С этим вопросом помочь не смогу."
    )
