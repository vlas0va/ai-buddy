"""HTTP-поверхность и поведение агента — openspec/specs/agent-behavior/spec.md (AG-xx)."""

from __future__ import annotations

import time

from agent.auth import mint_token
from tests.conftest import Client, first, make_settings


def test_AG_01_health_ready_metrics(client):
    assert client.http.get("/healthz").get_json()["status"] == "ok"
    ready = client.http.get("/readyz").get_json()
    assert ready["status"] == "ready" and ready["checks"]["kb_docs"] >= 30 and ready["checks"]["intent_model"]
    s = client.login("intern.anna")
    s.say("привет")
    m = client.http.get("/metrics").get_data(as_text=True)
    assert "agent_turns_total" in m and "agent_turn_seconds_bucket" in m


def test_AG_02_unauthorized_without_or_with_bad_token(client):
    body = {"session_id": "s", "turn": {"kind": "message", "text": "привет"}}
    assert client.http.post("/v1/turn", json=body).status_code == 401
    r = client.http.post("/v1/turn", json=body, headers={"Authorization": "Bearer nope"})
    assert r.status_code == 401 and r.get_json()["error"]["code"] == "unauthorized"


def test_AG_03_context_mismatch_is_rejected(client, anna):
    body = {
        "session_id": "s",
        "turn": {"kind": "message", "text": "привет"},
        "user_context": {**anna.ctx, "role": "lead"},
    }
    r = client.http.post("/v1/turn", json=body, headers=anna.headers)
    assert r.status_code == 403 and r.get_json()["error"]["code"] == "context_mismatch"


def test_AG_04_bad_requests(client, anna):
    h = anna.headers
    assert client.http.post("/v1/turn", data="not json", headers=h, content_type="application/json").status_code == 400
    assert (
        client.http.post(
            "/v1/turn", json={"session_id": "s", "turn": {"kind": "message", "text": " "}}, headers=h
        ).status_code
        == 400
    )
    r = client.http.post("/v1/turn", json={"session_id": "s", "turn": {"kind": "confirm"}}, headers=h)
    assert r.status_code == 400 and r.get_json()["error"]["code"] == "bad_proposal"
    assert client.http.post("/v1/turn", json={"turn": {}}, headers=h).status_code == 400


def test_AG_05_service_token_required_when_configured(app_factory):
    c = Client(app_factory(service_token="svc-secret"))
    s = c.login("intern.anna")
    body = {"session_id": "s", "turn": {"kind": "message", "text": "привет"}, "user_context": s.ctx}
    assert c.http.post("/v1/turn", json=body, headers=s.headers).status_code == 401
    ok = c.http.post("/v1/turn", json=body, headers={**s.headers, "X-Service-Token": "svc-secret"})
    assert ok.status_code == 200


def test_AG_06_config_endpoint_per_role(client, anna):
    cfg = client.http.get("/v1/agent/config", headers=anna.headers).get_json()
    assert cfg["provider"] == "stub" and cfg["prompt_version"] == "system.v1"
    assert "approvals.list_pending" not in cfg["read_tools"]
    assert "absence.create_request" in cfg["write_tools_via_confirmation"]
    assert cfg["mcp_servers"]["absence"] == "inprocess"


def test_AG_07_every_stream_has_exactly_one_terminal_event(anna):
    for msg in ("привет", "хочу отгул в пятницу", "покажи отпуск коллеги", "как проходит код-ревью"):
        names = [e.name for e in anna.say(msg)]
        assert names[0] == "status" and names[-1] in ("done", "error")
        assert sum(n in ("done", "error") for n in names) == 1


def test_AG_08_request_id_is_echoed(client, anna):
    r = client.http.post(
        "/v1/turn",
        json={"session_id": "s", "turn": {"kind": "message", "text": "привет"}, "user_context": anna.ctx},
        headers={**anna.headers, "X-Request-Id": "rid-42"},
    )
    assert r.headers["X-Request-Id"] == "rid-42" and '"request_id":"rid-42"' in r.get_data(as_text=True)


def test_AG_09_clock_override_for_demo(anna):
    ev = anna.say("хочу отгул завтра", today="2026-11-11")
    assert first(ev, "action_proposal")["draft"]["args"]["start_date"] == "2026-11-12"


def test_AG_10_faq_draft_from_mentor_answer_R10(client):
    m = client.login("mentor.oleg")
    r = client.http.post(
        "/v1/kb/faq-draft", json={"question": "Где стенд?", "answer": "Стенд — в разделе DLV."}, headers=m.headers
    ).get_json()
    assert r["doc_id"].startswith("faq-delivery-") and "source_type: test" in r["markdown"]
    assert "teams: [delivery]" in r["markdown"] and "origin: escalation" in r["markdown"]
    anna = client.login("intern.anna")
    assert (
        client.http.post("/v1/kb/faq-draft", json={"question": "q", "answer": "a"}, headers=anna.headers).status_code
        == 403
    )
    assert (
        client.http.post(
            "/v1/kb/faq-draft", json={"question": "q", "answer": "a", "team_id": "search"}, headers=m.headers
        ).status_code
        == 403
    )
    assert (
        client.http.post("/v1/kb/faq-draft", json={"question": " ", "answer": "a"}, headers=m.headers).status_code
        == 400
    )


def test_AG_11_internal_error_becomes_error_event(anna, monkeypatch):
    def boom(*a, **k):  # noqa: ANN002, ANN003
        raise RuntimeError("boom")

    monkeypatch.setattr("agent.flows.qa.handle_smalltalk", boom)
    ev = anna.say("привет")
    assert ev[-1].name == "error" and ev[-1].data["code"] == "internal"


def test_AG_12_tool_failure_is_retryable_error(app_factory):
    c = Client(app_factory(mock_faults="access.list_required=error:1"))
    s = c.login("intern.anna")
    ev = s.say("какие доступы мне нужны")
    assert ev[-1].name == "error" and ev[-1].data["retryable"] is True
    assert first(s.say("какие доступы мне нужны"), "action_proposal") is not None  # повтор работает


def test_AG_13_expired_jwt(client):
    settings = make_settings()
    tok = mint_token(
        settings.jwt_secret,
        {"sub": "intern.anna", "role": "intern", "team_id": "delivery", "exp": int(time.time()) - 10},
    )
    r = client.http.get("/v1/agent/config", headers={"Authorization": f"Bearer {tok}"})
    assert r.status_code == 401


def test_AG_14_dev_endpoints(client, anna):
    assert client.http.get("/dev").status_code == 200
    assert any(u["login"] == "intern.anna" for u in client.http.get("/dev/users").get_json())
    assert client.http.post("/dev/login", json={"login": "nobody"}).status_code == 401
    assert (
        client.http.post(
            "/dev/plan/task", json={"checklist_item_id": "CL-05", "status": "weird"}, headers=anna.headers
        ).status_code
        == 400
    )
    assert client.http.post("/dev/confirm", json={"proposal": {}}, headers=anna.headers).status_code == 400
    assert client.http.post("/dev/reset", json={}).get_json()["status"] == "reset"


def test_AG_15_unknown_role_in_token(client):
    tok = mint_token(make_settings().jwt_secret, {"sub": "x", "role": "admin", "team_id": "delivery"})
    assert client.http.get("/v1/agent/config", headers={"Authorization": f"Bearer {tok}"}).status_code == 401


def test_S2_22_faq_from_mentor_answer_has_no_names_or_dates_R10(client):
    from agent.text import generalize

    m = client.login("mentor.oleg")
    r = client.http.post(
        "/v1/kb/faq-draft",
        json={
            "question": "Олег, привет! Можно ли Анне взять отгул 13 ноября перед релизом?",
            "answer": "Анна, да, только предупреди Марию Лебедеву за два дня, в пятницу согласует.",
        },
        headers=m.headers,
    ).get_json()
    text = r["title"] + r["body"] + r["markdown"]
    for bad in ("Анн", "Олег", "Мари", "Лебедев", "13 ноября", "пятниц", "привет"):
        assert bad not in text, bad
    assert r["title"].startswith("Можно ли взять отгул в нужный день") and "предупреди за два дня" in r["body"]
    names = ["Анна Котова"]
    assert generalize("Отчёт сдаём 20.11.2026, а завтра — с 1 по 14 декабря.", names) == (
        "Отчёт сдаём в нужный день, а в нужный день — в нужный день."
    )
    assert generalize("Как настроить VPN?", names) == "Как настроить VPN?"
