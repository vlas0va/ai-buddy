"""S2 «Спроси buddy» and conversational guard rails: grounded answers with sources and the test badge, «к кому идти»,
escalation drafts, «remind, don't replace the mentor», refusals. Specs: ask-buddy (S2-xx), rights-guardrails (RG-xx)."""

from __future__ import annotations

import re
from collections.abc import Iterator
from typing import Any

from ..errors import AgentError
from ..retrieval import TEST_BADGE, sanitize_snippet
from ..sse import Event, status
from ..text import STOPWORDS, canonical_query, normalize, stems, words
from ..websearch import is_general_tech
from .coach import mask_secrets
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


# Questions buddy could not answer go to the mentor's stack (organizers 13.11): the mentor answers them in a batch when
# convenient and the answer lands in the knowledge base. The intern writes the person directly, buddy only gives the text.
QUEUE_ROLES = ("mentor", "lead", "hrbp")


def escalation_event(
    turn: Turn, question: str, role: str, topic: str = "question", kb_found: bool = False, queue: bool = False
) -> Event:
    person = turn.contact(role) or (turn.contact("mentor") if role != "mentor" else None)
    if person is None:
        role = "mentor"
    hello = f"{first_name(person)}, привет!" if person else "Привет!"
    q = question.strip().rstrip("?") + "?" if question.strip() else "Можешь подсказать?"
    data: dict[str, Any] = {
        "to_role": role,
        "to_user_id": (person or {}).get("user_id"),
        "to_name": person_name(person),
        "channel": (person or {}).get("channel", ""),
        "topic": topic,
        "text": f"{hello} Подскажи, пожалуйста: {q} "
        + (
            "В базе знаний есть общий ответ, но нужно твоё уточнение."
            if kb_found
            else "В базе знаний команды ответа не нашёл."
        ),
    }
    if queue and queues(turn, role):
        data["queue"] = True
        data["question"] = q
    return Event("escalation_draft", data)


def queues(turn: Turn, role: str) -> bool:
    """The question goes to the addressee's stack: interns' questions to the mentor, the lead or HR BP."""
    return turn.user.role == "intern" and role in QUEUE_ROLES


def write_directly(turn: Turn, role: str) -> str:
    """«Спроси наставника в личных сообщениях или в канале #… — ниже готовый текст» (+ the stack, for interns)."""
    gen = GENITIVE.get(role, "наставника")
    person = turn.contact(role)
    name = f" ({person_name(person)})" if person else ""
    channel = (person or {}).get("channel", "")
    where = f"в личных сообщениях или в канале {channel}" if channel else "в личных сообщениях"
    out = f"Спроси {gen}{name} {where} — ниже готовый текст, его можно скопировать."
    if queues(turn, role):
        out += (
            f" Я добавил вопрос в список вопросов для {gen}: когда он ответит, ответ появится в «Мои вопросы» и в базе "
            "знаний, и в следующий раз я отвечу сам."
        )
    return out


ASK_MENTOR = re.compile(r"(уточни|спроси|обратись|напиши)\w*\s+(\w+\s+){0,2}(у\s+|к\s+)?наставник", re.I)
# a polite sign-off («Если что-то не учтено — уточни у наставника») is not a gap in the answer (Rita 12.13)
SIGN_OFF = re.compile(r"^\s*(если|при\s+необходимости|в\s+случае)\b", re.I)


def sends_to_mentor(answer: str) -> bool:
    """The answer tells the intern to ask the mentor something concrete, not just «if anything — ask the mentor»."""
    for sentence in re.split(r"(?<=[.!?])\s+|\n+", answer):
        if ASK_MENTOR.search(sentence) and not SIGN_OFF.search(sentence):
            return True
    return False


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
        # the LLM gets the whole readable document (not only the snippet), so A-Vibe answers with its details
        body = sanitize_snippet(doc.body[:2500]) if doc is not None else ""
        if doc is None and str(h.get("doc_id", "")).startswith("faq-"):
            # a mentor's answer (mcp-kb only): the search snippet is a cut fragment — read the document and show the answer
            try:
                full = turn.gateway.read("kb.get", doc_id=h["doc_id"])
            except AgentError:
                full = None
            if isinstance(full, dict) and full.get("body"):
                body = sanitize_snippet(str(full["body"])[:2500])
                answer = str(full["body"]).split("Ответ:", 1)[-1].strip()
                snippet = answer or snippet
        coverage = kb.coverage(query, doc.doc_id) if doc is not None and query else 0.0
        out.append(
            {
                **h,
                "snippet": sanitize_snippet(snippet),
                "score": score,
                "checklist": checklist,
                "body": body,
                "coverage": coverage,
            }
        )
    return sorted(out, key=lambda h: h["score"], reverse=True)


def _remote_kb(turn: Turn) -> bool:
    backend = getattr(turn.gateway, "backends", {}).get("kb")
    return backend is not None and backend.__class__.__name__ != "InProcessServer"


def _or_query(text: str) -> str:
    """mcp-kb uses websearch_to_tsquery (all words must match): the fallback asks for any meaningful word."""
    return " or ".join(w for w in words(text) if len(w) > 2 and w not in STOPWORDS)[:500]


CYRILLIC_WORD = re.compile(r"^[а-яё]+$")


def _unknown_terms(turn: Turn, text: str) -> list[str]:
    """Question words that no KB document and no training question has (e.g. «собака», «стейдж»): the KB says nothing
    about that topic, so a document matching only the other words is not an answer."""
    kb = turn.kb
    if kb is None or getattr(kb, "_bm25", None) is None:
        return []
    known = kb._bm25.idf.keys() | getattr(kb, "extra_vocab", set())
    return [t for t in stems(text) if len(t) >= 4 and CYRILLIC_WORD.match(t) and t not in known and t not in STOPWORDS]


def _faq_matches(turn: Turn, text: str, hit: dict[str, Any]) -> bool:
    """A mentor's answer is for the question it was written for: mcp-kb's «any word» fallback finds it for other
    questions too («в офис с ноутбуком?» → the answer about dogs). It counts when the question's words are in it (85%)
    and every word the KB does not know (a dog breed, a service name) is there."""
    q = [t for t in stems(text) if t not in STOPWORDS and len(t) >= 3]
    if not q:
        return False
    have = set(stems(f"{hit.get('title', '')} {hit.get('body') or hit.get('snippet', '')}"))
    rare = set(_unknown_terms(turn, text))
    return all(t in have for t in rare) and sum(t in have for t in q) / len(q) >= 0.85


def _weak(turn: Turn, hits: list[dict[str, Any]], text: str) -> bool:
    return not hits or max(turn.kb.coverage(text, h["doc_id"]) for h in hits) < 0.5


def _nearby(turn: Turn, text: str) -> list[Any]:
    """Documents that share some meaningful words with the question but fail the answer threshold: offered as «maybe
    this», never quoted as an answer. Same ACL as search (readable_by before ranking)."""
    if turn.kb is None:
        return []
    found = turn.kb.search(text, turn.user.role, turn.user.team_id, k=2)
    return [h.doc for h in found if turn.kb.coverage(text, h.doc.doc_id) >= 0.2]


def handle_question(turn: Turn, text: str) -> Iterator[Event]:
    yield status("searching_kb")
    remote = _remote_kb(turn)
    min_score = float(turn.config.retrieval.get("min_score_remote" if remote else "min_score", 0.0 if remote else 1.2))
    k = int(turn.config.retrieval.get("k", 4))
    asked = text
    text = canonical_query(text)  # «ноут», «репа», «пр» → the words the KB is written with
    raw = items(turn.gateway.read("kb.search", query=text[:500], k=k))
    if remote and not raw and _or_query(text):
        raw = items(turn.gateway.read("kb.search", query=_or_query(text), k=k))
    hits = [h for h in _rerank(turn, raw, text) if h["score"] >= min_score]
    if remote and turn.kb is not None:
        # Postgres FTS misses short terms (CI/CD), synonyms and loose wording («у меня будет свой ноутбук?»); the agent's
        # own copy of the same test KB, filtered by the same ACL before ranking (ADR-003 layer 1), gives the BM25 ranking
        # used in eval. The set whose best document covers more of the question wins.
        local = [
            {**hit.source(), "score": hit.score, "snippet": hit.snippet}
            for hit in turn.kb.search(text, turn.user.role, turn.user.team_id, k=k)
        ]
        local_hits = [
            h for h in _rerank(turn, local, text) if h["score"] >= float(turn.config.retrieval.get("min_score", 1.2))
        ]
        # a mentor's answer added to the KB lives only in mcp-kb (faq-…): it answers the question it was written for, so
        # it goes first; otherwise the local ranking (the one eval checks) wins
        faq = [
            h
            for h in hits
            if turn.kb.by_id.get(h.get("doc_id")) is None
            and str(h.get("doc_id", "")).startswith("faq-")
            and _faq_matches(turn, text, h)
        ]
        if faq:
            hits = faq + [h for h in local_hits if h["doc_id"] not in {f["doc_id"] for f in faq}]
        elif local_hits:  # the ranking eval checks; mcp-kb stays the source when the local copy finds nothing
            hits = local_hits
        else:  # a mentor's answer to another question is not an answer to this one
            hits = [h for h in hits if not str(h.get("doc_id", "")).startswith("faq-")]
    if hits:  # secondary sources only when they are close to the best one — fewer irrelevant chips
        hits = [h for h in hits if h["score"] >= 0.5 * hits[0]["score"]]
    weak_kb = not hits or (turn.kb is not None and _weak(turn, hits, text))
    if weak_kb and turn.web is not None and turn.web.enabled and is_general_tech(asked):
        found = turn.web.search(mask_secrets(asked)[0][:300])  # only the masked question leaves the agent
        if found:
            yield from _web_answer(turn, asked, found)
            return
    is_faq = bool(hits) and str(hits[0].get("doc_id", "")).startswith("faq-")
    if hits and not is_faq and _unknown_terms(turn, text) and float(hits[0].get("coverage", 1) or 0) < 0.6:
        hits = []  # the documents match only the generic words of the question
    if not hits:
        ranking = turn.meta.get("ranking", [])
        work_words = re.search(
            r"команд|работ|стажир|наставник|задач|проект|офис|сервис|лог|стенд|stage|стейдж|прод|деплой|релиз|"
            r"\bapi\b|[a-z]+-[a-z]+|репозитор|тест",
            normalize(text),
        )
        if not work_words and any(
            lbl == "offtopic" and (i == 0 or prob >= 0.15) for i, (lbl, prob) in enumerate(ranking[:2])
        ):
            yield from handle_offtopic(turn, text)
            return
        # a mentor or a lead asks the curator of the internship, not «the mentor» (QA 12.11)
        role = _addressee(asked, "curator" if turn.user.role in ("mentor", "lead") else "mentor")
        near = _nearby(turn, text)
        if near:
            yield Event(
                "sources",
                {
                    "items": [
                        {"doc_id": d.doc_id, "title": d.title, "source_type": "test", "url": d.url, "badge": TEST_BADGE}
                        for d in near
                    ]
                },
            )
            titles = ", ".join(f"«{d.title}»" for d in near)
            lead = f"Точного ответа в базе знаний нет. Возможно, поможет: {titles} — загляни туда сначала."
        else:
            lead = "В базе знаний команды я не нашёл ответа на этот вопрос."
        yield from say(f"{lead} {write_directly(turn, role)}")
        yield escalation_event(turn, asked, role, queue=True)
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
    answer = []
    for chunk in turn.writer.answer(
        asked, hits[:3], turn.user, task_hint, gateway=turn.gateway, history=turn.history, meta=turn.meta
    ):
        answer.append(chunk)
        yield from (say(chunk) if len(chunk) > 80 else iter([Event("token", {"text": chunk})]))
    if turn.user.role == "intern" and sends_to_mentor("".join(answer)):
        # the answer sends the intern to the mentor: the message to copy, and the question to the mentor's stack so the
        # answer reaches the knowledge base for the next interns (organizers 13.11)
        ask = write_directly(turn, "mentor")
        yield from say(f"\n\nЕсли нужно уточнение — {ask[0].lower()}{ask[1:]}")
        yield escalation_event(turn, asked, "mentor", kb_found=True, queue=True)
    if CURATOR_TOPICS.search(normalize(text)) and re.search(r"документ|справк|подпис|договор", normalize(text)):
        yield from say(
            "\n\nПо документам для вуза лучше сразу написать куратору стажировки — могу подготовить сообщение."
        )


def _web_answer(turn: Turn, asked: str, found: list[Any]) -> Iterator[Event]:
    """General technical question outside the KB: public documentation, clearly marked as an external source (WS-01)."""
    yield status("searching_web", "Ищу в открытых источниках")
    yield Event("sources", {"items": [r.source() for r in found]})
    lines = [
        "Это общий технический вопрос — в базе знаний команды его нет, поэтому посмотрел открытые источники "
        "(это не правила Авито):",
        "",
    ]
    lines += [f"- {r.title}: {sanitize_snippet(r.snippet)}" if r.snippet else f"- {r.title}" for r in found]
    lines += [
        "",
        "Попробуй сначала сам по документации. Если речь о том, как это принято в вашей команде (ветки, CI, ревью), "
        "спроси наставника — могу подготовить вопрос.",
    ]
    yield from say("\n".join(lines))
    turn.meta["web_search"] = len(found)


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
    yield from say(f"Подготовил сообщение. {write_directly(turn, role)}")
    yield escalation_event(turn, question, role, topic="manual", queue=True)
    turn.meta["escalated"] = True


def handle_human_only(turn: Turn, text: str) -> Iterator[Event]:
    if turn.user.role in (
        "mentor",
        "lead",
    ):  # «дай обратную связь стажёру» from a mentor: prepare the 1:1, not a draft to oneself
        from .team import handle_one_on_one

        yield from handle_one_on_one(turn, text)
        return
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
    "trip": "Билеты и гостиницу я не бронирую и не оплачиваю: после согласования командировки это делает служба "
    "командировок. Саму заявку на командировку могу подготовить — напиши город, даты и цель.",
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
    "- ответить на вопросы о команде и инструментах со ссылкой на источник;\n- оформить отгул, отпуск или другое отсутствие и перенести даты («хочу в отпуск с 1 по 14 декабря»);\n"
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
    "hr_admin": "Я AI-buddy. HR-администратору могу: показать заявки на отсутствие, которые уже согласовал руководитель, "
    "и оформить решение («согласуй ABS-000101»), ответить по базе знаний.",
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
