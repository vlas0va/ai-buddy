"""S3 «Оформить отгул» — openspec/specs/absence-request/spec.md."""

from __future__ import annotations

from tests.conftest import all_of, first, text_of


def test_S3_01_explains_procedure_with_test_source_and_asks_dates(anna):
    ev = anna.say("Как оформить отгул?")
    assert ev[0].name == "status"
    src = first(ev, "sources")
    assert src["items"][0]["doc_id"] == "common-dayoff-procedure"
    assert src["items"][0]["source_type"] == "test" and src["items"][0]["badge"] == "Тестовая инструкция прототипа"
    assert "На какие даты" in text_of(ev)
    assert "Мария Лебедева" in text_of(ev)  # approver from the directory
    assert ev[-1].name == "done"


def test_S3_02_parses_relative_date_and_validates_via_service(anna):
    ev = anna.say("хочу отгул в пятницу")
    p = first(ev, "action_proposal")
    assert p["draft"]["args"] == {
        "type": "day_off",
        "start_date": "2026-11-13",
        "end_date": "2026-11-13",
        "comment": "",
    }
    assert "absence.validate_request" in first(ev, "done")["tool_calls"]


def test_S3_03_slot_filling_across_turns(anna):
    anna.say("хочу взять отгул")
    ev = anna.say("с 16 по 17 ноября")
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["start_date"] == "2026-11-16" and p["draft"]["args"]["end_date"] == "2026-11-17"
    assert p["draft"]["display"]["working_days"] == 2
    assert first(ev, "done")["route_source"] == "state"


def test_S3_04_draft_before_write_no_write_tool_called(anna):
    ev = anna.say("хочу отгул в пятницу")
    p = first(ev, "action_proposal")
    assert p["kind"] == "absence_request" and p["draft"]["tool"] == "absence.create_request"
    assert p["draft"]["display"]["approver_display_name"] == "Мария Лебедева"
    assert "absence.create_request" not in first(ev, "done")["tool_calls"]
    assert anna.c.agent.mocks.t_absence__list_my_requests({"sub": "intern.anna"}, {})["items"] == []


def test_S3_05_confirmed_request_created_shown_saved(anna):
    p = first(anna.say("хочу отгул в пятницу"), "action_proposal")
    ev = anna.confirm(p)
    names = [e.name for e in ev]
    assert names[0] == "status" and names[-1] == "done" and "tool_result" in names
    r = first(ev, "tool_result")
    assert r["external_id"].startswith("ABS-") and r["status"] == "pending_approval"
    assert r["status_label"] == "На согласовании" and r["approver_display_name"] == "Мария Лебедева"
    assert r["kind"] == "absence_request" and r["replayed"] is False
    # visible after "reload": the service returns it in the list
    status = text_of(anna.say("какой статус моей заявки?"))
    assert r["external_id"] in status and "На согласовании" in status


def test_S3_06_reminds_to_talk_to_mentor_not_replace_human(anna):
    ev = anna.say("хочу отгул в пятницу")
    assert "предупреди наставника" in text_of(ev)
    r = first(anna.confirm(first(ev, "action_proposal")), "tool_result")
    assert "предупредить наставника" in r["followup"]


def test_S3_07_weekend_is_rejected_by_validation(anna):
    ev = anna.say("хочу отгул 14 ноября")
    assert first(ev, "action_proposal") is None
    assert "выходной" in text_of(ev)


def test_S3_07b_past_and_too_long_and_overlap(anna):
    assert "уже прошла" in text_of(anna.say("отгул 2 ноября"))
    anna.say("отмена")
    assert "не больше 3 рабочих дней" in text_of(anna.say("отгул с 16 по 20 ноября"))
    anna.say("отмена")
    p = first(anna.say("отгул 18 ноября"), "action_proposal")
    anna.confirm(p)
    assert "Пересекается" in text_of(anna.say("хочу отгул 18 ноября"))


def test_S3_08_confirm_by_text_requires_button(anna):
    anna.say("хочу отгул в пятницу")
    ev = anna.say("да, отправляй")
    assert "нажми «Отправить»" in text_of(ev)
    assert first(ev, "action_proposal") is None


def test_S3_09_cancel_clears_draft(anna):
    anna.say("хочу отгул в пятницу")
    assert "отменил" in text_of(anna.say("отмена"))
    assert "На какие даты" in text_of(anna.say("хочу отгул"))


def test_S3_10_service_timeout_does_not_duplicate_request(app_factory):
    from tests.conftest import Client

    client = Client(app_factory(mock_faults="absence.create_request=timeout_after:1"))
    s = client.login("intern.anna")
    p = first(s.say("хочу отгул в пятницу"), "action_proposal")
    token = s.token_for(p)
    err = first(s.confirm(p, token), "error")
    assert err["retryable"] is True and err["code"] == "service_timeout"
    r = first(s.confirm(p, token), "tool_result")  # «Повторить» with the same proposal
    assert r["replayed"] is True
    assert len(client.agent.mocks.t_absence__list_my_requests({"sub": "intern.anna"}, {})["items"]) == 1


def test_S3_10b_repeat_confirm_returns_cached_result(anna):
    p = first(anna.say("хочу отгул в пятницу"), "action_proposal")
    first_result = first(anna.confirm(p), "tool_result")
    again = first(anna.confirm(p), "tool_result")
    assert again["external_id"] == first_result["external_id"] and again["replayed"] is True


def test_S3_11_request_for_another_person_is_refused(anna):
    ev = anna.say("оформи отгул за Ивана")
    refusal = first(ev, "refusal")
    assert refusal["reason"] == "no_rights"
    assert first(ev, "action_proposal") is None


def test_S3_12_invalid_or_tampered_confirmation_is_rejected(anna, ivan):
    p = first(anna.say("хочу отгул в пятницу"), "action_proposal")
    assert first(anna.confirm(p, token="bad.token"), "error")["code"] == "confirmation_invalid"
    tampered = {**p["draft"], "args": {**p["draft"]["args"], "start_date": "2026-11-12", "end_date": "2026-11-12"}}
    assert first(anna.confirm(p, draft=tampered), "error")["code"] == "confirmation_invalid"
    # another user cannot execute Anna's proposal even with a token minted for himself
    assert first(ivan.confirm(p), "error")["code"] == "confirmation_invalid"


def test_S3_13_vacation_and_sick_leave_are_explained_not_created(anna):
    ev = anna.say("хочу отпуск с 1 по 10 декабря")
    assert first(ev, "action_proposal") is None
    assert "только отгул" in text_of(ev)
    assert first(ev, "sources")["items"][0]["doc_id"] == "common-vacation"


def test_S3_14_short_notice_warning(anna):
    assert "за 2 рабочих дня" in text_of(anna.say("отгул завтра"))


def test_S3_15_scenario_flag_off_disables_flow(anna):
    ev = anna.say("хочу отгул в пятницу", flags={"scenario_s3": False})
    assert first(ev, "action_proposal") is None and "выключен" in text_of(ev)


def test_S3_16_alumni_role_cannot_create_day_off(client):
    s = client.login("alumni.petr")
    ev = s.say("хочу отгул завтра")
    assert first(ev, "refusal")["reason"] == "no_rights"


def test_S3_17_unparsed_dates_ask_again(anna):
    anna.say("хочу отгул")
    assert "Не получилось понять даты" in text_of(anna.say("ну когда-нибудь, отгул"))


def test_S3_18_proposal_fields_follow_contract(anna):
    p = first(anna.say("хочу отгул в пятницу"), "action_proposal")
    assert set(p) >= {"proposal_id", "kind", "summary", "draft"}
    assert set(p["draft"]) == {"tool", "args", "display"}
    assert len(p["proposal_id"]) == 36  # uuid = idempotency_key
    assert all_of(anna.say("мои заявки"), "action_proposal") == []
