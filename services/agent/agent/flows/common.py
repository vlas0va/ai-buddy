"""Shared turn context and helpers for flows."""

from __future__ import annotations

import re
import uuid
from collections.abc import Iterator
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any

from .. import clock
from ..auth import User
from ..canonical import args_hash
from ..config import AgentConfig
from ..errors import ToolError
from ..sse import Event, token
from ..state import StateStore
from ..tools.base import ToolGateway

PROPOSAL_TTL_S = 1800
PERIOD_LABELS = {
    "week_1": "1 неделя",
    "week_2": "2 неделя",
    "month_1": "1 месяц",
    "month_2": "2 месяц",
    "after_3_months": "После 3-х месяцев",
    "graduated": "Стажировка завершена",
}
PERIOD_ORDER = ["week_1", "week_2", "month_1", "month_2", "after_3_months"]
ROLE_LABELS = {
    "intern": "стажёр",
    "mentor": "наставник",
    "lead": "руководитель",
    "hrbp": "HR BP",
    "alumni": "выпускник",
}
CONTACT_LABELS = {"mentor": "наставник", "lead": "руководитель", "curator": "куратор стажировки", "hrbp": "HR BP"}
CANCEL = re.compile(r"^(отмен\w*|не\s+надо|не\s+нужно|стоп|передумал\w*|отбой|нет,?\s+не\s+надо)\b")
CONFIRM = re.compile(r"^(да|ок|окей|подтверждаю|отправляй|отправь|верно|все\s+верно|давай)\b")


@dataclass
class Turn:
    user: User
    gateway: ToolGateway
    config: AgentConfig
    store: StateStore
    session_id: str
    writer: Any
    kb: Any = None
    history: list[dict[str, str]] = field(default_factory=list)
    meta: dict[str, Any] = field(default_factory=dict)

    @property
    def state_key(self) -> str:
        return f"session:{self.user.user_id}:{self.session_id}"

    def state(self) -> dict[str, Any]:
        return self.store.get(self.state_key) or {}

    def save_state(self, value: dict[str, Any]) -> None:
        self.store.set(self.state_key, value, ttl_s=3600)

    def clear_state(self) -> None:
        self.store.delete(self.state_key)

    def contacts(self) -> dict[str, Any]:
        """directory.get_my_contacts → {role: {user_id, name, channel, ask_about}}."""
        if "contacts" not in self.meta:
            out: dict[str, Any] = {}
            try:
                for c in items(self.gateway.read("directory.get_my_contacts")):
                    out[c["role"]] = {
                        "user_id": c.get("employee_id"),
                        "name": c.get("display_name", ""),
                        "channel": c.get("channel", ""),
                        "ask_about": c.get("ask_about", []),
                    }
            except ToolError:
                pass
            self.meta["contacts"] = out
        return self.meta["contacts"]

    def contact(self, role: str) -> dict[str, Any] | None:
        return self.contacts().get(role)

    def task_for(self, *checklist_ids: str) -> str | None:
        for cid in checklist_ids:
            if any(t.checklist_item_id == cid for t in self.user.tasks):
                return cid
        return checklist_ids[0] if checklist_ids else None

    def make_proposal(
        self,
        kind: str,
        tool: str,
        args: dict[str, Any],
        title: str,
        summary: str,
        display: dict[str, Any],
        requires_approval: bool,
        checklist_item_id: str | None = None,
    ) -> dict[str, Any]:
        """action_proposal of contracts/agent-stream.md: draft = {tool, args, display}; args are signed by core."""
        pid = str(uuid.uuid4())  # = idempotency_key of the MCP write (format: uuid)
        h = args_hash(args)
        exp = datetime.now(timezone.utc) + timedelta(seconds=PROPOSAL_TTL_S)
        proposal = {
            "proposal_id": pid,
            "kind": kind,
            "summary": summary,
            "title": title,
            "draft": {"tool": tool, "args": args, "display": display},
            "args_hash": h,
            "requires_approval": requires_approval,
            "task_checklist_item_id": checklist_item_id,
            "expires_at": exp.strftime("%Y-%m-%dT%H:%M:%SZ"),
        }
        # Registry so the confirm turn can check the proposal really came from this agent for this user.
        self.store.set(
            f"proposal:{pid}",
            {
                "user_id": self.user.user_id,
                "args_hash": h,
                "tool": tool,
                "kind": kind,
                "state_key": self.state_key,
                "checklist_item_id": checklist_item_id,
                "summary": summary,
            },
            ttl_s=PROPOSAL_TTL_S,
        )
        return proposal


def items(result: Any) -> list[dict[str, Any]]:
    """MCP list tools return {"items": [...]} (contracts/schemas)."""
    if isinstance(result, dict):
        return list(result.get("items", []))
    return list(result or [])


def say(text: str, chunk: int = 48) -> Iterator[Event]:
    """Stream a prepared text as token events (word-boundary chunks)."""
    buf = ""
    for part in re.split(r"(\s+)", text):
        buf += part
        if len(buf) >= chunk:
            yield token(buf)
            buf = ""
    if buf:
        yield token(buf)


def person_name(p: dict[str, Any] | None, fallback: str = "") -> str:
    return (p or {}).get("name") or fallback


def first_name(p: dict[str, Any] | None, fallback: str = "") -> str:
    name = person_name(p, fallback)
    return name.split()[0] if name else fallback


def today_iso() -> str:
    return clock.today().isoformat()
