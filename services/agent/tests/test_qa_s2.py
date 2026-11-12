"""S2 «Спроси buddy» — openspec/specs/ask-buddy/spec.md."""

from __future__ import annotations

from tests.conftest import first, text_of


def test_S2_01_answer_with_source_and_test_badge(anna):
    ev = anna.say("как у нас проходит код-ревью?")
    src = first(ev, "sources")["items"]
    assert src[0]["doc_id"] == "delivery-code-review"
    assert all(s["source_type"] == "test" and s["badge"] == "Тестовая инструкция прототипа" for s in src)
    assert "тестовой инструкции" in text_of(ev).lower()
    assert [e.name for e in ev].index("status") == 0


def test_S2_02_status_events_before_answer(anna):
    ev = anna.say("какие ритуалы в команде?")
    stages = [e.data["stage"] for e in ev if e.name == "status"]
    assert stages[0] == "thinking" and "searching_kb" in stages


def test_S2_03_two_teams_get_different_answers_R02(anna, ivan):
    a = first(anna.say("как проходит код-ревью"), "sources")["items"][0]["doc_id"]
    i = first(ivan.say("как проходит код-ревью"), "sources")["items"][0]["doc_id"]
    assert a == "delivery-code-review" and i == "search-code-review"


def test_S2_04_acl_filter_before_llm_mentor_doc_hidden_from_intern_R71(client, anna):
    ev = anna.say("методичка наставника")
    ids = [s["doc_id"] for s in (first(ev, "sources") or {"items": []})["items"]]
    assert "mentor-handbook" not in ids
    mentor = client.login("mentor.oleg")
    ids_m = [s["doc_id"] for s in first(mentor.say("где методичка наставника?"), "sources")["items"]]
    assert "mentor-handbook" in ids_m


def test_S2_05_no_answer_escalates_with_draft(anna):
    ev = anna.say("как устроен квантовый компьютер в нашей команде?")
    draft = first(ev, "escalation_draft")
    assert draft["to_role"] == "mentor" and "квантовый" in draft["text"]
    assert first(ev, "done")["escalated"] is True


def test_S2_06_escalation_addressee_curator_for_university_topics(anna):
    ev = anna.say("спроси у куратора про подпись договора о практике")
    assert first(ev, "escalation_draft")["to_role"] == "curator"


def test_S2_07_context_links_answer_to_plan_task(anna):
    t = text_of(anna.say("какие документы команды нужно изучить?"))
    assert "Это относится к задаче твоего плана" in t


def test_S2_08_whois_from_directory(anna):
    t = text_of(anna.say("кто мой наставник?"))
    assert "Олег Мартынов" in t


def test_S2_09_human_only_topics_are_reminded_not_replaced_R05(anna):
    ev = anna.say("дай мне обратную связь по моей работе")
    assert "проводят люди" in text_of(ev)
    assert first(ev, "escalation_draft")["topic"] == "one_on_one"


def test_S2_10_offtopic_is_politely_declined(anna):
    assert "помогаю с адаптацией" in text_of(anna.say("какая погода в москве?"))


def test_S2_11_smalltalk_and_help(anna):
    assert "Привет, Анна" in text_of(anna.say("привет"))
    assert "оформить отгул" in text_of(anna.say("что ты умеешь?"))
    assert "Пожалуйста" in text_of(anna.say("спасибо"))


def test_S2_12_manual_escalation_uses_history(anna):
    history = [{"author": "user", "text": "Как поднять сервис локально?"}, {"author": "assistant", "text": "..."}]
    ev = anna.say("спроси у наставника", history=history)
    assert "поднять сервис" in first(ev, "escalation_draft")["text"]


def test_S2_13_alumni_still_gets_answers_R58(client):
    s = client.login("alumni.petr")
    assert first(s.say("как проходит код-ревью?"), "sources") is not None


def test_S2_14_kb_doc_endpoint_respects_acl(client, anna):
    ok = client.http.get("/v1/kb/delivery-code-review", headers=anna.headers)
    assert ok.status_code == 200 and ok.get_json()["badge"] == "Тестовая инструкция прототипа"
    assert client.http.get("/v1/kb/mentor-handbook", headers=anna.headers).status_code == 404
    assert client.http.get("/v1/kb/search-code-review", headers=anna.headers).status_code == 404


def test_S2_15_kb_doc_endpoint_opens_a_mentor_answer_from_mcp_kb(client, anna, monkeypatch):
    """Feedback 12.11: a mentor answer added to the KB lives in mcp-kb; its source link must open, not «Не найдено»."""
    from agent.tools.base import ToolGateway

    real = ToolGateway.read

    def fake(self, tool, **args):
        if tool == "kb.get" and args.get("doc_id") == "faq-delivery-1a2b3c4d":
            return {
                "doc_id": "faq-delivery-1a2b3c4d",
                "title": "Где логи стенда",
                "body": "Вопрос: где логи?\n\nОтвет: в Grafana.",
                "source_type": "test",
                "kind": "faq",
            }
        return real(self, tool, **args)

    monkeypatch.setattr(ToolGateway, "read", fake)
    r = client.http.get("/v1/kb/faq-delivery-1a2b3c4d", headers=anna.headers)
    assert (
        r.status_code == 200
        and "Grafana" in r.get_json()["body"]
        and r.get_json()["badge"] == "Тестовая инструкция прототипа"
    )
    assert client.http.get("/v1/kb/faq-search-00000000", headers=anna.headers).status_code == 404


def test_S2_16_topic_the_kb_does_not_know_goes_to_the_mentor(anna):
    """QA 12.13: «собака», «стейдж» are in no document: a document matching only «офис» or «заказ» is not an answer."""
    for q in ("можно ли приходить в офис с собакой?", "как создать тестовый заказ на стейдже доставки?"):
        ev = anna.say(q)
        assert first(ev, "escalation_draft")["to_role"] == "mentor", q
        assert "Точного ответа в базе знаний нет" in text_of(ev)
    ev = anna.say("где посмотреть логи delivery-api на stage?")  # engineering, not off-topic
    assert first(ev, "escalation_draft") is not None and first(ev, "refusal") is None
    assert first(anna.say("где взять ноутбук в первый день?"), "escalation_draft") is None


def test_S2_17_mentor_answer_in_mcp_kb_beats_the_local_ranking(anna, monkeypatch):
    """Feedback 12.13: the answer Oleg added to the KB lives only in mcp-kb; the next intern gets it, not «not found»."""
    from agent.flows import qa
    from agent.tools.base import ToolGateway

    real = ToolGateway.read

    def fake(self, tool, **args):
        if tool == "kb.search":
            return {
                "items": [
                    {
                        "doc_id": "faq-delivery-1a2b3c4d",
                        "title": "Можно ли приходить в офис с собакой",
                        "snippet": "Вопрос: Можно ли приходить в офис с собакой? Ответ: Нет, только собаки-поводыри.",
                        "source_type": "test",
                        "url": "",
                        "score": 0.3,
                    }
                ]
            }
        if tool == "kb.get" and args.get("doc_id") == "faq-delivery-1a2b3c4d":
            return {
                "doc_id": "faq-delivery-1a2b3c4d",
                "title": "Можно ли приходить в офис с собакой",
                "kind": "faq",
                "body": "Вопрос: Можно ли приходить в офис с собакой?\n\nОтвет: Нет, только собаки-поводыри.",
                "source_type": "test",
            }
        return real(self, tool, **args)

    monkeypatch.setattr(ToolGateway, "read", fake)
    monkeypatch.setattr(qa, "_remote_kb", lambda turn: True)
    ev = anna.say("можно ли приходить в офис с собакой?")
    assert first(
        ev, "escalation_draft"
    ) is None and "Наставник уже отвечал на такой вопрос:\n\nНет, только собаки-поводыри." in text_of(ev)
    assert first(ev, "sources")["items"][0]["doc_id"] == "faq-delivery-1a2b3c4d"


def test_S2_18_a_mentor_answer_is_not_used_for_another_question(anna, monkeypatch):
    """Feedback 12.13: mcp-kb's «any word» search returns the answer about dogs for «в офис с ноутбуком?» — not an answer."""
    from agent.flows import qa
    from agent.tools.base import ToolGateway

    real = ToolGateway.read
    faq = {
        "doc_id": "faq-delivery-1a2b3c4d",
        "title": "Можно ли приходить в офис с собакой породы бигль",
        "snippet": "Вопрос: Можно ли приходить в офис с собакой породы бигль?",
        "source_type": "test",
        "url": "",
        "score": 0.3,
    }

    def fake(self, tool, **args):
        if tool == "kb.search":
            return {"items": [faq]}
        if tool == "kb.get":
            return {
                **faq,
                "kind": "faq",
                "body": "Вопрос: Можно ли приходить в офис с собакой породы бигль?\n\nОтвет: Нет.",
            }
        return real(self, tool, **args)

    monkeypatch.setattr(ToolGateway, "read", fake)
    monkeypatch.setattr(qa, "_remote_kb", lambda turn: True)
    assert "Наставник уже отвечал" not in text_of(anna.say("можно ли приходить в офис с ноутбуком?"))
    assert "Наставник уже отвечал" not in text_of(anna.say("можно ли приходить в офис с собакой породы хаски?"))
    assert "Наставник уже отвечал" in text_of(anna.say("можно ли приходить в офис с собакой породы бигль?"))
