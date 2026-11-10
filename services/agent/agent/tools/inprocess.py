"""In-process mocks of the corporate MCP servers (absence, approvals, access, checklist, directory, kb).

Used when MCP_<SERVER>_URL is empty: standalone demo, unit tests, eval, and until B's Go servers implement a tool.
Inputs and outputs follow contracts/schemas/*.json exactly (B's contract), errors follow contracts/mcp-tools.md:
every mock verifies the user JWT ITSELF (ADR-003 layer 3), checks confirmation tokens on writes and is idempotent.
"""

from __future__ import annotations

import copy
import threading
from datetime import date, datetime, timedelta, timezone
from pathlib import Path
from typing import Any

import yaml

from .. import clock
from ..auth import decode_token
from ..canonical import args_hash, verify_confirmation_token
from ..dates import working_days
from ..errors import ToolDenied, ToolError, ToolValidationError
from ..retrieval import KnowledgeBase

PERIODS = [("week_1", 7), ("week_2", 14), ("month_1", 30), ("month_2", 90)]  # = B's platform/period.Current
TOOL_ROLES = {  # mirrors "roles" in contracts/schemas/*.json
    "absence.get_policy": {"intern", "mentor", "lead"},
    "absence.list_my_requests": {"intern", "mentor", "lead"},
    "absence.validate_request": {"intern", "mentor", "lead"},
    "absence.create_request": {"intern", "mentor", "lead"},
    "absence.get_request": {"intern", "mentor", "lead"},
    "approvals.list_pending": {"lead"},
    "approvals.decide": {"lead"},
    "access.list_required": {"intern", "mentor"},
    "access.list_my_access": {"intern", "mentor"},
    "access.list_team_channels": {"intern", "mentor"},
    "access.request_access": {"intern", "mentor"},
}
ALLOWED_ARGS = {
    "absence.list_my_requests": {"status"},
    "absence.validate_request": {"type", "start_date", "end_date"},
    "absence.create_request": {"type", "start_date", "end_date", "comment", "idempotency_key", "confirmation_token"},
    "absence.get_request": {"id"},
    "approvals.decide": {"approval_id", "decision", "comment", "idempotency_key", "confirmation_token"},
    "access.request_access": {"resource_id", "reason", "idempotency_key", "confirmation_token"},
    "checklist.get_plan_template": {"role"},
    "kb.search": {"query", "k"},
}


def parse_faults(spec: str) -> dict[str, list[Any]]:
    """MOCK_FAULTS="absence.create_request=timeout_after:1,access.request_access=error:2"."""
    faults: dict[str, list[Any]] = {}
    for part in filter(None, (p.strip() for p in (spec or "").split(","))):
        tool, _, rule = part.partition("=")
        kind, _, count = rule.partition(":")
        faults[tool.strip()] = [kind.strip(), int(count or 1)]
    return faults


def _now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class MockServices:
    """Shared in-memory state of all mock services."""

    def __init__(
        self,
        fixtures_path: Path,
        checklist_path: Path,
        kb: KnowledgeBase,
        jwt_secret: str,
        confirmation_secret: str,
        faults: str = "",
    ) -> None:
        self.fixtures = yaml.safe_load(Path(fixtures_path).read_text(encoding="utf-8"))
        self.checklist = yaml.safe_load(Path(checklist_path).read_text(encoding="utf-8"))
        self.kb = kb
        self.jwt_secret = jwt_secret
        self.confirmation_secret = confirmation_secret
        self.faults = parse_faults(faults)
        self._lock = threading.RLock()
        self.reset()

    def reset(self) -> None:
        with self._lock:
            self.employees: dict[str, dict[str, Any]] = copy.deepcopy(self.fixtures["employees"])
            self.absences: list[dict[str, Any]] = copy.deepcopy(self.fixtures.get("absence_requests", []))
            self.accesses: list[dict[str, Any]] = copy.deepcopy(self.fixtures.get("access_requests", []))
            self.approvals: list[dict[str, Any]] = []
            for r in self.absences:
                if r["status"] == "pending_approval":
                    self._open_approval(
                        "absence_request", r["id"], r["employee_id"], r["approver_id"], f"Отгул {self._dates(r)}"
                    )
            self.idempotency: dict[str, dict[str, Any]] = {}
            self.seq = {"ABS": 100 + len(self.absences), "ACC": 40 + len(self.accesses), "APR": 1}

    # ------------------------------------------------------------------ helpers
    def _next_id(self, prefix: str) -> str:
        self.seq[prefix] += 1
        return f"{prefix}-{self.seq[prefix]:06d}" if prefix != "APR" else f"APR-{self.seq[prefix]:08x}"

    def name(self, employee_id: str | None) -> str:
        return self.employees.get(employee_id or "", {}).get("display_name", "")

    @staticmethod
    def _dates(r: dict[str, Any]) -> str:
        s, e = date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"])
        return s.strftime("%d.%m.%Y") if s == e else f"{s.strftime('%d.%m.%Y')}–{e.strftime('%d.%m.%Y')}"

    def _open_approval(self, kind: str, ref_id: str, requester: str, approver: str, summary: str) -> None:
        self.approvals.append(
            {
                "id": f"APR-{len(self.approvals) + 1:08x}",
                "kind": kind,
                "ref_id": ref_id,
                "requester_id": requester,
                "approver_id": approver,
                "summary": summary,
                "status": "pending",
                "comment": "",
                "created_at": _now_iso(),
            }
        )

    def _fault(self, tool: str, phase: str) -> None:
        rule = self.faults.get(tool)
        if not rule or rule[1] <= 0:
            return
        kind = rule[0]
        if (phase == "before" and kind in ("timeout", "error")) or (phase == "after" and kind == "timeout_after"):
            rule[1] -= 1
            if kind == "error":
                raise ToolError("Сервис вернул ошибку. Попробуй ещё раз.", code="service_unavailable")
            raise ToolError(
                "Сервис не ответил вовремя. Попробуй ещё раз — дубль не создастся.",
                code="service_timeout",
                http_status=504,
            )

    def _check_confirmation(self, claims: dict[str, Any], args: dict[str, Any]) -> None:
        check = verify_confirmation_token(
            self.confirmation_secret,
            args.get("confirmation_token", ""),
            claims["sub"],
            args.get("idempotency_key", ""),
            args_hash(args),
        )
        if not check.ok:
            raise ToolDenied(f"Подтверждение недействительно ({check.reason}).", code="confirmation_invalid")

    def _replay(self, key: str) -> dict[str, Any] | None:
        stored = self.idempotency.get(key)
        return {**stored, "replayed": True} if stored else None

    # ------------------------------------------------------------------ dispatch
    def call(self, tool: str, args: dict[str, Any], user_token: str) -> Any:
        claims = decode_token(self.jwt_secret, user_token)  # the mock service authenticates the user itself
        roles = TOOL_ROLES.get(tool)
        if roles is not None and claims["role"] not in roles:
            raise ToolDenied("Операция недоступна.")  # forbidden never reveals whether a resource exists
        allowed = ALLOWED_ARGS.get(tool, set())
        unknown = set(args) - allowed
        if unknown:
            raise ToolValidationError(f"Неизвестные поля: {', '.join(sorted(unknown))}.")  # identity only from JWT
        handler = getattr(self, "t_" + tool.replace(".", "__"), None)
        if handler is None:
            raise ToolError(f"Неизвестный инструмент {tool}", code="bad_request", retryable=False, http_status=400)
        with self._lock:
            self._fault(tool, "before")
            result = handler(claims, dict(args))
            self._fault(tool, "after")
            return result

    # ------------------------------------------------------------------ directory
    def t_directory__whoami(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        e = self.employees.get(c["sub"], {})
        out = {
            "employee_id": c["sub"],
            "display_name": e.get("display_name", ""),
            "role": c["role"],
            "team_id": c.get("team_id", ""),
            "team_name": self.fixtures["teams"].get(c.get("team_id"), {}).get("name", ""),
        }
        if "cohort_days_ago" in e:
            start = clock.today() - timedelta(days=e["cohort_days_ago"])
            out["cohort_start"] = start.isoformat()
            out["current_period"] = current_period(start, clock.today())
        return out

    def t_directory__get_my_contacts(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        e = self.employees.get(c["sub"], {})
        items = []
        for role, field in (
            ("mentor", "mentor_id"),
            ("lead", "lead_id"),
            ("hrbp", "hrbp_id"),
            ("curator", "curator_id"),
        ):
            pid = e.get(field)
            if pid:
                p = self.employees[pid]
                items.append(
                    {
                        "role": role,
                        "employee_id": pid,
                        "display_name": p["display_name"],
                        "channel": p.get("channel", ""),
                        "ask_about": self.fixtures["ask_about"][role],
                    }
                )
        return {"items": items}

    # ------------------------------------------------------------------ checklist
    def t_checklist__get_plan_template(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        role = a.get("role", "intern")
        if role not in ("intern", "mentor"):
            raise ToolValidationError("role должна быть intern или mentor.")
        keep = ("id", "role", "period", "title", "description", "steps", "scenarios", "human_only")
        items = [{k: i[k] for k in keep if k in i} for i in self.checklist["items"] if i["role"] in (role, "both")]
        return {"periods": self.checklist["periods"], "items": items}

    # ------------------------------------------------------------------ absence
    def t_absence__get_policy(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        doc = self.kb.by_id[self.fixtures["absence_policy"]["doc_id"]]
        return {"doc_id": doc.doc_id, "title": doc.title, "text": doc.body, "source_type": "test", "url": doc.url}

    def _absence_view(self, r: dict[str, Any]) -> dict[str, Any]:
        keys = ("id", "type", "start_date", "end_date", "comment", "status", "approver_id")
        return {
            **{k: r.get(k, "") for k in keys},
            "approver_display_name": self.name(r["approver_id"]),
            "created_at": r.get("created_at", "2026-11-01T09:00:00Z"),
        }

    def t_absence__list_my_requests(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        items = [
            self._absence_view(r)
            for r in self.absences
            if r["employee_id"] == c["sub"]  # own records only
            and (not a.get("status") or r["status"] == a["status"])
        ]
        return {"items": items}

    def t_absence__get_request(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        r = next((r for r in self.absences if r["id"] == a.get("id")), None)
        if r is None or r["employee_id"] != c["sub"]:
            raise ToolDenied("Операция недоступна.")
        return self._absence_view(r)

    def _validate(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        problems: list[str] = []
        pol = self.fixtures["absence_policy"]
        try:
            start, end = date.fromisoformat(a["start_date"]), date.fromisoformat(a["end_date"])
        except (KeyError, ValueError):
            return {"valid": False, "problems": ["Не удалось разобрать даты."]}
        if a.get("type", "day_off") != "day_off":
            problems.append("В сервисе отсутствий через buddy оформляется только отгул.")
        wd = working_days(start, end)
        today = clock.today()
        if end < start:
            problems.append("Дата окончания раньше даты начала.")
        if start < today:
            problems.append("Дата уже прошла — выбери дату в будущем.")
        elif start == today:
            problems.append(
                "Через сервис отгул оформляется начиная с завтрашнего дня — на сегодня предупреди руководителя лично."
            )
        if end >= start and wd == 0:
            problems.append("Это выходной или праздничный день — отгул не нужен.")
        if wd > pol["max_working_days"]:
            problems.append(
                f"Отгул — не больше {pol['max_working_days']} рабочих дней подряд. Для более долгого срока "
                "оформи отпуск в сервисе отсутствий."
            )
        for r in self.absences:
            if (
                r["employee_id"] == c["sub"]
                and r["status"] in ("pending_approval", "approved")
                and not (end < date.fromisoformat(r["start_date"]) or start > date.fromisoformat(r["end_date"]))
            ):
                problems.append(f"Пересекается с заявкой {r['id']} ({self._dates(r)}).")
        approver = self.employees.get(c["sub"], {}).get("lead_id") or ""
        out: dict[str, Any] = {"valid": not problems, "problems": problems}
        if approver:
            out |= {"approver_id": approver, "approver_display_name": self.name(approver)}
        return out

    def t_absence__validate_request(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        return self._validate(c, a)

    def t_absence__create_request(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        self._check_confirmation(c, a)
        key = f"absence:{a['idempotency_key']}"
        if (replay := self._replay(key)) is not None:
            return replay
        v = self._validate(c, a)
        if not v["valid"]:
            raise ToolValidationError(" ".join(v["problems"]))
        approver = v.get("approver_id") or ""
        rec = {
            "id": self._next_id("ABS"),
            "employee_id": c["sub"],
            "type": "day_off",
            "start_date": a["start_date"],
            "end_date": a["end_date"],
            "comment": a.get("comment", ""),
            "approver_id": approver,
            "status": "pending_approval" if approver else "approved",
            "created_at": _now_iso(),
        }
        self.absences.append(rec)
        if approver:
            self._open_approval("absence_request", rec["id"], c["sub"], approver, f"Отгул {self._dates(rec)}")
        result = {**self._absence_view(rec), "replayed": False}
        self.idempotency[key] = result
        return result

    # ------------------------------------------------------------------ approvals
    def _approval_view(self, p: dict[str, Any]) -> dict[str, Any]:
        keys = ("id", "kind", "ref_id", "requester_id", "summary", "status", "comment", "created_at")
        return {**{k: p[k] for k in keys}, "requester_display_name": self.name(p["requester_id"])}

    def t_approvals__list_pending(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        return {
            "items": [
                self._approval_view(p)
                for p in self.approvals
                if p["approver_id"] == c["sub"] and p["status"] == "pending"
            ]
        }

    def t_approvals__decide(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        self._check_confirmation(c, a)
        key = f"decide:{a['idempotency_key']}"
        if (replay := self._replay(key)) is not None:
            return replay
        if a.get("decision") not in ("approved", "rejected"):
            raise ToolValidationError("Решение должно быть approved или rejected.")
        p = next((p for p in self.approvals if p["id"] == a.get("approval_id")), None)
        if p is None or p["approver_id"] != c["sub"]:  # manager acts only on own team's requests
            raise ToolDenied("Операция недоступна.")
        if p["status"] != "pending":
            raise ToolValidationError("По этой заявке решение уже принято.", code="conflict")
        p.update(status=a["decision"], comment=a.get("comment", ""))
        if p["kind"] == "absence_request":
            ref = next(r for r in self.absences if r["id"] == p["ref_id"])
            ref["status"] = "approved" if a["decision"] == "approved" else "rejected"
        else:
            ref = next(r for r in self.accesses if r["id"] == p["ref_id"])
            ref["status"] = "granted" if a["decision"] == "approved" else "rejected"
        result = {**self._approval_view(p), "replayed": False}
        self.idempotency[key] = result
        return result

    # ------------------------------------------------------------------ access
    def _required_ids(self, team_id: str) -> list[str]:
        return [
            rid
            for rid, r in self.fixtures["access_resources"].items()
            if r["team_id"] in (None, team_id) and r["kind"] != "channel"
        ]

    def _access_item(self, user_id: str, rid: str) -> dict[str, Any]:
        r = self.fixtures["access_resources"][rid]
        req = next(
            (x for x in reversed(self.accesses) if x["employee_id"] == user_id and x["resource_id"] == rid), None
        )
        return {
            "resource_id": rid,
            "name": r["name"],
            "kind": r["kind"],
            "elevated": bool(r["elevated"]),
            "status": req["status"] if req else "not_requested",
            "checklist_item_id": r.get("checklist_item_id", ""),
        }

    def t_access__list_required(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        return {"items": [self._access_item(c["sub"], rid) for rid in self._required_ids(c.get("team_id", ""))]}

    def t_access__list_my_access(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        rids = list(dict.fromkeys(x["resource_id"] for x in self.accesses if x["employee_id"] == c["sub"]))
        return {"items": [self._access_item(c["sub"], rid) for rid in rids]}

    def t_access__list_team_channels(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        team = c.get("team_id")
        return {
            "items": [
                {"name": r["name"], "purpose": r.get("purpose", ""), "closed": bool(r["elevated"])}
                for r in self.fixtures["access_resources"].values()
                if r["kind"] == "channel" and r["team_id"] in (None, team)
            ]
        }

    def t_access__request_access(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        self._check_confirmation(c, a)
        key = f"access:{a['idempotency_key']}"
        if (replay := self._replay(key)) is not None:
            return replay
        rid = a.get("resource_id", "")
        res = self.fixtures["access_resources"].get(rid)
        if res is None or res["team_id"] not in (None, c.get("team_id")):
            raise ToolDenied("Операция недоступна.")  # other team's resource: same answer as a missing one
        current = self._access_item(c["sub"], rid)["status"]
        if current in ("granted", "pending_approval"):
            raise ToolValidationError("Доступ уже выдан или ждёт согласования.", code="conflict")
        approver = self.employees.get(c["sub"], {}).get("lead_id") if res["elevated"] else None
        rec = {
            "id": self._next_id("ACC"),
            "employee_id": c["sub"],
            "resource_id": rid,
            "reason": a.get("reason", ""),
            "status": "pending_approval" if res["elevated"] else "granted",
            "approver_id": approver,
            "created_at": _now_iso(),
        }
        self.accesses.append(rec)
        if res["elevated"] and approver:
            self._open_approval("access_request", rec["id"], c["sub"], approver, f"Доступ: {res['name']}")
        result = {
            "id": rec["id"],
            "resource_id": rid,
            "resource_name": res["name"],
            "status": rec["status"],
            "approver_id": approver,
            "checklist_item_id": res.get("checklist_item_id"),
            "created_at": rec["created_at"],
            "replayed": False,
        }
        self.idempotency[key] = result
        return result

    # ------------------------------------------------------------------ kb
    def t_kb__search(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        query = str(a.get("query", ""))
        if len(query) < 2:
            raise ToolValidationError("Слишком короткий запрос.")
        hits = self.kb.search(
            query, c["role"], c.get("team_id", ""), k=min(int(a.get("k", 5)), 10), team_boost=1.0, stage_boost=1.0
        )  # plain ACL-filtered ranking, like mcp-kb; the agent re-ranks by context
        return {
            "items": [
                {
                    "doc_id": h.doc.doc_id,
                    "title": h.doc.title,
                    "snippet": h.snippet,
                    "source_type": "test",
                    "url": h.doc.url,
                    "score": h.score,
                }
                for h in hits
            ]
        }

    # ------------------------------------------------------------------ dev/demo helpers (not MCP tools)
    def mentees_of(self, mentor_id: str) -> list[dict[str, Any]]:
        out = []
        for eid, e in self.employees.items():
            if e.get("mentor_id") == mentor_id and e["role"] == "intern":
                out.append(
                    {
                        "user_id": eid,
                        "display_name": e["display_name"],
                        "accesses": [
                            {"name": i["name"], "status": i["status"], "elevated": i["elevated"]}
                            for i in (self._access_item(eid, r) for r in self._required_ids(e["team_id"]))
                        ],
                    }
                )
        return out


def current_period(cohort_start: date, today: date) -> str:
    days = (today - cohort_start).days
    for period, limit in PERIODS:
        if days < limit:
            return period
    return "after_3_months"


class InProcessServer:
    """Adapter so each MCP server name maps to the shared MockServices state."""

    def __init__(self, services: MockServices, server: str) -> None:
        self.services = services
        self.server = server

    def call(self, tool: str, args: dict[str, Any], user_token: str) -> Any:
        if tool.split(".", 1)[0] != self.server:
            raise ToolError(f"{tool} не принадлежит серверу {self.server}", code="bad_request", retryable=False)
        return self.services.call(tool, args, user_token)
