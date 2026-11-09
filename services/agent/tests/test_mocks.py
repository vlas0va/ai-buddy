"""Rights matrix of the in-process mocks (mirrors contracts/mcp-tools.md, which B's Go servers must also pass)."""

from __future__ import annotations

import pytest

from agent.auth import mint_token
from agent.canonical import args_hash, create_confirmation_token
from agent.errors import ToolDenied, ToolError, ToolValidationError
from agent.tools.inprocess import InProcessServer, current_period, parse_faults
from tests.conftest import make_settings


@pytest.fixture
def mocks(client):
    return client.agent.mocks


def tok(user, role, team):  # noqa: ANN001, ANN201
    return mint_token(make_settings().jwt_secret, {"sub": user, "role": role, "team_id": team})


ANNA = ("intern.anna", "intern", "delivery")
MARIA = ("lead.maria", "lead", "delivery")


def write_args(user, args, key="00000000-0000-0000-0000-000000000001"):  # noqa: ANN001, ANN201
    t = create_confirmation_token(make_settings().confirmation_secret, user, key, args_hash(args))
    return {**args, "idempotency_key": key, "confirmation_token": t}


def test_MCP_01_identity_from_jwt_only_unknown_fields_rejected(mocks):
    with pytest.raises(ToolValidationError):
        mocks.call(
            "absence.validate_request",
            {"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13", "employee_id": "intern.ivan"},
            tok(*ANNA),
        )


def test_MCP_02_write_without_or_with_foreign_confirmation(mocks):
    args = {"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13", "comment": ""}
    with pytest.raises(ToolDenied):
        mocks.call("absence.create_request", {**args, "idempotency_key": "k", "confirmation_token": "x.y"}, tok(*ANNA))
    foreign = write_args("intern.ivan", args)
    with pytest.raises(ToolDenied):
        mocks.call("absence.create_request", foreign, tok(*ANNA))


def test_MCP_03_idempotent_create(mocks):
    args = {"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13", "comment": ""}
    a = mocks.call("absence.create_request", write_args("intern.anna", args), tok(*ANNA))
    b = mocks.call("absence.create_request", write_args("intern.anna", args), tok(*ANNA))
    assert a["id"] == b["id"] and b["replayed"] is True and a["id"].startswith("ABS-")


def test_MCP_04_get_request_of_another_user_forbidden(mocks):
    with pytest.raises(ToolDenied):
        mocks.call("absence.get_request", {"id": "ABS-000100"}, tok(*ANNA))
    assert mocks.call("absence.get_request", {"id": "ABS-000100"}, tok("intern.ivan", "intern", "search"))["id"]


def test_MCP_05_intern_cannot_decide_lead_other_team_forbidden(mocks):
    with pytest.raises(ToolDenied):
        mocks.call("approvals.list_pending", {}, tok(*ANNA))
    pending_sergey = mocks.call("approvals.list_pending", {}, tok("lead.dmitry", "lead", "search"))["items"]
    args = {"approval_id": pending_sergey[0]["id"], "decision": "approved", "comment": ""}
    with pytest.raises(ToolDenied):
        mocks.call("approvals.decide", write_args("lead.maria", args), tok(*MARIA))
    bad = {"approval_id": pending_sergey[0]["id"], "decision": "maybe", "comment": ""}
    with pytest.raises(ToolValidationError):
        mocks.call(
            "approvals.decide",
            write_args("lead.dmitry", bad, key="00000000-0000-0000-0000-000000000009"),
            tok("lead.dmitry", "lead", "search"),
        )


def test_MCP_06_elevated_access_creates_approval_and_other_team_forbidden(mocks):
    r = mocks.call(
        "access.request_access",
        write_args("intern.anna", {"resource_id": "space-delivery-incidents", "reason": ""}),
        tok(*ANNA),
    )
    assert r["status"] == "pending_approval" and r["approver_id"] == "lead.maria"
    assert mocks.call("approvals.list_pending", {}, tok(*MARIA))["items"][0]["kind"] == "access_request"
    with pytest.raises(ToolDenied):
        mocks.call(
            "access.request_access",
            write_args(
                "intern.anna", {"resource_id": "space-search", "reason": ""}, key="00000000-0000-0000-0000-000000000002"
            ),
            tok(*ANNA),
        )
    with pytest.raises(ToolValidationError):  # already pending
        mocks.call(
            "access.request_access",
            write_args(
                "intern.anna",
                {"resource_id": "space-delivery-incidents", "reason": ""},
                key="00000000-0000-0000-0000-000000000003",
            ),
            tok(*ANNA),
        )


def test_MCP_07_kb_search_acl_and_validation(mocks):
    items = mocks.call("kb.search", {"query": "методичка наставника"}, tok(*ANNA))["items"]
    assert "mentor-handbook" not in [i["doc_id"] for i in items]
    with pytest.raises(ToolValidationError):
        mocks.call("kb.search", {"query": "a"}, tok(*ANNA))


def test_MCP_08_directory_checklist_whoami(mocks):
    me = mocks.call("directory.whoami", {}, tok(*ANNA))
    assert me["display_name"] == "Анна Котова" and me["current_period"] == "week_2"
    roles = [c["role"] for c in mocks.call("directory.get_my_contacts", {}, tok(*ANNA))["items"]]
    assert roles == ["mentor", "lead", "hrbp", "curator"]
    tpl = mocks.call("checklist.get_plan_template", {"role": "mentor"}, tok(*ANNA))
    assert {i["role"] for i in tpl["items"]} <= {"mentor", "both"}
    with pytest.raises(ToolValidationError):
        mocks.call("checklist.get_plan_template", {"role": "lead"}, tok(*ANNA))
    assert mocks.call("access.list_my_access", {}, tok(*ANNA))["items"][0]["resource_id"] == "confluence"
    assert mocks.call("absence.list_my_requests", {"status": "approved"}, tok(*ANNA))["items"] == []
    assert mocks.call("absence.get_policy", {}, tok(*ANNA))["doc_id"] == "common-dayoff-procedure"


def test_MCP_09_faults_parser_and_server_adapter(mocks):
    assert parse_faults("a.b=timeout:2, c.d=error") == {"a.b": ["timeout", 2], "c.d": ["error", 1]}
    with pytest.raises(ToolError):
        InProcessServer(mocks, "kb").call("absence.get_policy", {}, tok(*ANNA))
    with pytest.raises(ToolError):
        mocks.call("absence.nope", {}, tok(*ANNA))
    from datetime import date

    assert current_period(date(2026, 1, 1), date(2026, 12, 1)) == "after_3_months"


def test_MCP_10_validation_problems(mocks):
    v = mocks.call("absence.validate_request", {"type": "day_off", "start_date": "bad", "end_date": "x"}, tok(*ANNA))
    assert v["valid"] is False
    v = mocks.call(
        "absence.validate_request",
        {"type": "vacation", "start_date": "2026-11-17", "end_date": "2026-11-16"},
        tok(*ANNA),
    )
    assert len(v["problems"]) >= 2
    with pytest.raises(ToolValidationError):
        args = {"type": "day_off", "start_date": "2026-11-14", "end_date": "2026-11-14", "comment": ""}
        mocks.call("absence.create_request", write_args("intern.anna", args), tok(*ANNA))
