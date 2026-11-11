"""Mentor and lead scenarios — openspec/specs/team-roles/spec.md."""

from __future__ import annotations

from tests.conftest import first, text_of


def _anna_day_off(anna):
    return first(anna.confirm(first(anna.say("хочу отгул в пятницу"), "action_proposal")), "tool_result")


def test_TR_01_lead_lists_pending(client, anna):
    r = _anna_day_off(anna)
    t = text_of(client.login("lead.maria").say("что ждёт моего согласования?"))
    assert r["external_id"] in t and "Анна Котова" in t


def test_TR_02_lead_approves_in_chat_and_intern_sees_status(client, anna):
    r = _anna_day_off(anna)
    lead = client.login("lead.maria")
    p = first(lead.say(f"согласуй {r['external_id']}"), "action_proposal")
    assert p["kind"] == "approval_decision" and p["draft"]["args"]["decision"] == "approved"
    res = first(lead.confirm(p), "tool_result")
    assert res["status"] == "approved" and res["status_label"] == "Согласовано руководителем"
    assert "Ждёт HR-администратора" in text_of(anna.say("мои заявки"))


def test_TR_03_lead_rejects_with_comment(client, anna):
    r = _anna_day_off(anna)
    lead = client.login("lead.maria")
    p = first(lead.say(f"отклони {r['external_id']} потому что в пятницу релиз"), "action_proposal")
    assert p["draft"]["args"] == {
        "approval_id": p["draft"]["args"]["approval_id"],
        "decision": "rejected",
        "comment": "в пятницу релиз",
    }
    assert first(lead.confirm(p), "tool_result")["status"] == "rejected"
    assert "Отклонено" in text_of(anna.say("мои заявки"))


def test_TR_04_unknown_request_id(client):
    assert "нет среди ожидающих" in text_of(client.login("lead.maria").say("согласуй ABS-999999"))


def test_TR_05_no_pending(client):
    assert "нет" in text_of(client.login("lead.maria").say("покажи заявки на согласование"))


def test_TR_06_lead_approves_elevated_access(client, anna):
    anna.confirm(first(anna.say("запроси закрытое пространство"), "action_proposal"))
    lead = client.login("lead.maria")
    pending = client.agent.mocks.t_approvals__list_pending({"sub": "lead.maria"}, {})["items"]
    p = first(lead.say(f"одобри {pending[0]['ref_id']}"), "action_proposal")
    lead.confirm(p)
    assert "Закрытое пространство инцидентов «Доставки» — есть" in text_of(anna.say("какие доступы мне нужны"))


def test_TR_07_decision_twice_is_conflict(client, anna):
    r = _anna_day_off(anna)
    lead = client.login("lead.maria")
    p1 = first(lead.say(f"согласуй {r['external_id']}"), "action_proposal")
    p2 = first(lead.say(f"согласуй {r['external_id']}"), "action_proposal")
    lead.confirm(p1)
    err = first(lead.confirm(p2), "error")
    assert err["retryable"] is False


def test_TR_08_lead_sees_mentees_of_team(client):
    lead = client.login("lead.maria")
    assert "Анна Котова" in text_of(lead.say("как дела у моего стажёра"))


def test_S1_16_mentor_prepares_one_on_one(client):
    oleg = client.login("mentor.oleg")
    ev = oleg.say(
        "Как выполнить задачу «На 1:1 обсуди со стажёром промежуточные итоги, дай обратную связь по его прогрессу»?"
    )
    t = text_of(ev)
    assert first(ev, "done")["intent"] in ("human_only", "one_on_one")
    assert "Повестка 1:1" in t and "Анна" in t and first(ev, "escalation_draft") is None
    ev = oleg.say("подготовь меня к 1:1 с Анной")
    assert first(ev, "done")["intent"] == "one_on_one" and "Обратная связь" in text_of(ev)
    oleg.ctx["mentees"] = []
    assert "Мои стажёры" in text_of(oleg.say("подготовь меня к 1:1"))


def test_S1_16b_intern_still_gets_draft_for_feedback(anna):
    ev = anna.say("дай мне обратную связь по моей работе")
    assert first(ev, "escalation_draft")["to_role"] == "mentor"
