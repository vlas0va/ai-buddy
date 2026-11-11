"""Business trips (TP-xx) and consents (CN-xx) — openspec/specs/trips-consents/spec.md."""

from __future__ import annotations

from tests.conftest import first, text_of


def test_TP_01_explains_policy_and_asks_slots(anna):
    ev = anna.say("Как оформить командировку?")
    t = text_of(ev)
    assert "за 5 дней" in t and "Мария Лебедева" in t and "город, даты и цель" in t
    assert "trips.get_policy" in first(ev, "done")["tool_calls"]
    assert first(ev, "action_proposal") is None


def test_TP_02_one_message_draft_and_confirm_creates_request(anna):
    ev = anna.say("оформи командировку в Казань с 23 по 25 ноября, цель — встреча с командой поиска")
    p = first(ev, "action_proposal")
    assert p["kind"] == "trip_request" and p["draft"]["tool"] == "trips.create_request"
    assert p["draft"]["args"] == {
        "city": "Казань",
        "start_date": "2026-11-23",
        "end_date": "2026-11-25",
        "purpose": "встреча с командой поиска",
    }
    assert p["draft"]["display"]["days"] == 3 and p["requires_approval"] is True
    assert "trips.create_request" not in first(ev, "done")["tool_calls"]
    r = first(anna.confirm(p), "tool_result")
    assert r["kind"] == "trip_request" and r["external_id"].startswith("TRP-") and r["status"] == "pending_approval"
    assert r["summary"].startswith("Командировка: Казань") and "служба командировок" in r["followup"]
    ev = anna.say("статус моей командировки")
    assert "TRP-000200" in text_of(ev) and "На согласовании" in text_of(ev)
    assert "командировка Казань" in text_of(anna.say("мои заявки"))


def test_TP_03_slot_filling_across_turns(anna):
    anna.say("хочу в командировку")
    ev = anna.say("в Екатеринбург")
    assert "На какие даты" in text_of(ev) and "цель" in text_of(ev)
    ev = anna.say("с 1 по 3 декабря")
    assert "Какая цель" in text_of(ev) and first(ev, "done")["route_source"] == "state"
    ev = anna.say("воркшоп по архитектуре")
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["purpose"] == "воркшоп по архитектуре" and p["draft"]["args"]["city"] == "Екатеринбург"


def test_TP_04_validation_home_city_notice_and_overlap(anna, ivan):
    ev = anna.say("командировка в Москву с 23 по 25 ноября, цель — встреча")
    assert "ваш город" in text_of(ev) and first(ev, "action_proposal") is None
    ev = ivan.say("командировка в Питер с 23 по 24 ноября, цель — встреча")
    assert "ваш город" in text_of(ev)
    ev = anna.say("командировка в Казань с 10 по 11 ноября, цель — встреча")
    assert "за 5 дней" in text_of(ev)
    p = first(anna.say("командировка в Казань с 23 по 25 ноября, цель — встреча"), "action_proposal")
    anna.confirm(p)
    ev = anna.say("командировка в Новосибирск с 24 по 26 ноября, цель — конференция")
    assert "пересекаются" in text_of(ev)
    ev = anna.say("командировка в Казань с 1 по 20 декабря, цель — проект")
    assert "не больше 14 дней" in text_of(ev)


def test_TP_05_booking_is_refused_cancel_clears_draft(anna):
    ev = anna.say("забронируй мне билет в Казань")
    assert first(ev, "refusal")["detail"] == "trip" and "служба командировок" in text_of(ev)
    anna.say("хочу в командировку в Казань")
    assert "отменил" in text_of(anna.say("отмена"))
    ev = anna.say("а гостиницу кто бронирует при командировке?")
    assert "служба командировок" in text_of(ev)
    p = first(anna.say("командировка в Казань с 23 по 25 ноября, цель — встреча"), "action_proposal")
    assert "«Отправить»" in text_of(anna.say("да"))
    assert p is not None


def test_TP_06_lead_approves_trip_in_chat(client, anna):
    p = first(anna.say("командировка в Казань с 23 по 25 ноября, цель — встреча"), "action_proposal")
    trp = first(anna.confirm(p), "tool_result")["external_id"]
    maria = client.login("lead.maria")
    assert trp in text_of(maria.say("покажи заявки на согласование"))
    d = first(maria.say(f"согласуй {trp}"), "action_proposal")
    assert d["kind"] == "approval_decision"
    assert first(maria.confirm(d), "tool_result")["status"] == "approved"
    assert "Согласовано" in text_of(anna.say("статус командировки"))


def test_TP_07_roles_and_empty_status(client):
    hr = client.login("hrbp.elena")
    ev = hr.say("оформи командировку в Казань")
    assert first(ev, "refusal")["reason"] == "no_rights"
    assert "Заявок на командировку у тебя нет" in text_of(client.login("intern.ivan").say("мои командировки"))


def test_CN_01_lists_consents_with_statuses(anna):
    ev = anna.say("какие у меня согласия?")
    t = text_of(ev)
    assert "Обработка персональных данных (обязательное) — подписано" in t and "в кадровом портале" in t
    assert "Фото и видео во внутренних материалах (добровольное) — не подписано" in t
    assert "подпиши согласие на фото" in t


def test_CN_02_sign_voluntary_consent_after_confirmation(anna):
    ev = anna.say("подпиши согласие на фото")
    p = first(ev, "action_proposal")
    assert p["kind"] == "consent" and p["draft"] == {
        **p["draft"],
        "tool": "consents.sign",
        "args": {"consent_id": "photo"},
    }
    r = first(anna.confirm(p), "tool_result")
    assert r["status"] == "signed" and r["external_id"].startswith("CNS-") and "подписано" in r["summary"]
    assert "уже подписано" in text_of(anna.say("подпиши согласие на фото"))
    ev = anna.say("покажи мои согласия")
    assert "Все согласия подписаны" not in text_of(ev) and "подпиши согласие на опросы" in text_of(ev)


def test_CN_03_hr_portal_consents_and_revoke(anna):
    ev = anna.say("хочу подписать согласие на обработку персональных данных")
    assert "уже подписано" in text_of(ev) and first(ev, "action_proposal") is None
    svc = anna.c.agent.mocks
    svc.signatures = [g for g in svc.signatures if not (g["employee_id"] == "intern.anna" and g["consent_id"] == "nda")]
    ev = anna.say("подпиши NDA согласие")
    assert "только в кадровом портале" in text_of(ev) and first(ev, "action_proposal") is None
    ev = anna.say("хочу отозвать согласие на фото")
    assert "через HR BP" in text_of(ev) and "Елена Соколова" in text_of(ev)


def test_CN_04_all_signed(anna):
    for kind in ("фото", "опросы"):
        anna.confirm(first(anna.say(f"подпиши согласие на {kind}"), "action_proposal"))
    assert "Все согласия подписаны" in text_of(anna.say("мои согласия"))
