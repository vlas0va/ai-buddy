"""End-to-end smoke of the declared scenarios through core, exactly as the web client calls it (R80).

Usage: python scripts/smoke.py [CORE_URL]   (default http://localhost:8080; stack started with `make up`, stub LLM)

S3: intern.anna asks for a day off → draft → confirm → ABS-… with status pending_approval, saved in /api/actions and
linked to a plan task → lead.maria approves in /api/approvals → the intern's action turns approved.
S4: intern.anna requests Jira → confirm → access action saved with a status.
Standard library only, so it runs on a bare runner and on Windows. Exit code 0 = all checks passed.
"""

from __future__ import annotations

import datetime as dt
import json
import sys
import urllib.error
import urllib.request
import uuid

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080").rstrip("/")
TIMEOUT_S = 60


def fail(msg: str) -> None:
    print(f"FAIL: {msg}", file=sys.stderr)
    sys.exit(1)


def call(method: str, path: str, token: str | None = None, body: dict | None = None, headers: dict | None = None):
    req = urllib.request.Request(BASE + path, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    data = json.dumps(body).encode() if body is not None else None
    try:
        with urllib.request.urlopen(req, data, timeout=TIMEOUT_S) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        fail(f"{method} {path} → {e.code}: {raw[:300]}")


def stream(path: str, token: str, body: dict | None = None) -> list[tuple[str, dict]]:
    """POST an SSE endpoint and return [(event, data)] until done/error."""
    req = urllib.request.Request(BASE + path, method="POST", data=json.dumps(body or {}).encode())
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "text/event-stream")
    req.add_header("Authorization", f"Bearer {token}")
    events: list[tuple[str, dict]] = []
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT_S) as resp:
            name, data = "message", []
            for raw in resp:
                line = raw.decode().rstrip("\r\n")
                if line == "":
                    if data:
                        events.append((name, json.loads("\n".join(data))))
                        if name in ("done", "error"):
                            break
                    name, data = "message", []
                elif line.startswith(":"):
                    continue
                elif line.startswith("event:"):
                    name = line[6:].strip()
                elif line.startswith("data:"):
                    data.append(line[5:].strip())
    except urllib.error.HTTPError as e:
        fail(f"POST {path} → {e.code}: {e.read().decode()[:300]}")
    if not events or events[-1][0] != "done":
        fail(f"{path}: stream did not end with done: {events[-3:]}")
    return events


def first(events: list[tuple[str, dict]], name: str) -> dict:
    for ev, data in events:
        if ev == name:
            return data
    fail(f"no {name} event in {[e for e, _ in events]}")
    return {}


def login(user: str) -> str:
    _, body = call("POST", "/api/auth/login", body={"login": user, "password": "demo"})
    return body["token"]


def day_off_date(today: dt.date) -> dt.date:
    """A Wednesday 8-14 days ahead: a working day, far from today (no short-notice question), never a weekend."""
    d = today + dt.timedelta(days=8)
    while d.weekday() != 2:
        d += dt.timedelta(days=1)
    return d


def ok(msg: str) -> None:
    print(f"ok  {msg}")


def main() -> None:
    status, health = call("GET", "/healthz")
    if status != 200:
        fail(f"core /healthz: {status} {health}")
    ok("core healthy")

    anna = login("intern.anna")
    _, me = call("GET", "/api/me", anna)
    if me.get("user", me).get("role", "intern") != "intern":
        fail(f"/api/me: {me}")
    _, sess = call("POST", "/api/chat/sessions", anna)
    sid = sess["session_id"]

    # --- S3: day off ---------------------------------------------------------------------------------------------
    day = day_off_date(dt.date.today())
    ev = stream(f"/api/chat/sessions/{sid}/messages", anna, {"text": f"хочу отгул {day:%d.%m.%Y}"})
    proposal = first(ev, "action_proposal")
    if proposal.get("kind") != "absence_request" or proposal["draft"]["args"].get("start_date") != day.isoformat():
        fail(f"S3 draft: {proposal}")
    ok(f"S3 draft for {day.isoformat()} (proposal {proposal['proposal_id']})")

    ev = stream(f"/api/chat/sessions/{sid}/proposals/{proposal['proposal_id']}/confirm", anna)
    if any(e == "tool_result" for e, _ in ev):
        fail("tool_result must never reach the web client")
    result = first(ev, "action_result")
    if not str(result.get("external_id", "")).startswith("ABS-") or result.get("status") != "pending_approval":
        fail(f"S3 result: {result}")
    if not result.get("task_id"):
        fail(f"S3 action not linked to a plan task: {result}")
    ok(f"S3 request {result['external_id']} {result['status']}, task {result['task_id']}")

    again = stream(f"/api/chat/sessions/{sid}/proposals/{proposal['proposal_id']}/confirm", anna)
    if first(again, "action_result").get("action_id") != result["action_id"]:
        fail("repeated confirm must replay the same action (R79)")
    ok("S3 repeated confirm replays the stored result")

    _, actions = call("GET", "/api/actions", anna)
    saved = next((a for a in actions if a.get("external_id") == result["external_id"]), None)
    if not saved or saved.get("task_id") != result["task_id"]:
        fail(f"S3 action not saved: {actions}")
    _, plan = call("GET", "/api/plan", anna)
    tasks = [t for p in plan.get("periods", []) for t in p.get("tasks", [])]
    if not any(t.get("id") == result["task_id"] for t in tasks):
        fail("S3 linked task is not in the plan")
    ok("S3 action saved and visible after reload, linked task in the plan")

    maria = login("lead.maria")
    _, pending = call("GET", "/api/approvals", maria)
    item = next((a for a in pending if a.get("kind") == "absence_request" and a.get("status") == "pending"), None)
    if not item:
        fail(f"lead sees no pending absence approval: {pending}")
    call("POST", f"/api/approvals/{item['id']}/decision", maria, {"decision": "approved", "comment": ""},
         {"Idempotency-Key": str(uuid.uuid4())})
    _, actions = call("GET", "/api/actions", anna)
    saved = next((a for a in actions if a.get("external_id") == result["external_id"]), None)
    if not saved or saved.get("status") != "pending_hr":
        fail(f"S3 status after the lead: {saved}")
    ok("S3 lead approved, the request waits for the HR admin")
    olga = login("hradmin.olga")
    _, pending = call("GET", "/api/approvals", olga)
    item = next((a for a in pending if a.get("stage") == "hr_admin" and a.get("status") == "pending"), None)
    if not item:
        fail(f"HR admin sees no pending absence approval: {pending}")
    call("POST", f"/api/approvals/{item['id']}/decision", olga, {"decision": "approved", "comment": ""},
         {"Idempotency-Key": str(uuid.uuid4())})
    _, actions = call("GET", "/api/actions", anna)
    saved = next((a for a in actions if a.get("external_id") == result["external_id"]), None)
    if not saved or saved.get("status") != "approved":
        fail(f"S3 status after approval: {saved}")
    ok("S3 HR admin approved, intern's action is approved")

    # --- S4: access ----------------------------------------------------------------------------------------------
    ev = stream(f"/api/chat/sessions/{sid}/messages", anna, {"text": "запроси доступ в jira"})
    proposal = first(ev, "action_proposal")
    if proposal.get("kind") != "access_request":
        fail(f"S4 draft: {proposal}")
    ev = stream(f"/api/chat/sessions/{sid}/proposals/{proposal['proposal_id']}/confirm", anna)
    result = first(ev, "action_result")
    if result.get("status") not in ("granted", "pending_approval") or not result.get("task_id"):
        fail(f"S4 result: {result}")
    ok(f"S4 access request {result.get('external_id')} {result['status']}, task {result['task_id']}")

    # --- HR steps of the approved day off: the HR BP got a notice (Q&A 10.11) -------------------------------------
    elena = login("hrbp.elena")
    _, notes = call("GET", "/api/notifications", elena)
    items = notes.get("items", notes) if isinstance(notes, dict) else notes
    if not any(str(n.get("title", "")).startswith("Отгул оформлен") for n in items):
        fail(f"HR BP notice after approval: {items}")
    ok("S3 approved day off registered by the HR admin, HR BP notified")

    # --- trip: draft → confirm → TRP-… → lead approves ------------------------------------------------------------
    start = dt.date.today() + dt.timedelta(days=12)
    end = start + dt.timedelta(days=2)
    text = f"оформи командировку в Казань с {start:%d.%m.%Y} по {end:%d.%m.%Y}, цель — встреча с командой"
    proposal = first(stream(f"/api/chat/sessions/{sid}/messages", anna, {"text": text}), "action_proposal")
    if proposal.get("kind") != "trip_request" or proposal["draft"]["args"].get("city") != "Казань":
        fail(f"trip draft: {proposal}")
    result = first(stream(f"/api/chat/sessions/{sid}/proposals/{proposal['proposal_id']}/confirm", anna), "action_result")
    if not str(result.get("external_id", "")).startswith("TRP-") or result.get("status") != "pending_approval":
        fail(f"trip result: {result}")
    _, pending = call("GET", "/api/approvals", maria)
    item = next((p for p in pending if p.get("kind") == "trip_request"), None)
    if not item:
        fail(f"trip approval missing: {pending}")
    call("POST", f"/api/approvals/{item['id']}/decision", maria, {"decision": "approved", "comment": ""},
         headers={"Idempotency-Key": str(uuid.uuid4())})
    _, actions = call("GET", "/api/actions", anna)
    saved = next((a for a in actions if a.get("external_id") == result["external_id"]), None)
    if not saved or saved.get("status") != "approved":
        fail(f"trip status after approval: {saved}")
    ok(f"trip {result['external_id']} approved by the lead")

    # --- consent: voluntary consent signed after confirmation -----------------------------------------------------
    proposal = first(stream(f"/api/chat/sessions/{sid}/messages", anna, {"text": "подпиши согласие на фото"}), "action_proposal")
    result = first(stream(f"/api/chat/sessions/{sid}/proposals/{proposal['proposal_id']}/confirm", anna), "action_result")
    if result.get("kind") != "consent" or result.get("status") != "signed":
        fail(f"consent result: {result}")
    ok(f"consent {result.get('external_id')} signed")

    # --- attachments: a log with a secret is masked ---------------------------------------------------------------
    log = "ERROR connect postgres://buddy:hunter2@db:5432/app\nconnection refused\n"
    ev = stream(f"/api/chat/sessions/{sid}/messages", anna,
                {"text": "", "attachments": [{"name": "app.log", "kind": "text", "size": len(log), "text": log}]})
    if "hunter2" in json.dumps(ev, ensure_ascii=False) or not any(name == "done" for name, _ in ev):
        fail(f"attachment turn: {ev[-3:]}")
    ok("attachment: log analysed, secret masked")

    print("SMOKE OK")


if __name__ == "__main__":
    main()
