"""Протокол бэкенда инструментов и шлюз с проверкой ролей, через который агент делает каждый вызов (ADR-003, слой 2)."""

from __future__ import annotations

from typing import Any, Protocol

from ..auth import User
from ..config import AgentConfig
from ..errors import ToolDenied


class ToolBackend(Protocol):
    def call(self, tool: str, args: dict[str, Any], user_token: str) -> Any: ...


class ToolGateway:
    """Каждый вызов инструмента агентом проходит здесь: белый список ролей из agent.yaml, защита записи, журнал вызовов."""

    def __init__(self, config: AgentConfig, backends: dict[str, ToolBackend], user: User) -> None:
        self.config = config
        self.backends = backends
        self.user = user
        self.calls: list[str] = []

    def allowed(self, tool: str) -> bool:
        return self.config.is_allowed(tool, self.user.role)

    def read(self, tool: str, **args: Any) -> Any:
        spec = self.config.tools.get(tool)
        if spec is None or not self.allowed(tool):
            raise ToolDenied(f"Инструмент {tool} недоступен для роли {self.user.role}.")
        if spec.write:
            raise ToolDenied(f"{tool} — пишущий инструмент; он вызывается только после подтверждения.")
        return self._call(tool, args)

    def write(self, tool: str, args: dict[str, Any], *, idempotency_key: str, confirmation_token: str) -> Any:
        spec = self.config.tools.get(tool)
        if spec is None or not spec.write or not self.allowed(tool):
            raise ToolDenied(f"Инструмент {tool} недоступен для роли {self.user.role}.")
        if not confirmation_token or not idempotency_key:
            raise ToolDenied("Запись без подтверждения запрещена.", code="confirmation_invalid")
        return self._call(tool, {**args, "idempotency_key": idempotency_key, "confirmation_token": confirmation_token})

    def _call(self, tool: str, args: dict[str, Any]) -> Any:
        server = tool.split(".", 1)[0]
        self.calls.append(tool)
        return self.backends[server].call(tool, args, self.user.token)
