"""Absence request as at Avito (checkpoint 11.11): types, documents, lead → HR admin, reschedule —
openspec/specs/absence-request/spec.md (S3-13, S3-23 … S3-29)."""

from __future__ import annotations

from tests.conftest import first, text_of


def doc(name: str = "заявление.pdf"):  # noqa: ANN201
    return {"name": name, "kind": "document", "mime": "application/pdf", "size": 12000}


def say_with(session, text: str, *atts):  # noqa: ANN001, ANN201
    return session.say(text, turn={"kind": "message", "text": text, "attachments": list(atts)})


def test_S3_23_vacation_question_gets_labour_code_facts_and_process(anna):
    ev = anna.say("сколько дней отпуска мне положено и как его взять?")
    t = text_of(ev)
    assert first(ev, "action_proposal") is None
    assert "28 календарных дней" in t and "ст. 115" in t and "6 месяцев" in t and "ст. 122" in t
    assert "HR-администратор" in t and "перенести" in t
    assert first(ev, "sources")["items"][0]["doc_id"] == "common-vacation"
    assert t.count("«") == t.count("»")


def test_S3_24_vacation_request_is_created_with_its_type(anna):
    ev = anna.say("хочу в отпуск с 1 по 10 декабря")
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["type"] == "vacation" and p["draft"]["display"]["type_label"] == "Отпуск"
    assert p["draft"]["display"]["calendar_days"] == 10 and "календарных дней: 10" in text_of(ev)
    r = first(anna.confirm(p), "tool_result")
    assert r["status"] == "pending_approval" and r["summary"].startswith("Отпуск")
    assert "HR-администратор" in r["followup"]


def test_S3_25_vacation_needs_two_weeks_notice(anna):
    ev = anna.say("хочу в отпуск с 16 по 20 ноября")
    assert first(ev, "action_proposal") is None and "за 14 дней" in text_of(ev)


def test_S3_26_parental_leave_needs_a_document(anna):
    t = text_of(anna.say("оформи декрет с 1 декабря по 30 марта"))
    assert "документ" in t
    ev = say_with(anna, "", doc())
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["type"] == "parental_leave"
    assert p["draft"]["args"]["documents"] == [{"name": "заявление.pdf", "mime": "application/pdf", "size": 12000}]
    assert "заявление.pdf" in text_of(ev)
    assert first(anna.confirm(p), "tool_result")["result"]["documents"][0]["name"] == "заявление.pdf"


def test_S3_27_absence_for_study_with_a_document(anna):
    ev = say_with(anna, "нужно отсутствие на учёбу 20 ноября", doc("справка-вызов.pdf"))
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["type"] == "absence" and p["draft"]["display"]["documents"] == ["справка-вызов.pdf"]


def test_S3_28_lead_then_hr_admin_approve(client, anna):
    r = first(anna.confirm(first(anna.say("хочу отгул в пятницу"), "action_proposal")), "tool_result")
    lead = client.login("lead.maria")
    res = first(lead.confirm(first(lead.say(f"согласуй {r['external_id']}"), "action_proposal")), "tool_result")
    assert res["status_label"] == "Согласовано руководителем" and "HR-администратору" in res["followup"]
    assert "Ждёт HR-администратора" in text_of(anna.say("мои заявки"))
    admin = client.login("hradmin.olga")
    listing = text_of(admin.say("что ждёт моего согласования?"))
    assert r["external_id"] in listing and "этап HR-администратора" in listing
    p = first(admin.say(f"согласуй {r['external_id']}"), "action_proposal")
    assert first(admin.confirm(p), "tool_result")["status"] == "approved"
    t = text_of(anna.say("мои заявки"))
    assert "Согласовано" in t and "HR-администратор (Ольга Смирнова): оформил" in t
    assert "HR-администратору могу" in text_of(admin.say("помощь"))


def test_S3_29_reschedule_until_hr_admin_approves(client, anna):
    r = first(anna.confirm(first(anna.say("хочу в отпуск с 1 по 10 декабря"), "action_proposal")), "tool_result")
    lead = client.login("lead.maria")
    lead.confirm(first(lead.say(f"согласуй {r['external_id']}"), "action_proposal"))
    ev = anna.say("перенеси отпуск на 8-17 декабря")
    p = first(ev, "action_proposal")
    assert p["kind"] == "absence_reschedule" and p["draft"]["tool"] == "absence.reschedule_request"
    assert p["draft"]["args"] == {"id": r["external_id"], "start_date": "2026-12-08", "end_date": "2026-12-17"}
    moved = first(anna.confirm(p), "tool_result")
    assert moved["status"] == "pending_approval" and "перенос" in moved["summary"]
    assert r["external_id"] in text_of(lead.say("что ждёт моего согласования?"))
    # the lead and the HR admin approve: the request can no longer be moved
    lead.confirm(first(lead.say(f"согласуй {r['external_id']}"), "action_proposal"))
    admin = client.login("hradmin.olga")
    admin.confirm(first(admin.say(f"согласуй {r['external_id']}"), "action_proposal"))
    ev = anna.say(f"перенеси заявку {r['external_id']} на 15-20 декабря")
    assert first(ev, "action_proposal") is None and "уже нельзя перенести" in text_of(ev)


def test_S3_29_reschedule_edge_cases(anna):
    assert "Не нашёл заявку" in text_of(anna.say("перенеси отпуск на 8-17 декабря"))
    r = first(anna.confirm(first(anna.say("хочу в отпуск с 1 по 10 декабря"), "action_proposal")), "tool_result")
    assert "На какие даты перенести" in text_of(anna.say("перенеси отпуск"))
    assert "не получится" in text_of(anna.say(f"перенеси заявку {r['external_id']} на 12 ноября"))


def test_S3_13_sick_leave_is_not_created(anna):
    ev = anna.say("я заболел, нужен больничный")
    assert first(ev, "action_proposal") is None
