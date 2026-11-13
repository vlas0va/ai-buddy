"""Заявка на отсутствие как в Авито (чекпоинт 11.11): виды, документы, руководитель → HR-администратор, перенос —
openspec/specs/absence-request/spec.md (S3-13, S3-23 … S3-29).
"""

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
    # руководитель и HR-администратор согласовали: заявку больше нельзя перенести
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


def test_S3_30_calendar_bare_day_and_change_after_the_lead(client, anna):
    """Отзыв 12.11: календарь под объяснением, «13» — это дата, «хочу изменить отгул» переносит заявку даже после
    согласования руководителем (пока не согласовал HR-администратор).
    """
    ev = anna.say("Как оформить отгул?")
    picker = first(ev, "date_picker")
    assert picker["purpose"] == "absence" and picker["min"] == "2026-11-10" and "не позже чем за 3" in text_of(ev)
    p = first(anna.say("13"), "action_proposal")
    assert p["draft"]["args"]["start_date"] == "2026-11-13"
    r = first(anna.confirm(p), "tool_result")
    lead = client.login("lead.maria")
    lead.confirm(first(lead.say(f"согласуй {r['external_id']}"), "action_proposal"))
    ev = anna.say("хочу изменить отгул")
    picker = first(ev, "date_picker")
    assert picker["purpose"] == "reschedule" and picker["ref_id"] == r["external_id"]
    moved = first(anna.say(f"перенеси заявку {r['external_id']} на 17 ноября"), "action_proposal")
    assert moved["kind"] == "absence_reschedule" and moved["draft"]["args"]["start_date"] == "2026-11-17"


def test_S3_31_withdraw_absorb_and_plan_clash(client, anna):
    """Отзыв 12.13: несогласованную заявку можно отозвать; отпуск поверх несогласованного отгула заменяет его за один шаг;
    календарь отмечает занятые дни; упоминается задача плана со сроком в дни отсутствия; проблемы читаются как
    предложения.
    """
    day = first(anna.confirm(first(anna.say("отгул 24 ноября"), "action_proposal")), "tool_result")["external_id"]
    # календарь показывает заявку как занятые дни
    picker = first(anna.say("Как оформить отпуск?"), "date_picker")
    assert any(b["start"] == "2026-11-24" and day in b["label"] for b in picker["busy"])
    # отпуск, накрывающий отгул, заменяет его
    ev = anna.say("отпуск с 23 ноября по 27 ноября")
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["replaces"] == day and "отменится сама" in text_of(ev)
    res = first(anna.confirm(p), "tool_result")
    assert res["result"]["replaced_id"] == day
    # отозвать отпуск; опечатка «оотгул» всё равно означает отгул
    ev = anna.say("отмени заявку на отпуск")
    c = first(ev, "action_proposal")
    assert c["kind"] == "absence_cancel" and c["draft"]["args"]["id"] == res["external_id"]
    done = first(anna.confirm(c), "tool_result")
    assert done["status"] == "cancelled" and "(отменена)" in done["summary"]
    assert "Не нашёл заявку" in text_of(anna.say("отменить заявку на оотгул"))
    # два открытых отгула: buddy спрашивает, какой
    for d in ("1 декабря", "3 декабря"):
        anna.confirm(first(anna.say(f"отгул {d}"), "action_proposal"))
    assert "Какую заявку отменить?" in text_of(anna.say("отмени отгул"))


def test_S3_32_partial_overlap_moves_the_right_request(anna):
    """Отзыв 12.13: при частичном пересечении предлагается перенести ИМЕННО ту заявку, с её видом в заголовке календаря."""
    day = first(anna.confirm(first(anna.say("отгул с 24 по 25 ноября"), "action_proposal")), "tool_result")[
        "external_id"
    ]
    ev = anna.say("отгул с 25 по 26 ноября")
    picker = first(ev, "date_picker")
    assert first(ev, "action_proposal") is None
    assert (
        picker["purpose"] == "reschedule" and picker["ref_id"] == day and picker["label"] == f"Новые даты: отгул {day}"
    )
    assert ". " in text_of(ev).split("оформить не получится:")[1][:200]  # предложения, а не «a; b»


def test_S3_33_lead_sees_teammates_away_on_the_same_days(client, anna):
    """Отзыв 12.13: в списке руководителя у отсутствия видно, кто ещё из команды отсутствует в эти дни."""
    oleg = client.login("mentor.oleg")
    oleg.confirm(first(oleg.say("отгул 25 ноября"), "action_proposal"))
    anna.confirm(first(anna.say("отгул 25 ноября"), "action_proposal"))
    text = text_of(client.login("lead.maria").say("покажи заявки на согласование"))
    assert "в эти дни отсутствуют: Олег Мартынов: отгул 25.11" in text
