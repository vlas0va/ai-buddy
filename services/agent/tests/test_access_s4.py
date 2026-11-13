"""S4 «Доступы и каналы» — openspec/specs/access-and-channels/spec.md."""

from __future__ import annotations

from tests.conftest import all_of, first, text_of


def test_S4_01_lists_required_accesses_with_statuses(anna):
    ev = anna.say("Какие доступы мне нужны?")
    t = text_of(ev)
    assert (
        "| Confluence | — | чтение, редактирование | есть |" in t
        and "| Jira | — | чтение, редактирование | не запрошен |" in t
    )
    assert "| Ресурс | Окружения | Права | Статус | Кто согласует |" in t  # чекпоинт 11.11: таблица
    assert "согласование: руководитель команды" in t  # ресурс с повышенными правами помечен
    proposals = all_of(ev, "action_proposal")
    kinds = {p["draft"]["args"]["resource_id"] for p in proposals}
    # все недостающие, с повышенными правами тоже (один список с галочками); ресурсы по запросу (база данных) не
    # предлагаются
    assert kinds == {"jira", "bitbucket", "jira-dlv", "bb-delivery-api", "space-delivery", "space-delivery-incidents"}
    assert [
        p["requires_approval"] for p in proposals if p["draft"]["args"]["resource_id"] == "space-delivery-incidents"
    ] == [True]
    assert "Запросить выбранные" in t


def test_S4_02_ordinary_access_request_granted_and_saved(anna):
    p = first(anna.say("запроси доступ в jira"), "action_proposal")
    assert p["requires_approval"] is False and p["task_checklist_item_id"] == "CL-01"
    r = first(anna.confirm(p), "tool_result")
    assert r["status"] == "granted" and r["external_id"].startswith("ACC-")
    assert "| Jira | — | чтение, редактирование | есть |" in text_of(anna.say("какие доступы мне нужны"))


def test_S4_03_elevated_access_goes_through_manager_approval(client, anna):
    p = first(anna.say("запроси закрытое пространство"), "action_proposal")
    assert p["requires_approval"] is True
    assert p["draft"]["display"]["approver_display_name"] == "Мария Лебедева"
    r = first(anna.confirm(p), "tool_result")
    assert r["status"] == "pending_approval" and r["approver_display_name"] == "Мария Лебедева"
    lead = client.login("lead.maria")
    assert "Закрытое пространство инцидентов" in text_of(lead.say("покажи заявки на согласование"))


def test_S4_04_channels_list_and_draft_to_mentor(anna):
    ev = anna.say("какие каналы есть в команде?")
    assert "#delivery-team" in text_of(ev) and "(закрытый)" in text_of(ev)
    # чекпоинт 11.11: о чатах просят в личном сообщении — совет, без черновика
    assert first(ev, "escalation_draft") is None
    assert "личные сообщения" in text_of(ev) and "Олег Мартынов" in text_of(ev)


def test_S4_05_already_granted_is_not_requested_again(anna):
    ev = anna.say("запроси доступ к confluence")
    assert first(ev, "action_proposal") is None and "Повторная заявка не нужна" in text_of(ev)


def test_S4_06_other_team_resource_is_refused(anna):
    ev = anna.say("запроси доступ к пространству поиска")
    assert first(ev, "refusal")["reason"] == "no_rights"
    assert first(ev, "action_proposal") is None


def test_S4_07_access_for_another_person_is_refused(anna):
    assert first(anna.say("запроси доступ в jira для Ивана"), "refusal")["reason"] == "no_rights"


def test_S4_08_unknown_resource(anna):
    ev = anna.say("запроси доступ к марсоходу")
    assert "Не нашёл такой ресурс" in text_of(ev)


def test_S4_09_lead_has_no_access_requests_in_chat(client):
    lead = client.login("lead.maria")
    ev = lead.say("какие доступы мне нужны")
    assert first(ev, "refusal")["reason"] == "no_rights" and "Согласования" in text_of(ev)


def test_S4_10_team_specific_lists_differ(anna, ivan):
    assert "Доставка" in text_of(anna.say("какие доступы мне нужны"))
    t = text_of(ivan.say("какие доступы мне нужны"))
    assert "Поиск" in t and "Доставка" not in t


def test_S4_11_mentor_sees_what_intern_requested(client, anna):
    anna.confirm(first(anna.say("запроси доступ в jira"), "action_proposal"))
    mentor = client.login("mentor.oleg")
    t = text_of(mentor.say("что запросила Анна?"))
    assert "Анна Котова" in t and "Jira: выдан" in t


def test_S4_12_all_granted_message(anna):
    for p in all_of(anna.say("какие доступы мне нужны"), "action_proposal"):
        anna.confirm(p)
    assert "Все нужные доступы уже есть" in text_of(anna.say("какие доступы мне нужны"))


def test_S4_13_concrete_jira_project_and_repository(anna):
    p = first(anna.say("запроси доступ к проекту DLV в Jira"), "action_proposal")
    assert p["draft"]["args"] == {"resource_id": "jira-dlv", "reason": p["draft"]["args"]["reason"], "level": "read"}
    assert first(anna.confirm(p), "tool_result")["status"] == "granted"
    p = first(anna.say("нужны права на запись в репозиторий delivery-api"), "action_proposal")
    assert p["draft"]["args"]["resource_id"] == "bb-delivery-api" and p["draft"]["args"]["level"] == "write"
    assert p["requires_approval"] is True and "владелец репозитория" in p["draft"]["display"]["approver_label"]
    r = first(anna.confirm(p), "tool_result")
    assert r["status"] == "pending_approval" and r["summary"].endswith("(редактирование)")


def test_S4_14_database_with_environment_and_rights(anna):
    ev = anna.say("дай доступ к БД на stage на чтение")
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["resource_id"] == "db-delivery-orders"
    assert p["draft"]["args"]["environment"] == "stage" and p["draft"]["args"]["level"] == "read"
    assert p["requires_approval"] is False and "(stage, чтение)" in text_of(ev)
    assert first(anna.confirm(p), "tool_result")["summary"].endswith("(stage, чтение)")
    p = first(anna.say("доступ к базе данных на prod"), "action_proposal")
    assert p["draft"]["args"]["environment"] == "prod" and p["requires_approval"] is True
    assert "DevOps" in p["draft"]["display"]["approver_label"]


def test_S4_15_database_without_environment_takes_dev(anna):
    ev = anna.say("запроси доступ к базе данных delivery_orders")
    p = first(ev, "action_proposal")
    assert p["draft"]["args"]["environment"] == "dev" and "Окружение не указано" in text_of(ev)


def test_S4_16_level_the_resource_does_not_offer(anna):
    ev = anna.say("дай админские права в confluence")
    assert first(ev, "action_proposal") is None and "можно запросить: чтение, редактирование" in text_of(ev)


def test_S4_17_environment_for_a_resource_without_environments(anna):
    ev = anna.say("доступ к проекту DLV на stage")
    p = first(ev, "action_proposal")
    assert "environment" not in p["draft"]["args"] and "нет окружений" in text_of(ev)


def test_S4_30_justification_and_asking_the_mentor_first(anna):
    """Организаторы 12.11: у заявки есть обоснование; до заявки стажёр может спросить наставника, нужен ли доступ."""
    ev = anna.say("доступ на редактирование в репозиторий delivery-api, обоснование: задача DLV-12 из плана")
    p = first(ev, "action_proposal")
    assert (
        p["draft"]["args"]["reason"] == "задача DLV-12 из плана"
        and p["draft"]["display"]["reason"] == "задача DLV-12 из плана"
    )
    assert p["draft"]["display"][
        "level_label"
    ] == "редактирование" and "Обоснование: «задача DLV-12 из плана»" in text_of(ev)
    ev = anna.say("нужны права на редактирование в репозиторий delivery-api")
    assert "Добавь обоснование" in text_of(ev) and first(ev, "action_proposal")["draft"]["args"]["reason"].startswith(
        "Онбординг"
    )
    ev = anna.say("нужен ли мне доступ к БД delivery_orders?")
    d = first(ev, "escalation_draft")
    assert first(ev, "action_proposal") is None and d["to_role"] == "mentor" and d["topic"] == "access"
    assert "БД delivery_orders" in d["text"] and "лишние доступы" in text_of(ev)
