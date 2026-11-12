"""In-process mocks of the corporate MCP servers (absence, approvals, access, checklist, directory, kb, trips, consents).

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
    "absence.reschedule_request": {"intern", "mentor", "lead"},
    "approvals.list_pending": {"lead", "hr_admin"},
    "approvals.decide": {"lead", "hr_admin"},
    "access.list_required": {"intern", "mentor"},
    "access.list_my_access": {"intern", "mentor"},
    "access.list_team_channels": {"intern", "mentor"},
    "access.request_access": {"intern", "mentor"},
    "trips.get_policy": {"intern", "mentor", "lead"},
    "trips.list_my_requests": {"intern", "mentor", "lead"},
    "trips.validate_request": {"intern", "mentor", "lead"},
    "trips.create_request": {"intern", "mentor", "lead"},
    "consents.list": {"intern", "mentor", "lead", "hrbp", "alumni", "hr_admin"},
    "consents.sign": {"intern", "mentor", "lead", "hrbp", "alumni", "hr_admin"},
}
ABSENCE_TYPES = {"day_off": "Отгул", "vacation": "Отпуск", "absence": "Отсутствие", "parental_leave": "Декрет"}
MAX_VACATION_DAYS, VACATION_NOTICE_DAYS, MAX_ABSENCE_DAYS, MAX_DOCUMENTS = 28, 14, 5, 3  # = B's mocks/absence
LEVEL_LABELS = {"read": "чтение", "write": "запись", "admin": "администрирование"}
TRIP_OFFICES = ["Москва", "Санкт-Петербург", "Казань", "Екатеринбург", "Новосибирск"]  # = B's mocks/trips.Offices
TRIP_MIN_NOTICE_DAYS = 5
TRIP_MAX_DAYS = 14
ALLOWED_ARGS = {
    "absence.list_my_requests": {"status"},
    "absence.validate_request": {"type", "start_date", "end_date", "documents", "exclude_id"},
    "absence.create_request": {
        "type",
        "start_date",
        "end_date",
        "comment",
        "documents",
        "idempotency_key",
        "confirmation_token",
    },
    "absence.reschedule_request": {"id", "start_date", "end_date", "idempotency_key", "confirmation_token"},
    "absence.get_request": {"id"},
    "approvals.decide": {"approval_id", "decision", "comment", "idempotency_key", "confirmation_token"},
    "access.request_access": {"resource_id", "reason", "environment", "level", "idempotency_key", "confirmation_token"},
    "checklist.get_plan_template": {"role"},
    "kb.search": {"query", "k"},
    "trips.validate_request": {"city", "start_date", "end_date", "purpose"},
    "trips.create_request": {"city", "start_date", "end_date", "purpose", "idempotency_key", "confirmation_token"},
    "consents.sign": {"consent_id", "idempotency_key", "confirmation_token"},
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
            self.trips: list[dict[str, Any]] = []
            self.consent_types: list[dict[str, Any]] = copy.deepcopy(self.fixtures.get("consents", []))
            self.signatures: list[dict[str, Any]] = [
                {
                    "id": f"CNS-{i + 1:06d}",
                    "employee_id": emp,
                    "consent_id": t["consent_id"],
                    "via": "hr_portal",
                    "signed_at": "2026-10-27T09:00:00Z",
                }
                for i, (emp, t) in enumerate(
                    (emp, t) for emp in sorted(self.employees) for t in self.consent_types if not t["signable"]
                )
            ]
            for r in self.absences:
                if r["status"] == "pending_approval":
                    self._open_approval(
                        "absence_request", r["id"], r["employee_id"], r["approver_id"], self._abs_summary(r)
                    )
            self.idempotency: dict[str, dict[str, Any]] = {}
            self.seq = {
                "ABS": 100 + len(self.absences),
                "ACC": 40 + len(self.accesses),
                "APR": 1,
                "TRP": 199,
                "CNS": 299,
            }

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

    @classmethod
    def _abs_summary(cls, r: dict[str, Any]) -> str:
        return f"{ABSENCE_TYPES.get(r.get('type', 'day_off'), 'Отгул')} {cls._dates(r)}"

    def _open_approval(
        self, kind: str, ref_id: str, requester: str, approver: str, summary: str, stage: str = "lead"
    ) -> None:
        self.approvals.append(
            {
                "stage": stage,
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
            "documents": r.get("documents", []),
            "created_at": r.get("created_at", "2026-11-01T09:00:00Z"),
            "steps": self._absence_steps(r),
        }

    def _hr_admin(self) -> str:
        return next((k for k, e in sorted(self.employees.items()) if e.get("role") == "hr_admin"), "")

    def _absence_steps(self, r: dict[str, Any]) -> list[dict[str, Any]]:
        """lead approves → HR admin registers in the HR system → HR BP is notified (= B's absence.steps)."""
        hrbp = r.get("hrbp_id") or self.employees.get(r["employee_id"], {}).get("hrbp_id", "")
        lead = {"role": "lead", "display_name": self.name(r["approver_id"]), "status": "waiting", "at": None}
        admin = {
            "role": "hr_admin",
            "display_name": self.name(r.get("hr_admin_id") or self._hr_admin()),
            "status": "waiting",
            "at": None,
        }
        bp = {"role": "hrbp", "display_name": self.name(hrbp), "status": "waiting", "at": None}
        if r["status"] in ("approved", "pending_hr"):
            lead.update(status="approved", at=r.get("lead_decided_at") or r.get("decided_at"))
        elif r["status"] in ("rejected", "cancelled") and r.get("lead_decided_at"):
            lead.update(status="approved", at=r["lead_decided_at"])
            admin.update(status="rejected", at=r.get("decided_at"))
            bp["status"] = "skipped"
        elif r["status"] in ("rejected", "cancelled"):
            lead.update(status="rejected", at=r.get("decided_at"))
            admin["status"] = bp["status"] = "skipped"
        if r.get("registered_at"):
            admin.update(status="registered", at=r["registered_at"])
        if r.get("hrbp_notified_at"):
            bp.update(status="notified", at=r["hrbp_notified_at"])
        return [lead, admin, bp]

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
        try:
            start, end = date.fromisoformat(a["start_date"]), date.fromisoformat(a["end_date"])
        except (KeyError, ValueError):
            return {"valid": False, "problems": ["Не удалось разобрать даты."]}
        kind = a.get("type", "day_off")
        docs = a.get("documents") or []
        if kind not in ABSENCE_TYPES:
            raise ToolValidationError("Некорректные аргументы: неизвестный вид отсутствия.")
        if len(docs) > MAX_DOCUMENTS:
            raise ToolValidationError(f"Можно приложить не больше {MAX_DOCUMENTS} документов")
        problems += self._type_problems(kind, start, end, len(docs))
        for r in self.absences:
            if (
                r["employee_id"] == c["sub"]
                and r["id"] != a.get("exclude_id")
                and r["status"] in ("pending_approval", "pending_hr", "approved")
                and not (end < date.fromisoformat(r["start_date"]) or start > date.fromisoformat(r["end_date"]))
            ):
                problems.append(f"Пересекается с заявкой {r['id']} ({self._dates(r)}).")
        approver = self.employees.get(c["sub"], {}).get("lead_id") or ""
        out: dict[str, Any] = {"valid": not problems, "problems": problems}
        if approver:
            out |= {"approver_id": approver, "approver_display_name": self.name(approver)}
        return out

    def _type_problems(self, kind: str, start: date, end: date, docs: int) -> list[str]:
        """= B's absence.typeProblems: the rules of each kind of absence."""
        today, problems = clock.today(), []
        days = (end - start).days + 1
        if kind == "day_off":
            return self._day_off_problems(start, end)
        if kind == "vacation" and start < today + timedelta(days=VACATION_NOTICE_DAYS):
            earliest = today + timedelta(days=VACATION_NOTICE_DAYS)
            problems.append(
                f"Заявку на отпуск подают не позже чем за {VACATION_NOTICE_DAYS} дней: начало не раньше "
                f"{earliest:%d.%m.%Y}."
            )
        if kind == "absence" and start <= today:
            problems.append(
                f"Дата начала должна быть не раньше завтрашнего дня ({today + timedelta(days=1):%d.%m.%Y})."
            )
        if kind == "parental_leave" and start < today:
            problems.append("Дата начала уже прошла.")
        if end < start:
            return problems + ["Дата окончания не может быть раньше даты начала."]
        if kind == "vacation" and days > MAX_VACATION_DAYS:
            problems.append(f"Отпуск за один раз — не больше {MAX_VACATION_DAYS} календарных дней, выбрано {days}.")
        if kind == "absence" and days > MAX_ABSENCE_DAYS:
            problems.append(f"Отсутствие оформляется не больше чем на {MAX_ABSENCE_DAYS} дней, выбрано {days}.")
        if kind == "parental_leave" and docs == 0:
            problems.append("Для декрета нужен подтверждающий документ — приложите заявление.")
        return problems

    def _day_off_problems(self, start: date, end: date) -> list[str]:
        pol = self.fixtures["absence_policy"]
        problems: list[str] = []
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
                "оформи отпуск — я подготовлю заявку."
            )
        return problems

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
            "type": a.get("type", "day_off"),
            "start_date": a["start_date"],
            "end_date": a["end_date"],
            "comment": a.get("comment", ""),
            "documents": a.get("documents") or [],
            "approver_id": approver,
            "status": "pending_approval" if approver else "approved",
            "created_at": _now_iso(),
        }
        self.absences.append(rec)
        if approver:
            self._open_approval("absence_request", rec["id"], c["sub"], approver, self._abs_summary(rec))
        result = {**self._absence_view(rec), "replayed": False}
        self.idempotency[key] = result
        return result

    def t_absence__reschedule_request(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        """Move a request until the HR admin approves it; it goes back to the lead (= B's absence.rescheduleRequest)."""
        self._check_confirmation(c, a)
        key = f"reschedule:{a['idempotency_key']}"
        if (replay := self._replay(key)) is not None:
            return replay
        r = next((r for r in self.absences if r["id"] == a.get("id")), None)
        if r is None or r["employee_id"] != c["sub"]:
            raise ToolDenied("Операция недоступна.")
        if r["status"] not in ("pending_approval", "pending_hr"):
            raise ToolValidationError(
                "Перенести можно только заявку, которую HR-администратор ещё не согласовал.", code="conflict"
            )
        v = self._validate(
            c, {**a, "type": r.get("type", "day_off"), "documents": r.get("documents"), "exclude_id": r["id"]}
        )
        if not v["valid"]:
            raise ToolValidationError(" ".join(v["problems"]))
        for p in self.approvals:
            if p["kind"] == "absence_request" and p["ref_id"] == r["id"] and p["status"] == "pending":
                p["status"] = "cancelled"
        r.update(start_date=a["start_date"], end_date=a["end_date"], status="pending_approval")
        r.pop("lead_decided_at", None)
        r.pop("decided_at", None)
        self._open_approval("absence_request", r["id"], c["sub"], r["approver_id"], self._abs_summary(r) + " (перенос)")
        result = {**self._absence_view(r), "replayed": False}
        self.idempotency[key] = result
        return result

    # ------------------------------------------------------------------ approvals
    def _approval_view(self, p: dict[str, Any]) -> dict[str, Any]:
        keys = ("id", "kind", "ref_id", "requester_id", "summary", "status", "comment", "created_at")
        return {
            **{k: p[k] for k in keys},
            "stage": p.get("stage", "lead"),
            "requester_display_name": self.name(p["requester_id"]),
        }

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
            if p.get("stage", "lead") == "lead" and a["decision"] == "approved":
                # the lead approved: the request goes to the HR admin (checkpoint 11.11)
                ref |= {"status": "pending_hr", "lead_decided_at": _now_iso()}
                self._open_approval(
                    "absence_request", ref["id"], ref["employee_id"], self._hr_admin(), p["summary"], stage="hr_admin"
                )
            else:
                ref["status"] = "approved" if a["decision"] == "approved" else "rejected"
                ref["decided_at"] = _now_iso()
            if ref["status"] == "approved":
                ref |= {
                    "hr_admin_id": self._hr_admin(),
                    "registered_at": _now_iso(),
                    "hrbp_id": self.employees.get(ref["employee_id"], {}).get("hrbp_id", ""),
                    "hrbp_notified_at": _now_iso(),
                }
        elif p["kind"] == "trip_request":
            ref = next(r for r in self.trips if r["id"] == p["ref_id"])
            ref["status"] = "approved" if a["decision"] == "approved" else "rejected"
        else:
            ref = next(r for r in self.accesses if r["id"] == p["ref_id"])
            ref["status"] = "granted" if a["decision"] == "approved" else "rejected"
        result = {**self._approval_view(p), "replayed": False}
        self.idempotency[key] = result
        return result

    # ------------------------------------------------------------------ access
    def _required_ids(self, team_id: str) -> list[str]:
        order = ["tool", "project", "repo", "space", "database"]  # = B's list_required order
        rids = [
            rid
            for rid, r in self.fixtures["access_resources"].items()
            if r["team_id"] in (None, team_id) and r["kind"] != "channel"
        ]
        return sorted(rids, key=lambda rid: order.index(self.fixtures["access_resources"][rid]["kind"]))

    def _access_item(self, user_id: str, rid: str) -> dict[str, Any]:
        r = self.fixtures["access_resources"][rid]
        req = next(
            (x for x in reversed(self.accesses) if x["employee_id"] == user_id and x["resource_id"] == rid), None
        )
        out = {
            "resource_id": rid,
            "name": r["name"],
            "kind": r["kind"],
            "elevated": bool(r["elevated"]),
            "status": req["status"] if req else "not_requested",
            "checklist_item_id": r.get("checklist_item_id") or "",
            "environments": list(r.get("environments") or []),
            "levels": list(r.get("levels") or ["read"]),
            "optional": bool(r.get("optional")),
            "approver_label": r.get("approver_label") or "руководитель команды",
        }
        if req:
            out |= {"level": req.get("level", "read")} | (
                {"environment": req["environment"]} if req.get("environment") else {}
            )
        return out

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
        env, level = a.get("environment") or "", a.get("level") or "read"
        envs, levels = list(res.get("environments") or []), list(res.get("levels") or ["read"])
        if envs and env not in envs:
            raise ToolValidationError(f"Для ресурса «{res['name']}» укажите окружение: {', '.join(envs)}.")
        if not envs and env:
            raise ToolValidationError(f"У ресурса «{res['name']}» нет окружений.")
        if level not in levels:
            raise ToolValidationError(
                f"Права «{LEVEL_LABELS.get(level, level)}» недоступны для ресурса «{res['name']}»."
            )
        last = next(
            (
                x
                for x in reversed(self.accesses)
                if x["employee_id"] == c["sub"] and x["resource_id"] == rid and (x.get("environment") or "") == env
            ),
            None,
        )
        if last and (
            last["status"] == "pending_approval" or (last["status"] == "granted" and last.get("level", "read") == level)
        ):
            raise ToolValidationError("Доступ уже выдан или ждёт согласования.", code="conflict")
        elevated = bool(res["elevated"]) or level != "read" or env == "prod"  # checkpoint 11.11
        approver = self.employees.get(c["sub"], {}).get("lead_id") if elevated else None
        rec = {
            "id": self._next_id("ACC"),
            "employee_id": c["sub"],
            "resource_id": rid,
            "reason": a.get("reason", ""),
            "status": "pending_approval" if elevated else "granted",
            "approver_id": approver,
            "environment": env or None,
            "level": level,
            "created_at": _now_iso(),
        }
        self.accesses.append(rec)
        if elevated and approver:
            scope = (
                f" ({env}, {LEVEL_LABELS[level]})" if env else (f" ({LEVEL_LABELS[level]})" if level != "read" else "")
            )
            self._open_approval("access_request", rec["id"], c["sub"], approver, f"Доступ: {res['name']}{scope}")
        result = {
            "id": rec["id"],
            "resource_id": rid,
            "resource_name": res["name"],
            "status": rec["status"],
            "approver_id": approver,
            "checklist_item_id": res.get("checklist_item_id"),
            "environment": env or None,
            "level": level,
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
    # ------------------------------------------------------------------ trips (= B's mocks/trips)
    def t_trips__get_policy(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        return {
            "offices": list(TRIP_OFFICES),
            "min_notice_days": TRIP_MIN_NOTICE_DAYS,
            "max_days": TRIP_MAX_DAYS,
            "text": f"Командировка в другой офис Авито согласуется руководителем. Подать заявку нужно не позже чем за "
            f"{TRIP_MIN_NOTICE_DAYS} дней, длительность — до {TRIP_MAX_DAYS} дней. Билеты и гостиницу после согласования "
            "бронирует служба командировок (тестовые правила прототипа).",
        }

    def _trip_view(self, r: dict[str, Any]) -> dict[str, Any]:
        keys = ("id", "city", "start_date", "end_date", "purpose", "status", "approver_id", "created_at")
        return {**{k: r[k] for k in keys}, "approver_display_name": self.name(r["approver_id"])}

    def t_trips__list_my_requests(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        mine = [self._trip_view(r) for r in self.trips if r["employee_id"] == c["sub"]]
        return {"items": sorted(mine, key=lambda r: (r["start_date"], r["id"]), reverse=True)}

    def home_city(self, employee_id: str) -> str:
        e = self.employees.get(employee_id, {})
        if e.get("location"):
            return e["location"]
        if e.get("role") == "alumni":
            return "Удалённо"
        return "Санкт-Петербург" if e.get("team_id") == "search" else "Москва"

    def _validate_trip(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        try:
            start, end = date.fromisoformat(a.get("start_date", "")), date.fromisoformat(a.get("end_date", ""))
        except ValueError:
            raise ToolValidationError("Даты должны быть в формате ГГГГ-ММ-ДД") from None
        problems: list[str] = []
        city = (a.get("city") or "").strip()
        if city not in TRIP_OFFICES:
            problems.append("Офиса в этом городе нет. Доступно: " + ", ".join(TRIP_OFFICES))
        elif city == self.home_city(c["sub"]):
            problems.append("Это ваш город: командировка оформляется только в другой офис")
        earliest = clock.today() + timedelta(days=TRIP_MIN_NOTICE_DAYS)
        if start < earliest:
            problems.append(
                f"Заявку подают не позже чем за {TRIP_MIN_NOTICE_DAYS} дней: начало не раньше {earliest:%d.%m.%Y}"
            )
        if end < start:
            problems.append("Дата окончания не может быть раньше даты начала")
        else:
            if (days := (end - start).days + 1) > TRIP_MAX_DAYS:
                problems.append(f"Командировка длится не больше {TRIP_MAX_DAYS} дней, у вас {days}")
            other = next(
                (
                    r
                    for r in sorted(self.trips, key=lambda r: r["start_date"])
                    if r["employee_id"] == c["sub"]
                    and r["status"] in ("pending_approval", "approved")
                    and r["start_date"] <= end.isoformat()
                    and r["end_date"] >= start.isoformat()
                ),
                None,
            )
            if other:
                problems.append(f"Даты пересекаются с другой командировкой ({other['id']})")
        if not (a.get("purpose") or "").strip():
            problems.append("Укажите цель командировки")
        lead = self.employees.get(c["sub"], {}).get("lead_id")
        out: dict[str, Any] = {"valid": False, "problems": problems}
        if not lead:
            problems.append("Не удалось определить согласующего: в справочнике не указан руководитель")
        else:
            out |= {"approver_id": lead, "approver_display_name": self.name(lead)}
        out["valid"] = not problems
        return out

    def t_trips__validate_request(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        return self._validate_trip(c, a)

    def t_trips__create_request(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        self._check_confirmation(c, a)
        key = f"trip:{a['idempotency_key']}"
        if (replay := self._replay(key)) is not None:
            return replay
        v = self._validate_trip(c, a)
        if not v["valid"]:
            raise ToolValidationError(". ".join(v["problems"]))
        rec = {
            "id": self._next_id("TRP"),
            "employee_id": c["sub"],
            "city": a["city"].strip(),
            "start_date": a["start_date"],
            "end_date": a["end_date"],
            "purpose": a["purpose"].strip(),
            "status": "pending_approval",
            "approver_id": v["approver_id"],
            "created_at": _now_iso(),
        }
        self.trips.append(rec)
        s, e = date.fromisoformat(rec["start_date"]), date.fromisoformat(rec["end_date"])
        self._open_approval(
            "trip_request",
            rec["id"],
            c["sub"],
            rec["approver_id"],
            f"Командировка: {rec['city']}, {s:%d.%m.%Y}–{e:%d.%m.%Y}",
        )
        result = {**self._trip_view(rec), "replayed": False}
        self.idempotency[key] = result
        return result

    # ------------------------------------------------------------------ consents (= B's mocks/consents)
    def t_consents__list(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        out = []
        for t in self.consent_types:
            g = next(
                (g for g in self.signatures if g["employee_id"] == c["sub"] and g["consent_id"] == t["consent_id"]),
                None,
            )
            item = {**t, "status": "signed" if g else "not_signed"}
            if g:
                item |= {"signed_via": g["via"], "signed_at": g["signed_at"]}
            out.append(item)
        return {"items": out}

    def t_consents__sign(self, c: dict[str, Any], a: dict[str, Any]) -> dict[str, Any]:
        self._check_confirmation(c, a)
        key = f"consent:{a['idempotency_key']}"
        if (stored := self.idempotency.get(key)) is not None:
            if stored["consent_id"] != a.get("consent_id"):
                raise ToolValidationError(
                    "Ключ идемпотентности уже использован, подтвердите действие ещё раз", code="conflict"
                )
            return {**stored, "replayed": True}
        t = next((t for t in self.consent_types if t["consent_id"] == a.get("consent_id")), None)
        if t is None:
            raise ToolError("Такого согласия нет", code="not_found", retryable=False, http_status=404)
        if not t["signable"]:
            raise ToolDenied(f"«{t['title']}» подписывается только в кадровом портале при оформлении")
        if any(g["employee_id"] == c["sub"] and g["consent_id"] == t["consent_id"] for g in self.signatures):
            raise ToolValidationError(f"Согласие «{t['title']}» уже подписано", code="conflict")
        g = {
            "id": self._next_id("CNS"),
            "employee_id": c["sub"],
            "consent_id": t["consent_id"],
            "via": "buddy",
            "signed_at": _now_iso(),
        }
        self.signatures.append(g)
        result = {
            "id": g["id"],
            "consent_id": t["consent_id"],
            "title": t["title"],
            "status": "signed",
            "signed_at": g["signed_at"],
            "replayed": False,
        }
        self.idempotency[key] = result
        return result

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
