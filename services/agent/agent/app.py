"""Flask-приложение: HTTP API сервиса агента (contracts/agent-stream.md)."""

from __future__ import annotations

import logging
import time
import uuid
from datetime import date
from typing import Any, Literal

from flask import Flask, Response, g, jsonify, request, stream_with_context
from pydantic import BaseModel, Field, ValidationError

from . import __version__, clock
from .auth import bearer, build_user, check_service_token
from .config import AgentConfig, Settings, load_agent_config
from .errors import AgentError, BadRequest
from .orchestrator import Agent, build_agent
from .sse import encode

log = logging.getLogger("agent")


class Attachment(BaseModel):
    """contracts/agent-stream.md turn.attachments: у текстовых файлов передаётся текст, у картинок и документов — только
    метаданные.
    """

    name: str = Field(min_length=1, max_length=200)
    kind: Literal["text", "image", "document"]
    mime: str = Field(default="", max_length=100)
    size: int = Field(default=0, ge=0)
    text: str = Field(default="", max_length=32768)


class Turn(BaseModel):
    kind: Literal["message", "confirm"] = "message"
    text: str | None = Field(default=None, max_length=4000)
    attachments: list[Attachment] = Field(default_factory=list, max_length=3)
    proposal_id: str | None = None
    draft: dict[str, Any] | None = None
    confirmation_token: str | None = None


class TurnRequest(BaseModel):
    """contracts/agent-stream.md, `POST /v1/turn`."""

    session_id: str = Field(min_length=1, max_length=128)
    message_id: str = ""
    turn: Turn
    user_context: dict[str, Any] = Field(default_factory=dict)
    history: list[dict[str, Any]] = Field(default_factory=list)
    flags: dict[str, bool] = Field(default_factory=dict)
    today: date | None = None


class RemindersRequest(BaseModel):
    user_context: dict[str, Any] = Field(default_factory=dict)
    items: list[dict[str, Any]] = Field(default_factory=list)


class SummaryRequest(BaseModel):
    user_context: dict[str, Any] = Field(default_factory=dict)
    actions: list[dict[str, Any]] = Field(default_factory=list)


class FaqRequest(BaseModel):
    question: str
    answer: str
    team_id: str | None = None


def _parse(model: type[BaseModel]) -> Any:
    try:
        return model.model_validate(request.get_json(force=True, silent=False) or {})
    except ValidationError as exc:
        raise BadRequest("Некорректный запрос: " + "; ".join(e["msg"] for e in exc.errors()[:3])) from exc
    except Exception as exc:  # noqa: BLE001 - malformed JSON
        raise BadRequest("Тело запроса должно быть JSON.") from exc


def create_app(
    settings: Settings | None = None, config: AgentConfig | None = None, agent: Agent | None = None
) -> Flask:
    settings = settings or Settings.from_env()
    config = config or load_agent_config()
    agent = agent or build_agent(settings, config)
    logging.basicConfig(level=settings.log_level, format="%(asctime)s %(levelname)s %(name)s %(message)s")

    app = Flask(__name__)
    app.config["AGENT"] = agent
    app.config["SETTINGS"] = settings
    app.json.ensure_ascii = False  # type: ignore[attr-defined]

    @app.before_request
    def _before() -> None:
        g.request_id = request.headers.get("X-Request-Id") or uuid.uuid4().hex[:16]
        g.started = time.perf_counter()

    @app.after_request
    def _after(resp: Response) -> Response:
        resp.headers["X-Request-Id"] = g.get("request_id", "")
        agent.metrics.inc(
            "agent_http_requests_total",
            path=request.url_rule.rule if request.url_rule else "unknown",
            status=str(resp.status_code),
        )
        return resp

    @app.errorhandler(AgentError)
    def _agent_error(exc: AgentError) -> tuple[Response, int]:
        return jsonify({"error": exc.to_dict()}), exc.http_status

    def _user(user_context: dict[str, Any] | None = None, flags: dict[str, bool] | None = None):  # noqa: ANN202
        check_service_token(settings.service_token, request.headers)
        return build_user(settings.jwt_secret, bearer(request.headers), user_context, flags)

    @app.get("/healthz")
    def healthz() -> Response:
        return jsonify({"status": "ok", "version": __version__})

    @app.get("/readyz")
    def readyz() -> tuple[Response, int]:
        checks = {
            "config": True,
            "kb_docs": len(agent.kb.docs) if agent.kb else 0,
            "intent_model": agent.router.model is not None,
            "provider": getattr(agent.writer, "name", "stub"),
            # openai_compat: ready | loading (Ollama ещё скачивает модель) | down | slow; пока что отвечает stub
            "llm_state": getattr(agent.writer, "state", "stub"),
            "llm_model": getattr(getattr(agent.writer, "client", None), "model", None),
        }
        ok = checks["kb_docs"] > 0
        return jsonify({"status": "ready" if ok else "degraded", "checks": checks}), 200 if ok else 503

    @app.get("/metrics")
    def metrics() -> Response:
        return Response(agent.metrics.render(), mimetype="text/plain; version=0.0.4")

    @app.get("/v1/agent/config")
    def agent_config() -> Response:
        user = _user()
        return jsonify(config.sanitized(user.role))

    @app.post("/v1/turn")
    def turn() -> Response:
        req: TurnRequest = _parse(TurnRequest)
        user = _user(req.user_context, req.flags)
        request_id = g.request_id
        override = req.today if settings.allow_clock_override else None
        history = [
            {
                "role": "assistant" if str(h.get("author") or h.get("role")) == "assistant" else "user",
                "text": str(h.get("text", "")),
            }
            for h in req.history
        ]
        t = req.turn
        if t.kind == "message" and not (t.text or "").strip() and not t.attachments:
            raise BadRequest("Пустое сообщение.")
        if t.kind == "confirm" and not (t.proposal_id and t.draft and t.confirmation_token):
            raise BadRequest("Для подтверждения нужны proposal_id, draft и confirmation_token.", code="bad_proposal")

        def generate():  # noqa: ANN202
            token = clock.set_today(override) if override else None
            try:
                events = (
                    agent.chat(
                        user,
                        t.text or "",
                        req.session_id,
                        req.message_id,
                        history,
                        request_id,
                        attachments=[a.model_dump() for a in t.attachments],
                    )
                    if t.kind == "message"
                    else agent.confirm(user, t.proposal_id or "", t.draft or {}, t.confirmation_token or "", request_id)
                )
                for seq, event in enumerate(events, start=1):
                    yield encode(event, seq)
            finally:
                if token is not None:
                    clock.reset(token)

        resp = Response(stream_with_context(generate()), mimetype="text/event-stream")
        resp.headers["Cache-Control"] = "no-cache"
        resp.headers["X-Accel-Buffering"] = "no"
        return resp

    @app.post("/v1/reminders")
    def reminders() -> Response:
        req: RemindersRequest = _parse(RemindersRequest)
        user = _user(req.user_context)
        return jsonify({"reminders": agent.reminders(user, req.items)})

    @app.post("/v1/summary/progress")
    def summary() -> Response:
        req: SummaryRequest = _parse(SummaryRequest)
        user = _user(req.user_context)
        return jsonify(agent.summary(user, req.actions))

    @app.post("/v1/kb/faq-draft")
    def faq_draft() -> Response:
        req: FaqRequest = _parse(FaqRequest)
        user = _user()
        return jsonify(agent.faq_draft(user, req.question, req.answer, req.team_id))

    @app.get("/v1/kb/<doc_id>")
    def kb_doc(doc_id: str) -> Response:
        user = _user()
        doc = agent.kb.get(doc_id, user.role, user.team_id) if agent.kb else None
        if doc is None:
            # Ответ наставника, добавленный в базу знаний, живёт только в mcp-kb: ссылка на источник под ответом тоже
            # должна его открывать.
            try:
                d = agent.gateway(user).read("kb.get", doc_id=doc_id)
            except AgentError:
                d = None
            if isinstance(d, dict) and d.get("body"):
                return jsonify(
                    {
                        "doc_id": d.get("doc_id", doc_id),
                        "title": d.get("title", ""),
                        "body": d["body"],
                        "source_type": "test",
                        "badge": "Тестовая инструкция прототипа",
                    }
                )
            return jsonify(
                {"error": {"code": "not_found", "message": "Документ не найден или недоступен.", "retryable": False}}
            ), 404  # type: ignore[return-value]
        return jsonify(
            {
                "doc_id": doc.doc_id,
                "title": doc.title,
                "body": doc.body,
                "source_type": doc.source_type,
                "badge": "Тестовая инструкция прототипа",
            }
        )

    if settings.dev_mode:
        from .dev import register_dev

        register_dev(app, agent, settings)

    log.info(
        "agent ready: provider=%s prompt=%s kb_docs=%s",
        getattr(agent.writer, "name", "stub"),
        config.prompt_version,
        len(agent.kb.docs) if agent.kb else 0,
    )
    return app


def main() -> None:  # pragma: no cover - entry point for `python -m agent.app`
    import os

    create_app().run(host="0.0.0.0", port=int(os.environ.get("AGENT_PORT", "8090")), threaded=True)


if __name__ == "__main__":  # pragma: no cover
    main()
