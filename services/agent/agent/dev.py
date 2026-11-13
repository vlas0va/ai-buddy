"""Эндпоинты только для разработки (AGENT_DEV_MODE=true): консоль, чтобы показать и проверить агента до готовности ядра и
веба.

Они имитируют то, что делает ядро: вход тестовых учётных записей (мок-IdP), сборку user_context с планом и выпуск токена
подтверждения по «Отправить». Никогда не включаются в профилях compose, которыми пользуется жюри.
"""

from __future__ import annotations

from datetime import date, timedelta
from pathlib import Path
from typing import Any

from flask import Flask, Response, jsonify, request

from .auth import bearer, decode_token, mint_token
from .canonical import args_hash, create_confirmation_token
from .config import Settings
from .errors import BadRequest, Unauthorized
from .orchestrator import Agent
from .tools.inprocess import current_period

PERIOD_DUE = {"week_1": 7, "week_2": 14, "month_1": 30, "month_2": 60, "after_3_months": 100}  # как в демо-данных B


def _plan(agent: Agent, user_id: str, e: dict[str, Any], overrides: dict[str, str]) -> list[dict[str, Any]]:
    mocks = agent.mocks
    assert mocks is not None
    roles = {"intern": ("intern", "both"), "mentor": ("mentor", "both")}.get(e["role"])
    if not roles:
        return []
    start = date.today() - timedelta(days=e.get("cohort_days_ago", 10))
    statuses = {**mocks.fixtures.get("demo_plan_status", {}).get(user_id, {}), **overrides}
    tasks = []
    for item in mocks.checklist["items"]:
        if item["role"] not in roles:
            continue
        due = start + timedelta(days=PERIOD_DUE[item["period"]])
        st = statuses.get(item["id"], "not_started")
        if st != "done" and due < date.today():
            st = "overdue"
        tasks.append(
            {
                "task_id": f"t-{user_id}-{item['id']}",
                "checklist_item_id": item["id"],
                "title": item["title"],
                "status": st,
                "period": item["period"],
                "due_at": due.isoformat(),
                "human_only": bool(item.get("human_only")),
            }
        )
    return tasks


def register_dev(app: Flask, agent: Agent, settings: Settings) -> None:
    overrides: dict[str, dict[str, str]] = {}
    html = (Path(__file__).parent / "static" / "dev.html").read_text(encoding="utf-8")

    def _context(user_id: str) -> dict[str, Any]:
        """То, что ядро отправляет как user_context (contracts/agent-stream.md), + полный план и подопечные для консоли."""
        assert agent.mocks is not None
        e = agent.mocks.employees[user_id]
        ctx: dict[str, Any] = {
            "user_id": user_id,
            "display_name": e["display_name"],
            "role": e["role"],
            "team_id": e["team_id"],
            "location": e.get("location", ""),
            "plan": {"tasks": _plan(agent, user_id, e, overrides.get(user_id, {}))},
        }
        if "cohort_days_ago" in e and e["role"] == "intern":
            ctx["current_period"] = current_period(date.today() - timedelta(days=e["cohort_days_ago"]), date.today())
        if e["role"] in ("mentor", "lead"):
            mentor_ids = (
                [user_id]
                if e["role"] == "mentor"
                else [
                    k for k, v in agent.mocks.employees.items() if v["role"] == "mentor" and v.get("lead_id") == user_id
                ]
            )
            ctx["mentees"] = [m for mid in mentor_ids for m in agent.mocks.mentees_of(mid)]
            for m in ctx["mentees"]:
                m["open_tasks"] = [
                    t
                    for t in _plan(
                        agent, m["user_id"], agent.mocks.employees[m["user_id"]], overrides.get(m["user_id"], {})
                    )
                    if t["status"] != "done"
                ]
        return ctx

    @app.get("/dev")
    def dev_console() -> Response:
        return Response(html, mimetype="text/html")

    @app.get("/dev/users")
    def dev_users() -> Response:
        assert agent.mocks is not None
        order = {"intern": 0, "mentor": 1, "lead": 2, "hrbp": 3, "alumni": 4}
        users = [
            {"login": uid, "name": e["display_name"], "role": e["role"], "team_id": e["team_id"]}
            for uid, e in agent.mocks.employees.items()
            if e["role"] in order
        ]
        return jsonify(sorted(users, key=lambda u: (order[u["role"]], u["login"])))

    @app.post("/dev/login")
    def dev_login() -> Response:
        assert agent.mocks is not None
        login = (request.get_json(silent=True) or {}).get("login", "")
        e = agent.mocks.employees.get(login)
        if e is None or e["role"] == "curator":
            raise Unauthorized("Неизвестная тестовая учётка.")
        claims = {"sub": login, "role": e["role"], "team_id": e["team_id"], "name": e["display_name"]}
        if "cohort_days_ago" in e:
            claims["cohort_start"] = (date.today() - timedelta(days=e["cohort_days_ago"])).isoformat()
        return jsonify({"token": mint_token(settings.jwt_secret, claims), "user_context": _context(login)})

    @app.get("/dev/context")
    def dev_context() -> Response:
        claims = decode_token(settings.jwt_secret, bearer(request.headers))
        return jsonify(_context(claims["sub"]))

    @app.post("/dev/plan/task")
    def dev_task() -> Response:
        claims = decode_token(settings.jwt_secret, bearer(request.headers))
        body = request.get_json(silent=True) or {}
        if body.get("status") not in ("not_started", "in_progress", "waiting_approval", "done"):
            raise BadRequest("Неизвестный статус.")
        overrides.setdefault(claims["sub"], {})[str(body.get("checklist_item_id"))] = body["status"]
        return jsonify(_context(claims["sub"]))

    @app.post("/dev/confirm")
    def dev_confirm() -> Response:
        """То, что делает ядро в POST /api/actions/proposals/{id}/confirm."""
        claims = decode_token(settings.jwt_secret, bearer(request.headers))
        proposal = (request.get_json(silent=True) or {}).get("proposal") or {}
        draft = proposal.get("draft") or {}
        if not proposal.get("proposal_id") or not isinstance(draft.get("args"), dict):
            raise BadRequest("Нужен proposal с draft.args.")
        tok = create_confirmation_token(
            settings.confirmation_secret, claims["sub"], proposal["proposal_id"], args_hash(draft["args"])
        )
        return jsonify({"confirmation_token": tok})

    @app.post("/dev/reset")
    def dev_reset() -> Response:
        assert agent.mocks is not None
        agent.mocks.reset()
        overrides.clear()
        return jsonify({"status": "reset"})
