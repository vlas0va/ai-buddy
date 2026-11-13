"""Общие фикстуры. Имена тестов содержат ID сценариев OpenSpec (S1-xx, S2-xx, S3-xx, S4-xx, AG-xx, RG-xx, TR-xx)."""

from __future__ import annotations

import os
from datetime import date
from typing import Any

import pytest

os.environ.setdefault("JWT_SECRET", "test-jwt-secret-long-enough-for-hs256-0123456789")
os.environ.setdefault("CONFIRMATION_HMAC_SECRET", "test-hmac-secret-long-enough-0123456789")

from agent import clock  # noqa: E402
from agent.app import create_app  # noqa: E402
from agent.config import Settings, load_agent_config  # noqa: E402
from agent.orchestrator import build_agent  # noqa: E402
from agent.sse import Event, parse_stream  # noqa: E402

TODAY = date(2026, 11, 9)  # понедельник


@pytest.fixture(autouse=True)
def fixed_clock():
    clock.set_default(TODAY)
    yield
    clock.set_default(None)


def make_settings(**kw: Any) -> Settings:
    s = Settings.from_env(
        {
            "JWT_SECRET": os.environ["JWT_SECRET"],
            "CONFIRMATION_HMAC_SECRET": os.environ["CONFIRMATION_HMAC_SECRET"],
            "AGENT_DEV_MODE": "true",
            "AGENT_ALLOW_CLOCK_OVERRIDE": "true",
        }
    )
    for k, v in kw.items():
        setattr(s, k, v)
    return s


class Client:
    """Тонкий помощник поверх тестового клиента Flask, говорящий по contracts/agent-stream.md."""

    def __init__(self, app):  # noqa: ANN001
        self.app = app
        self.http = app.test_client()
        self.agent = app.config["AGENT"]

    def login(self, user: str) -> Session:
        d = self.http.post("/dev/login", json={"login": user}).get_json()
        return Session(self, d["token"], d["user_context"])


class Session:
    def __init__(self, client: Client, token: str, ctx: dict[str, Any]) -> None:
        self.c = client
        self.token = token
        self.ctx = ctx
        self.session_id = "s-test"

    @property
    def headers(self) -> dict[str, str]:
        return {"Authorization": f"Bearer {self.token}"}

    def refresh(self) -> None:
        self.ctx = self.c.http.get("/dev/context", headers=self.headers).get_json()

    def say(self, text: str, **extra: Any) -> list[Event]:
        body = {
            "session_id": self.session_id,
            "turn": {"kind": "message", "text": text},
            "user_context": self.ctx,
            **extra,
        }
        r = self.c.http.post("/v1/turn", json=body, headers=self.headers)
        assert r.status_code == 200, r.get_data(as_text=True)
        assert r.mimetype == "text/event-stream"
        return parse_stream(r.get_data(as_text=True))

    def token_for(self, proposal: dict[str, Any]) -> str:
        return self.c.http.post("/dev/confirm", json={"proposal": proposal}, headers=self.headers).get_json()[
            "confirmation_token"
        ]

    def confirm(
        self, proposal: dict[str, Any], token: str | None = None, draft: dict[str, Any] | None = None
    ) -> list[Event]:
        body = {
            "session_id": self.session_id,
            "turn": {
                "kind": "confirm",
                "proposal_id": proposal["proposal_id"],
                "draft": draft or proposal["draft"],
                "confirmation_token": token or self.token_for(proposal),
            },
            "user_context": self.ctx,
        }
        r = self.c.http.post("/v1/turn", json=body, headers=self.headers)
        assert r.status_code == 200, r.get_data(as_text=True)
        return parse_stream(r.get_data(as_text=True))


def text_of(events: list[Event]) -> str:
    return "".join(e.data["text"] for e in events if e.name == "token")


def first(events: list[Event], name: str) -> Any:
    return next((e.data for e in events if e.name == name), None)


def all_of(events: list[Event], name: str) -> list[Any]:
    return [e.data for e in events if e.name == name]


@pytest.fixture
def app_factory():
    def factory(**settings_kw: Any):  # noqa: ANN202
        settings = make_settings(**settings_kw)
        config = load_agent_config()
        return create_app(settings, config, build_agent(settings, config))

    return factory


@pytest.fixture
def client(app_factory) -> Client:  # noqa: ANN001
    return Client(app_factory())


@pytest.fixture
def anna(client: Client) -> Session:
    return client.login("intern.anna")


@pytest.fixture
def ivan(client: Client) -> Session:
    return client.login("intern.ivan")
