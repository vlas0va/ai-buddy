"""User identity from the mock-IdP JWT; the agent never trusts role/team from the request body (ADR-003)."""

from __future__ import annotations

import hmac
import time
from dataclasses import dataclass, field
from typing import Any

import jwt

from .errors import Forbidden, Unauthorized

ROLES = ("intern", "mentor", "lead", "hrbp", "alumni", "hr_admin")


@dataclass
class PlanTask:
    task_id: str
    title: str
    status: str = "not_started"
    checklist_item_id: str | None = None
    period: str | None = None
    due_at: str | None = None
    human_only: bool = False
    parent_task_id: str | None = None

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> PlanTask:
        return cls(
            task_id=str(d.get("task_id") or d.get("id") or d.get("checklist_item_id") or ""),
            title=str(d.get("title", "")),
            status=str(d.get("status", "not_started")),
            checklist_item_id=d.get("checklist_item_id"),
            period=d.get("period"),
            due_at=d.get("due_at"),
            human_only=bool(d.get("human_only", False)),
            parent_task_id=d.get("parent_task_id"),
        )


@dataclass
class User:
    user_id: str
    role: str
    team_id: str
    token: str
    display_name: str = ""
    stage: str | None = None
    tasks: list[PlanTask] = field(default_factory=list)
    flags: dict[str, bool] = field(default_factory=dict)
    mentees: list[dict[str, Any]] = field(default_factory=list)
    location: str = ""  # office city or «удалённо»: personal data the organizers allow buddy to use (Q&A 10.11)

    def flag_enabled(self, key: str | None) -> bool:
        return True if not key else self.flags.get(key, True)


def mint_token(secret: str, claims: dict[str, Any], ttl_s: int = 8 * 3600) -> str:
    """Dev/test helper mirroring mock IdP."""
    payload = dict(claims)
    payload.setdefault("exp", int(time.time()) + ttl_s)
    return jwt.encode(payload, secret, algorithm="HS256")


def decode_token(secret: str, token: str) -> dict[str, Any]:
    try:
        claims = jwt.decode(token, secret, algorithms=["HS256"], options={"require": ["sub", "exp"]})
    except jwt.PyJWTError as exc:
        raise Unauthorized("Сессия истекла или токен недействителен.") from exc
    if claims.get("role") not in ROLES:
        raise Unauthorized("Неизвестная роль в токене.")
    return claims


def bearer(headers: Any) -> str:
    value = headers.get("Authorization", "")
    if not value.startswith("Bearer "):
        raise Unauthorized()
    return value[7:].strip()


def check_service_token(expected: str, headers: Any) -> None:
    got = headers.get("X-Service-Token", "")
    if expected and not hmac.compare_digest(got.encode(), expected.encode()):  # bytes: a non-ASCII value must not crash
        raise Unauthorized("Неверный сервисный токен.")


def build_user(
    secret: str, token: str, user_context: dict[str, Any] | None, flags: dict[str, bool] | None = None
) -> User:
    claims = decode_token(secret, token)
    ctx = user_context or {}
    for key, claim in (("user_id", "sub"), ("role", "role"), ("team_id", "team_id")):
        if ctx.get(key) not in (None, "") and str(ctx[key]) != str(claims.get(claim)):
            raise Forbidden("Контекст пользователя не совпадает с токеном.", code="context_mismatch")
    # contracts/agent-stream.md: open_tasks (core) or plan.tasks (full plan); current_period (core) or stage.
    raw_tasks = (ctx.get("plan") or {}).get("tasks") or ctx.get("open_tasks") or []
    tasks = [PlanTask.from_dict(t) for t in raw_tasks]
    return User(
        user_id=str(claims["sub"]),
        role=str(claims["role"]),
        team_id=str(claims.get("team_id", "")),
        token=token,
        display_name=str(ctx.get("display_name") or claims.get("name") or ""),
        stage=ctx.get("current_period") or ctx.get("stage") or claims.get("cohort_stage"),
        tasks=tasks,
        flags=dict(flags or ctx.get("flags") or {}),
        mentees=list(ctx.get("mentees") or []),
        location=str(ctx.get("location") or "")[:60],
    )
