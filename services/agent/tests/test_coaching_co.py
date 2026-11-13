"""Coaching scenarios (openspec/specs/coaching/spec.md, CO-xx): learn instead of delegating, code review preparation,
log troubleshooting with secret masking, knowledge checks, benefits without money operations, engineering onboarding."""

from __future__ import annotations

from agent.flows import coach

from .conftest import all_of, first, text_of


def _sources(ev) -> list[str]:  # noqa: ANN001
    src = first(ev, "sources")
    return [i["doc_id"] for i in src["items"]] if src else []


def _quiz_state(session) -> dict:  # noqa: ANN001
    return (session.c.agent.store.get(f"session:{session.ctx['user_id']}:{session.session_id}") or {}).get("quiz") or {}


def test_CO_01_delegation_is_coached_not_done(anna):
    ev = anna.say("напиши за меня код задачи целиком")
    text = text_of(ev)
    assert "не могу" in text and "шаг" in text
    assert _sources(ev) == ["common-ai-usage"]
    assert first(ev, "action_proposal") is None
    assert "Олег Мартынов" in text  # the mentor is named as the next step, buddy does not replace them


def test_CO_02_code_review_preparation_names_reviewers_and_drafts(anna):
    ev = anna.say("кого позвать на код-ревью?")
    text = text_of(ev)
    assert {"delivery-code-review", "eng-branches-pr"} <= set(_sources(ev))
    assert "Сам код я не ревьюю" in text and "CI зелёный" in text
    assert "Олег Мартынов" in text
    draft = first(ev, "escalation_draft")
    assert draft["to_role"] == "mentor" and draft["topic"] == "code_review" and "pull request" in draft["text"]


def test_CO_03_reviewers_are_team_specific(ivan):
    ev = ivan.say("посмотри мой pull request")
    text = text_of(ev)
    assert "search-code-review" in _sources(ev) and "delivery-code-review" not in _sources(ev)
    assert "Павел Григорьев" in text and "Олег Мартынов" not in text


def test_CO_04_log_gets_main_line_and_hint(anna):
    ev = anna.say(
        "$ git clone ssh://bitbucket/dlv/app.git\nERROR: Permission denied (publickey).\nfatal: Could not read"
    )
    text = text_of(ev)
    assert "Permission denied (publickey)" in text
    assert "Bitbucket" in text
    assert _sources(ev) == ["eng-repo-bitbucket"]
    draft = first(ev, "escalation_draft")
    assert draft["topic"] == "troubleshoot" and "publickey" in draft["text"]


def test_CO_05_secrets_are_masked_everywhere(anna):
    log = (
        "Traceback (most recent call last):\nModuleNotFoundError: No module named 'app'\n"
        "DATABASE_URL=postgres://svc:SuperSecret1@db:5432/app password=hunter2 "
        "Authorization: Bearer abcdefghijklmnop123 AKIAABCDEFGHIJKLMNOP"
    )
    ev = anna.say(log)
    out = text_of(ev) + str(all_of(ev, "escalation_draft"))
    for secret in ("SuperSecret1", "hunter2", "abcdefghijklmnop123", "AKIAABCDEFGHIJKLMNOP"):
        assert secret not in out
    assert "секреты (4)" in text_of(ev)
    masked, n = coach.mask_secrets("token: abc api_key=xyz eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghij")
    assert n == 3 and "abc" not in masked.split("token:")[1][:5] and "<jwt скрыт>" in masked


def test_CO_06_unknown_log_gets_generic_advice(anna):
    ev = anna.say("ERROR something odd happened in module X\nERROR retry 2 failed\nERROR retry 3 failed")
    text = text_of(ev)
    assert "Точную причину по логу не определить" in text
    assert first(ev, "escalation_draft") is not None
    assert first(ev, "sources") is None


def test_CO_07_quiz_from_topic(anna):
    ev = anna.say("проверь меня по защите данных")
    assert _sources(ev) == ["common-confidential-info"]
    text = text_of(ev)
    assert text.count("_____") >= 2 and "Тестов" not in text[:5]
    assert "тестовой инструкции" in text
    state = _quiz_state(anna)
    assert state["doc_id"] == "common-confidential-info" and len(state["answers"]) >= 2


def test_CO_08_quiz_full_score_suggests_marking_task(anna):
    anna.say("проверь меня")
    state = _quiz_state(anna)
    assert state["task"]  # no topic → the first open plan task with a matching document
    ev = anna.say(", ".join(state["answers"]))
    text = text_of(ev)
    n = len(state["answers"])
    assert f"Результат: {n} из {n}" in text and state["task"] in text
    assert _quiz_state(anna) == {}


def test_CO_09_quiz_wrong_answers_show_corrections(anna):
    anna.say("задай вопросы по защите данных")
    answers = _quiz_state(anna)["answers"]
    text = text_of(anna.say("1. мимо\n2. снова мимо"))
    assert f"Результат: 0 из {len(answers)}" in text
    assert f"нужно: {answers[0]}" in text and "(у тебя: мимо)" in text
    assert "попробуй ещё раз" in text


def test_CO_10_quiz_stops_on_cancel_or_new_scenario(anna):
    anna.say("проверь меня по защите данных")
    assert "остановил" in text_of(anna.say("стоп"))
    anna.say("проверь меня по защите данных")
    ev = anna.say("хочу отгул в пятницу")
    assert _quiz_state(anna) == {}
    assert "Результат" not in text_of(ev)
    anna.say("проверь меня по защите данных")
    ev = anna.say("сколько я буду получать?")  # guardrails still win over the quiz
    assert first(ev, "refusal")["detail"] == "sensitive_data"


def test_CO_11_quiz_without_document(anna, monkeypatch):
    monkeypatch.setattr(coach, "_quiz_doc", lambda turn, text: None)
    text = text_of(anna.say("устрой мне квиз"))
    assert "Не нашёл подходящего документа" in text
    assert coach.build_quiz("короткий текст") == []
    assert coach._mask("a b c") is None


def test_CO_12_benefit_operation_refused_with_reference(anna):
    ev = anna.say("подключи мне компенсацию спорта")
    refusal = first(ev, "refusal")
    assert refusal["reason"] == "out_of_scope" and refusal["detail"] == "benefit"
    text = text_of(ev)
    assert "3 000 ₽" in text and "тестовые данные" in text and "HR BP" in text
    assert _sources(ev) == ["common-benefits-overview"]
    # personal pay stays a sensitive-data refusal
    assert first(anna.say("какая у меня будет зарплата?"), "refusal")["detail"] == "sensitive_data"


def test_CO_13_engineering_onboarding_answers_from_kb(anna):
    cases = {
        "как создать ветку для задачи?": "eng-branches-pr",
        "как устроен ci cd и деплой?": "eng-cicd-gitops",
        "где хранить секреты сервиса?": "eng-secrets",
        "как поднять дев стенд?": "eng-dev-stand",
        "где склонировать репозиторий в bitbucket?": "eng-repo-bitbucket",
    }
    for question, doc in cases.items():
        ev = anna.say(question)
        assert _sources(ev)[0] == doc, question
        src = first(ev, "sources")["items"][0]
        assert src["source_type"] == "test" and src["badge"] == "Тестовая инструкция прототипа"
        assert "## " not in text_of(ev), question  # markdown headings are cleaned from snippets


def test_CO_14_location_picks_office_section(anna, ivan):
    assert anna.ctx["location"] == "Москва" and ivan.ctx["location"] == "Санкт-Петербург"
    anna_text = text_of(anna.say("где получить ноутбук?"))
    ivan_text = text_of(ivan.say("где получить ноутбук?"))
    assert "московском офисе" in anna_text
    assert "петербургском офисе" in ivan_text
    # an explicit city in the question wins over the profile
    assert "курьером" in text_of(ivan.say("как получить ноутбук, если я работаю удалённо?"))


def test_CO_15_help_lists_coaching_and_roles(client, anna):
    text = text_of(anna.say("что ты умеешь?"))
    assert "код-ревью" in text and "проверь меня" in text and "Задачи за тебя я не делаю" in text
    hrbp = client.login("hrbp.elena")
    ev = hrbp.say("кого позвать на код-ревью?")
    assert first(ev, "refusal")["reason"] == "no_rights"
    assert coach._section("## A\nx\n## B\ny", "C") == ""


class _RemoteKb:
    """Stands in for mcp-kb: ts_rank scores in 0..1 and websearch_to_tsquery AND semantics."""

    def __init__(self, inner, answers):  # noqa: ANN001
        self.inner, self.answers, self.queries = inner, answers, []

    def call(self, tool, args, token):  # noqa: ANN001, ANN201
        if tool != "kb.search":
            return self.inner.call(tool, args, token)
        self.queries.append(args["query"])
        return {"items": self.answers(args["query"])}


def test_CO_13_remote_kb_scores_retry_and_local_fallback(client, anna):
    agent = client.agent
    inner = agent.backends["kb"]
    hit = {
        "doc_id": "delivery-team-overview",
        "title": "Кто есть кто",
        "source_type": "test",
        "score": 0.72,
        "snippet": "x",
    }
    remote = _RemoteKb(inner, lambda q: [hit] if "команд" in q else [])
    agent.backends["kb"] = remote
    try:
        ev = anna.say("кто есть кто в команде?")  # FTS score 0.72 < local min_score 1.2 must still answer
        assert _sources(ev) == ["delivery-team-overview"]
        ev = anna.say("как создать ветку для задачи?")  # AND query finds nothing → OR retry → local fallback
        assert " or " in remote.queries[-1]
        assert _sources(ev)[0] == "eng-branches-pr"
    finally:
        agent.backends["kb"] = inner


def test_CO_16_colloquial_words_find_the_document(anna):
    from agent.text import canonical_query

    cases = {
        "будет ли у меня рабочий ноут?": "common-laptop",
        "где взять комп?": "common-laptop",
        "как сделать пр в репу?": "eng-branches-pr",
        "как настроить впн?": "common-network-email",
        "как задеплоить сервис?": "eng-cicd-gitops",
    }
    for question, doc in cases.items():
        ev = anna.say(question)
        assert _sources(ev)[0] == doc, question
        assert first(ev, "escalation_draft") is None, question
    assert canonical_query("когда у нас стендап?") == "когда у нас стендап?"  # not a dev stand
    assert "ноутбук" not in canonical_query("как работает квантовый компьютер?")


def test_CO_17_nearby_documents_before_escalation_and_kb_loop(anna, monkeypatch):
    from agent.flows import qa

    class _Doc:
        doc_id, title, url = "common-team-values", "Ценности команды", ""

    monkeypatch.setattr(qa, "_nearby", lambda turn, text: [_Doc()])
    ev = anna.say("Где посмотреть расписание командного митапа по архитектуре?")
    text = text_of(ev)
    assert "Возможно, поможет: «Ценности команды»" in text and "в базе знаний" in text
    assert _sources(ev) == ["common-team-values"]
    draft = first(ev, "escalation_draft")
    # organizers 13.11: the intern writes the mentor directly; the question goes to the mentor's stack
    assert draft["to_role"] == "mentor" and draft["queue"] is True and draft["channel"] == "#delivery-team"
    assert "в личных сообщениях или в канале #delivery-team" in text and "в список вопросов для наставника" in text
    monkeypatch.undo()
    ev = anna.say("Где посмотреть расписание командного митапа по архитектуре?")
    assert "ниже готовый текст" in text_of(ev) and first(ev, "sources") is None


def test_CO_22_unsure_classifier_but_the_kb_knows(anna, client):
    """«расскажи про ноутбук» looked off-topic to the classifier; the KB document on laptops answers it."""
    from tests.conftest import first

    ev = anna.say("расскажи про ноутбук")
    assert first(ev, "sources")["items"][0]["doc_id"] == "common-laptop"
    assert "Я помогаю с адаптацией" not in "".join(e.data["text"] for e in ev if e.name == "token")
    assert first(anna.say("расскажи анекдот"), "sources") is None  # a real off-topic question stays off-topic


def test_CO_23_unclear_task_gets_steps_not_a_summary(anna):
    """QA 12.11: «не понимаю задачу» was routed to the equator summary."""
    from tests.conftest import first, text_of

    ev = anna.say("не понимаю задачу, помоги")
    assert first(ev, "done")["intent"] == "task_help" and "критерии готовности" in text_of(ev)
    assert first(ev, "sources")["items"][0]["doc_id"] == "delivery-first-task"


def test_CO_24_red_ci_question_gets_steps(anna):
    """QA 12.11: a question about failed tests is not a log to parse."""
    from tests.conftest import first, text_of

    ev = anna.say("что делать если упали тесты в ci?")
    t = text_of(ev)
    assert "Что делать, если CI красный" in t and "Главная строка ошибки" not in t
    assert first(ev, "sources")["items"][0]["doc_id"] == "eng-cicd-gitops"


def test_CO_25_trip_while_an_absence_draft_is_open(anna):
    """QA 12.11: «оформи командировку с … по …» after an absence draft became a new absence."""
    from tests.conftest import first

    first(anna.say("мне нужно отсутствие на учёбу 20 ноября"), "action_proposal")
    p = first(
        anna.say("оформи командировку в Казань с 01.12.2026 по 03.12.2026, цель — встреча с командой"),
        "action_proposal",
    )
    assert p["kind"] == "trip_request"


def test_CO_20_study_task_confirmed_by_the_quiz(anna):
    """Organizers 12.13: «Проверить себя» on a study task of the plan → quiz on that task's documents → a full score
    sends quiz_passed (core marks the task done); a partial score does not."""
    title = "Изучи все необходимые документы и мануалы по работе в своей команде"  # CL-07, as the plan button sends it
    anna.say(f"Проверь меня по задаче «{title}»")
    state = _quiz_state(anna)
    assert state["item"] == "CL-07" and state["doc_id"].startswith("delivery-"), state
    ev = anna.say("мимо")
    assert first(ev, "quiz_passed") is None
    anna.say(f"Проверь меня по задаче «{title}»")
    state = _quiz_state(anna)
    ev = anna.say(", ".join(state["answers"]))
    assert first(ev, "quiz_passed") == {"checklist_item_id": "CL-07"}
    assert "отмечена выполненной" in text_of(ev)


def test_S2_23_only_interns_questions_go_to_the_stack(client):
    """Organizers 13.11: a mentor's unknown question gives the text to the curator, nothing goes to a stack."""
    ev = client.login("mentor.oleg").say("Где посмотреть расписание командного митапа по архитектуре?")
    draft = first(ev, "escalation_draft")
    assert draft and "queue" not in draft and "в список вопросов" not in text_of(ev)
