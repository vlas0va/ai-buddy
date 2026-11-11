"""S1 in chat, AI-cron reminders, progress summary — openspec/specs/plan-assistant/spec.md."""

from __future__ import annotations

from tests.conftest import first, text_of


def test_S1_01_next_step_and_stage(anna):
    ev = anna.say("что мне делать дальше?")
    t = text_of(ev)
    assert "Сейчас этап «2 неделя»" in t and "Следующий шаг" in t
    step = first(ev, "next_step")
    assert step["checklist_item_id"] and step["cta"]["message"]


def test_S1_02_overdue_items_come_first(anna):
    ev = anna.say("что у меня просрочено?")
    assert "Просрочено" in text_of(ev)
    assert first(ev, "next_step")["status"] == "overdue"


def test_S1_03_done_tasks_change_next_step(client, anna):
    before = first(anna.say("какой мой следующий шаг"), "next_step")["checklist_item_id"]
    client.http.post("/dev/plan/task", json={"checklist_item_id": before, "status": "done"}, headers=anna.headers)
    anna.refresh()
    after = first(anna.say("какой мой следующий шаг"), "next_step")["checklist_item_id"]
    assert after != before


def test_S1_04_human_only_step_is_marked(anna):
    ctx = dict(anna.ctx)
    ctx["plan"] = {
        "tasks": [
            {
                "checklist_item_id": "CL-03",
                "title": "Познакомься с командой",
                "status": "not_started",
                "period": "week_1",
                "human_only": True,
            }
        ]
    }
    anna.ctx = ctx
    ev = anna.say("что дальше?")
    assert first(ev, "next_step")["human_only"] is True and "делаешь ты сам" in text_of(ev)


def test_S1_05_template_used_when_core_sends_only_open_tasks(client, ivan):
    ivan.ctx = {
        "user_id": "intern.ivan",
        "role": "intern",
        "team_id": "search",
        "current_period": "week_1",
        "open_tasks": [{"checklist_item_id": "CL-05", "title": "Запроси доступ в пространство"}],
    }
    ev = ivan.say("какой мой следующий шаг?")
    assert first(ev, "next_step")["cta"]["kind"] == "access"


def test_S1_06_template_fallback_without_any_tasks(ivan):
    ivan.ctx = {"user_id": "intern.ivan", "role": "intern", "team_id": "search"}
    ev = ivan.say("мой план")
    assert first(ev, "next_step") is not None
    assert "checklist.get_plan_template" in first(ev, "done")["tool_calls"]


def test_S1_07_mentor_plan_includes_mentees(client):
    mentor = client.login("mentor.oleg")
    t = text_of(mentor.say("мой план"))
    assert "Твои стажёры" in t and "Анна" in t


def test_S1_08_alumni_has_no_plan_R58(client):
    assert "нет плана адаптации" in text_of(client.login("alumni.petr").say("что мне делать дальше?"))


def test_S1_09_lead_overview(client):
    assert "на согласовании" in text_of(client.login("lead.maria").say("что дальше по плану?"))


def test_S1_10_summary_for_equator(client, anna):
    anna.confirm(first(anna.say("запроси доступ в jira"), "action_proposal"))
    t = text_of(anna.say("подготовь сводку для экватора"))
    assert "Выполнено задач плана" in t and "Jira" in t and "согласуй" in t


def test_S1_11_mentor_summary(client):
    t = text_of(client.login("mentor.oleg").say("подготовь сводку прогресса моего стажёра"))
    assert "Анна Котова" in t and "обратную связь даёшь ты" in t


def test_S1_12_reminders_endpoint_human_only_and_overdue(client, anna):
    items = [
        {"task_id": "t1", "checklist_item_id": "CL-11", "title": "Экваторная встреча", "human_only": True},
        {"task_id": "t2", "checklist_item_id": "CL-05", "title": "Запроси доступ", "reason": "overdue"},
        {"task_id": "t3", "checklist_item_id": "CL-08", "title": "Защита данных", "reason": "period_start"},
    ]
    r = client.http.post("/v1/reminders", json={"user_context": anna.ctx, "items": items}, headers=anna.headers)
    rem = r.get_json()["reminders"]
    assert [x["kind"] for x in rem] == ["human_only", "overdue", "period_start"]
    assert "помочь подготовиться" in rem[0]["text"] and rem[0]["text"].startswith("Анна")


def test_S1_13_summary_endpoint(client, anna):
    r = client.http.post(
        "/v1/summary/progress",
        json={"user_context": anna.ctx, "actions": [{"name": "Jira", "status": "granted"}]},
        headers=anna.headers,
    ).get_json()
    assert r["bullets"][0].startswith("Выполнено задач плана") and "Jira" in r["text"]


def test_S1_14_mentee_without_context_points_to_ui(client):
    m = client.login("mentor.oleg")
    m.ctx = {k: v for k, v in m.ctx.items() if k != "mentees"}
    assert "Мои стажёры" in text_of(m.say("как дела у моего стажёра?"))


def test_S1_15_plan_button_drafts_message_about_the_task(anna):
    tasks = (anna.ctx.get("plan") or {}).get("tasks") or anna.ctx.get("open_tasks") or []
    parent = next(t for t in tasks if not t.get("parent_task_id"))
    ev = anna.say(f"Помоги написать наставнику про задачу «{parent['title']}»")
    draft = first(ev, "escalation_draft")
    assert first(ev, "done")["intent"] == "task_message"
    assert draft["to_role"] == "mentor" and f"«{parent['title']}»" in draft["text"]
    assert "Подскажи, пожалуйста: Помоги" not in draft["text"] and "Не нашёл ответа" not in text_of(ev)
    assert first(ev, "sources") is None


def test_S1_15b_task_with_open_subtasks_lists_them(anna):
    anna.ctx["plan"] = {
        "tasks": [
            {"task_id": "t1", "title": "Работа с наставником", "status": "in_progress", "checklist_item_id": "CL-04"},
            {"task_id": "t2", "title": "Обсуди встречи с наставником", "status": "not_started", "parent_task_id": "t1"},
            {"task_id": "t3", "title": "Попроси добавить в каналы", "status": "done", "parent_task_id": "t1"},
        ]
    }
    ev = anna.say("Помоги написать наставнику про задачу «Работа с наставником»")
    text = first(ev, "escalation_draft")["text"]
    assert "— Обсуди встречи с наставником" in text and "каналы" not in text
