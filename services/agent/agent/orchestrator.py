"""Agent orchestrator: one chat turn → SSE events; confirmed proposal → MCP write; reminders, summaries, FAQ drafts.

Spec: openspec/specs/agent-behavior/spec.md (AG-xx). Rights: ADR-003. Hybrid design: ADR-007.
"""

from __future__ import annotations

import hashlib
import logging
import re
import time
from collections.abc import Iterator
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from .auth import User
from .canonical import args_hash, verify_confirmation_token
from .config import AgentConfig, Settings
from .errors import AgentError, BadRequest, Forbidden, LLMError, ToolDenied, ToolError
from .flows import absence, access, coach, consents, plan, qa, team, trips
from .flows import attachments as attachment_flow
from .flows.common import Turn, items, say
from .llm.openai_compat import EmbeddingsClient
from .llm.writer import make_writer
from .metrics import Metrics
from .nlu.classifier import IntentModel
from .nlu.router import IntentRouter, Route
from .retrieval import KnowledgeBase
from .sse import Event, status
from .state import StateStore, make_store
from .text import normalize, same_person
from .tools.base import ToolBackend, ToolGateway
from .tools.inprocess import InProcessServer, MockServices
from .tools.mcp_client import McpHttpServer
from .websearch import WebSearch

log = logging.getLogger("agent")

INTENT_ALIAS = {"absence_status": "absence", "trip_status": "trip"}
ROLE_DENIED = {
    "absence": "Отгулы через меня в прототипе оформляют стажёры, наставники и руководители. Порядок можно посмотреть в "
    "базе знаний: спроси «как оформить отгул?».",
    "access": "Заявки на доступы через меня создают стажёр и наставник. Повышенные доступы стажёров согласует руководитель "
    "— они придут в «Согласования».",
    "channels": "Список каналов команды я показываю стажёру и наставнику.",
    "approvals": "Согласование заявок доступно только руководителю команды.",
    "mentee": "Прогресс стажёров видят их наставник и руководитель.",
    "summary": "Сводку прогресса готовлю для стажёра и наставника.",
    "plan": "У этой роли нет плана адаптации.",
    "trip": "Командировки через меня оформляют стажёры, наставники и руководители.",
}
# A message that clearly starts another scenario ends the quiz instead of being scored as an answer.
QUIZ_BREAK_INTENTS = {
    "absence",
    "absence_status",
    "access",
    "channels",
    "plan",
    "summary",
    "approvals",
    "mentee",
    "escalate",
    "trip",
    "trip_status",
    "consents",
    "help",
    "quiz",
    "code_review",
    "troubleshoot",
    "learning",
    "benefit",
}
SLOT_INTENTS_BLOCKING = {
    "task_message",
    "one_on_one",
    "absence_status",
    "trip_status",
    "consents",
    "access",
    "channels",
    "approvals",
    "whois",
    "plan",
    "summary",
    "help",
    "mentee",
}


class Agent:
    def __init__(
        self,
        config: AgentConfig,
        settings: Settings,
        backends: dict[str, ToolBackend],
        store: StateStore,
        writer: Any,
        router: IntentRouter,
        kb: KnowledgeBase | None = None,
        mocks: MockServices | None = None,
        metrics: Metrics | None = None,
        web: WebSearch | None = None,
    ) -> None:
        self.config = config
        self.settings = settings
        self.backends = backends
        self.store = store
        self.writer = writer
        self.router = router
        self.kb = kb
        self.mocks = mocks
        self.metrics = metrics or Metrics()
        self.web = web

    # ------------------------------------------------------------------ chat
    def gateway(self, user: User) -> ToolGateway:
        return ToolGateway(self.config, self.backends, user)

    def chat(
        self,
        user: User,
        message: str,
        session_id: str,
        message_id: str = "",
        history: list[dict[str, str]] | None = None,
        request_id: str = "",
        attachments: list[dict[str, Any]] | None = None,
    ) -> Iterator[Event]:
        started = time.perf_counter()
        turn = Turn(
            user=user,
            gateway=self.gateway(user),
            config=self.config,
            store=self.store,
            session_id=session_id,
            writer=self.writer,
            history=list(history or [])[-10:],
            kb=self.kb,
            web=self.web,
        )
        yield status("thinking")
        route = Route("unknown", 0.0, "none")
        try:
            if attachments:
                route = self.router.route(message, turn.user.display_name) if message.strip() else route
                if route.source == "guardrail" and route.intent == "refusal":
                    yield from self._dispatch(turn, route, message)  # a refused request is not rescued by a file
                else:
                    route = Route("attachments", 1.0, "attachments")
                    yield from attachment_flow.handle(
                        turn, message, attachments, lambda t: self._route_and_dispatch(turn, t)
                    )
            else:
                route = self._route(turn, message)
                turn.meta["ranking"] = route.detail.get("ranking", [])
                yield from self._dispatch(turn, route, message)
        except ToolDenied as exc:
            self.metrics.inc("agent_refusals_total", reason="no_rights")
            yield Event(
                "refusal", {"reason": "no_rights", "redirect_role": None, "redirect_name": None, "text": exc.message}
            )
            yield Event("token", {"text": exc.message})
        except (ToolError, LLMError) as exc:
            self._finish_metrics(route, started, error=exc.code)
            yield Event("error", exc.to_dict())
            return
        except AgentError as exc:
            self._finish_metrics(route, started, error=exc.code)
            yield Event("error", exc.to_dict())
            return
        except Exception:  # noqa: BLE001 - never leave the stream without a terminal event
            log.exception("chat turn failed request_id=%s", request_id)
            self._finish_metrics(route, started, error="internal")
            yield Event(
                "error", {"code": "internal", "message": "Что-то пошло не так. Попробуй ещё раз.", "retryable": True}
            )
            return
        latency = self._finish_metrics(route, started)
        provider = getattr(self.writer, "name", "stub")
        if turn.meta.get("llm_fallback"):
            provider = "stub_fallback"
        yield Event(
            "done",
            {
                "message_id": message_id,
                "intent": route.intent,
                "route_source": route.source,
                "provider": provider if route.intent == "kb_question" else "deterministic",
                "latency_ms": latency,
                "tool_calls": turn.gateway.calls,
                "confidence": round(route.confidence, 3),
                "escalated": bool(turn.meta.get("escalated")),
                "request_id": request_id,
                "prompt_version": self.config.prompt_version,
            },
        )

    def _route_and_dispatch(self, turn: Turn, message: str) -> Iterator[Event]:
        route = self._route(turn, message)
        turn.meta["ranking"] = route.detail.get("ranking", [])
        yield from self._dispatch(turn, route, message)

    def _finish_metrics(self, route: Route, started: float, error: str | None = None) -> int:
        elapsed = time.perf_counter() - started
        self.metrics.observe("agent_turn_seconds", elapsed)
        self.metrics.inc("agent_turns_total", intent=route.intent, source=route.source)
        if error:
            self.metrics.inc("agent_errors_total", code=error)
        return int(elapsed * 1000)

    def _route(self, turn: Turn, message: str) -> Route:
        route = self.router.route(message, turn.user.display_name)
        state = turn.state().get("absence")
        if (
            state
            and state.get("step") in ("collect", "proposed")
            and route.source != "guardrail"
            and route.intent not in SLOT_INTENTS_BLOCKING
            and absence.looks_like_slot_answer(message)
        ):
            return Route("absence", 0.9, "state")
        trip_state = turn.state().get("trip")
        if (
            trip_state
            and trip_state.get("step") in ("collect", "proposed")
            and route.source != "guardrail"
            and route.intent not in SLOT_INTENTS_BLOCKING
            and not (route.intent == "absence" and route.source == "rule")  # «отгул» said explicitly
            and route.intent not in ("quiz", "code_review", "troubleshoot", "learning", "benefit")
            and trips.looks_like_slot_answer(message, trip_state)
        ):
            return Route("trip", 0.9, "state")
        quiz_state = turn.state()
        if quiz_state.get("quiz"):
            if route.source != "guardrail" and route.intent not in QUIZ_BREAK_INTENTS:
                return Route("quiz_answer", 0.9, "state")
            quiz_state.pop("quiz")
            turn.save_state(quiz_state)
        if route.intent == "refusal" and route.reason == "no_rights":
            person = route.detail.get("person", "colleague")
            if person != "colleague":
                allowed = self._role_exception(turn, person, message)
                if allowed:
                    return allowed
        return route

    def _role_exception(self, turn: Turn, person: str, message: str) -> Route | None:
        """Mentor may see own mentees' progress; lead may decide own team's requests. Everything else is refused."""
        if re.search(r"(оформи|создай|подай|возьми)\w*\s", normalize(message)):
            return None  # nobody acts on behalf of another person
        if turn.user.role == "mentor" and any(
            same_person(person, m.get("display_name", "")) for m in turn.user.mentees
        ):
            return Route("mentee", 0.9, "rule")
        if turn.user.role in ("lead", "hr_admin"):
            try:
                pending = items(turn.gateway.read("approvals.list_pending"))
            except ToolError:
                return None
            if any(same_person(person, p.get("requester_display_name", "")) for p in pending):
                return Route("approvals", 0.9, "rule")
        return None

    def _dispatch(self, turn: Turn, route: Route, message: str) -> Iterator[Event]:
        intent = route.intent
        if intent == "refusal":
            self.metrics.inc("agent_refusals_total", reason=route.reason)
            yield from qa.handle_refusal(turn, route.reason, route.redirect_role, route.detail.get("topic", ""))
            return
        cfg_intent = INTENT_ALIAS.get(intent, intent)
        if not self.config.intent_allowed(cfg_intent, turn.user.role):
            self.metrics.inc("agent_refusals_total", reason="no_rights")
            text = ROLE_DENIED.get(cfg_intent, "Это действие недоступно для твоей роли.")
            yield Event("refusal", {"reason": "no_rights", "redirect_role": None, "redirect_name": None, "text": text})
            yield from say(text)
            return
        flag = self.config.intent_flag(cfg_intent)
        if not turn.user.flag_enabled(flag):
            yield from say(
                "Этот сценарий пока выключен для твоей команды (пилот включается по командам). "
                "Могу помочь с другими вопросами."
            )
            return
        handlers = {
            "absence": absence.handle,
            "absence_status": absence.handle_status,
            "access": access.handle,
            "channels": access.handle_channels,
            "plan": plan.handle,
            "summary": plan.handle_summary,
            "task_message": plan.handle_task_message,
            "one_on_one": team.handle_one_on_one,
            "approvals": team.handle_approvals,
            "mentee": team.handle_mentee,
            "whois": qa.handle_whois,
            "escalate": qa.handle_escalate,
            "human_only": qa.handle_human_only,
            "help": qa.handle_help,
            "smalltalk": qa.handle_smalltalk,
            "offtopic": qa.handle_offtopic,
            "kb_question": qa.handle_question,
            "learning": coach.handle_learning,
            "code_review": coach.handle_code_review,
            "troubleshoot": coach.handle_troubleshoot,
            "quiz": coach.handle_quiz,
            "quiz_answer": coach.handle_quiz_answer,
            "benefit": coach.handle_benefit,
            "trip": trips.handle,
            "trip_status": trips.handle_status,
            "consents": consents.handle,
        }
        yield from handlers.get(intent, qa.handle_question)(turn, message)

    # ------------------------------------------------------------------ confirm turn (write) — ADR-003 layer 4
    def confirm(
        self, user: User, proposal_id: str, draft: dict[str, Any], confirmation_token: str, request_id: str = ""
    ) -> Iterator[Event]:
        """`turn.kind = confirm` of contracts/agent-stream.md: call draft.tool with draft.args unchanged, then emit the
        internal `tool_result` event (core saves the action and relays `action_result` to web)."""
        started = time.perf_counter()
        yield status("calling_service", "Отправляю заявку")
        try:
            result = self.execute(user, proposal_id, draft, confirmation_token)
        except AgentError as exc:
            self._finish_metrics(Route("confirm", 1.0, "confirm"), started, error=exc.code)
            yield Event("error", exc.to_dict())
            return
        yield status("saving")
        yield Event("tool_result", result)
        latency = self._finish_metrics(Route("confirm", 1.0, "confirm"), started)
        yield Event(
            "done",
            {
                "message_id": "",
                "intent": "confirm",
                "route_source": "confirm",
                "provider": "deterministic",
                "latency_ms": latency,
                "tool_calls": [draft.get("tool")],
                "confidence": 1.0,
                "escalated": False,
                "request_id": request_id,
                "prompt_version": self.config.prompt_version,
            },
        )

    def execute(self, user: User, proposal_id: str, draft: dict[str, Any], confirmation_token: str) -> dict[str, Any]:
        started = time.perf_counter()
        tool, args = draft.get("tool"), draft.get("args")
        if not proposal_id or not isinstance(args, dict) or not tool:
            raise BadRequest("Некорректный черновик действия.", code="bad_proposal")
        spec = self.config.tools.get(str(tool))
        if spec is None or not spec.write:
            raise BadRequest("Черновик не соответствует пишущему инструменту.", code="bad_proposal")
        if not self.config.is_allowed(spec.name, user.role):
            raise Forbidden("Это действие недоступно для твоей роли.")
        kind = spec.proposal_kind
        h = args_hash(args)
        reg = self.store.get(f"proposal:{proposal_id}")
        if reg and (reg.get("user_id") != user.user_id or reg.get("args_hash") != h or reg.get("tool") != tool):
            raise Forbidden("Черновик был изменён или принадлежит другому пользователю.", code="confirmation_invalid")
        cached = self.store.get(f"result:{proposal_id}")
        if cached and cached.get("_user") == user.user_id:
            return {k: v for k, v in cached.items() if k != "_user"} | {"replayed": True}
        check = verify_confirmation_token(
            self.settings.confirmation_secret, confirmation_token, user.user_id, proposal_id, h
        )
        if not check.ok:
            raise Forbidden(f"Подтверждение недействительно ({check.reason}).", code="confirmation_invalid")
        gw = self.gateway(user)
        try:
            raw = gw.write(spec.name, args, idempotency_key=proposal_id, confirmation_token=confirmation_token)
        except ToolError as exc:
            self.metrics.inc("agent_actions_total", kind=str(kind), outcome=exc.code)
            raise
        contacts: dict[str, Any] = {}
        try:
            for c in items(gw.read("directory.get_my_contacts")):
                contacts[c["role"]] = {"name": c.get("display_name", ""), "user_id": c.get("employee_id")}
        except ToolError:
            pass
        card = {
            "absence_request": absence.result_card,
            "absence_reschedule": absence.reschedule_card,
            "access_request": access.result_card,
            "approval_decision": team.result_card,
            "trip_request": trips.result_card,
            "consent": consents.result_card,
        }[str(kind)](raw, contacts)
        result = {
            "proposal_id": proposal_id,
            "kind": kind,
            "tool": spec.name,
            **card,
            "task_checklist_item_id": raw.get("checklist_item_id") or (reg or {}).get("checklist_item_id"),
            "replayed": bool(raw.get("replayed")),
            "result": raw,
        }
        self.store.set(f"result:{proposal_id}", {**result, "_user": user.user_id}, ttl_s=86400)
        if kind in ("absence_request", "trip_request") and reg and reg.get("state_key"):
            self.store.delete(reg["state_key"])  # the day-off or trip dialogue is finished
        self.metrics.inc("agent_actions_total", kind=str(kind), outcome="created")
        self.metrics.observe("agent_execute_seconds", time.perf_counter() - started)
        return result

    # ------------------------------------------------------------------ cron, summary, faq
    def reminders(self, user: User, items: list[dict[str, Any]]) -> list[dict[str, Any]]:
        name = (user.display_name or "").split(" ")[0]
        return [plan.reminder_text(i, name) for i in items]

    def summary(self, user: User, actions: list[dict[str, Any]]) -> dict[str, Any]:
        return plan.build_summary(user, [t for t in user.tasks if not t.parent_task_id], actions)

    def faq_draft(self, user: User, question: str, answer: str, team_id: str | None = None) -> dict[str, Any]:
        if user.role not in ("mentor", "lead"):
            raise Forbidden("Пополнять базу знаний могут наставник и руководитель.")
        team_id = team_id or user.team_id
        if team_id != user.team_id:
            raise Forbidden("Можно пополнять базу знаний только своей команды.")
        if not question.strip() or not answer.strip():
            raise BadRequest("Нужны вопрос и ответ.")
        digest = hashlib.sha1(f"{team_id}:{question}".encode()).hexdigest()[:8]
        doc_id = f"faq-{team_id}-{digest}"
        title = question.strip().rstrip("?")[:90]
        md = (
            f'---\ndoc_id: {doc_id}\ntitle: "{title}"\nsource_type: test\n'
            f"acl: {{roles: [intern, mentor, lead, alumni], teams: [{team_id}]}}\nchecklist: []\ntags: [faq]\n"
            f"origin: escalation\nauthor: {user.user_id}\nupdated: {datetime.now(timezone.utc).date().isoformat()}\n---\n"
            f"# {title}?\n\n{answer.strip()}\n"
        )
        # title/body are exactly the arguments of kb.add_faq (contracts/schemas/kb.json) that core calls itself.
        return {"doc_id": doc_id, "title": title, "body": answer.strip(), "markdown": md}


# ---------------------------------------------------------------------- factory
def build_agent(settings: Settings, config: AgentConfig, *, http_client: Any = None, llm_client: Any = None) -> Agent:
    embedder = None
    emb = config.embeddings
    if emb.get("base_url") and emb.get("model"):
        embedder = EmbeddingsClient(str(emb["base_url"]), settings.embeddings_api_key, str(emb["model"]))
    kb = KnowledgeBase.from_dir(settings.kb_dir, embedder=embedder)
    mocks = MockServices(
        settings.fixtures_path,
        settings.checklist_path,
        kb,
        settings.jwt_secret,
        settings.confirmation_secret,
        settings.mock_faults,
    )
    backends: dict[str, ToolBackend] = {}
    base_url = settings.mcp_base_url.rstrip("/")
    for server, spec in config.mcp_servers.items():
        url = str(spec.get("url") or "") or (f"{base_url}/{server}" if base_url else "")
        backends[server] = (
            McpHttpServer(server, url, float(spec.get("timeout_s", 5)), client=http_client)
            if url
            else InProcessServer(mocks, server)
        )
    model_path = Path(config.base_dir) / str(config.nlu.get("model_path", "agent/models/intent_model.json"))
    model = IntentModel.load(model_path) if model_path.exists() else None
    router = IntentRouter(model, float(config.nlu.get("min_confidence", 0.3)))
    writer = make_writer(config, settings.llm_api_key, client=llm_client)
    web = WebSearch(config.raw.get("web_search") or {}, Path(config.base_dir), client=http_client)
    return Agent(
        config, settings, backends, make_store(settings.redis_url), writer, router, kb=kb, mocks=mocks, web=web
    )
