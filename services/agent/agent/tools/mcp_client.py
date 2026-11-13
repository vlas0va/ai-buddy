"""Минимальный MCP-клиент поверх streamable HTTP (JSON-RPC 2.0), через который агент ходит к Go MCP-серверам B.

На каждый вызов: передаёт bearer-токен пользователя (серверы перепроверяют права, ADR-003, слой 3), выполняет
рукопожатие initialize и `Mcp-Session-Id`, принимает ответы и в JSON, и в SSE, превращает сбои в ToolError.
"""

from __future__ import annotations

import itertools
import json
import threading
from typing import Any

import httpx

from ..errors import ToolDenied, ToolError, ToolValidationError

PROTOCOL_VERSION = "2025-06-18"


class McpHttpServer:
    def __init__(self, server: str, url: str, timeout_s: float = 5.0, client: httpx.Client | None = None) -> None:
        self.server = server
        self.url = url
        self.client = client or httpx.Client(timeout=timeout_s)
        self._session: str | None = None
        self._ids = itertools.count(1)
        self._lock = threading.Lock()

    def _headers(self, token: str) -> dict[str, str]:
        h = {
            "Accept": "application/json, text/event-stream",
            "Content-Type": "application/json",
            "Authorization": f"Bearer {token}",
            "MCP-Protocol-Version": PROTOCOL_VERSION,
        }
        if self._session:
            h["Mcp-Session-Id"] = self._session
        return h

    @staticmethod
    def _parse(resp: httpx.Response) -> dict[str, Any]:
        ctype = resp.headers.get("content-type", "")
        if "text/event-stream" in ctype:
            for line in resp.text.splitlines():
                if line.startswith("data:"):
                    msg = json.loads(line[5:].strip())
                    if "result" in msg or "error" in msg:
                        return msg
            raise ToolError("Пустой ответ сервиса.")
        return resp.json() if resp.content else {}

    def _post(self, payload: dict[str, Any], token: str) -> httpx.Response:
        try:
            return self.client.post(self.url, json=payload, headers=self._headers(token))
        except httpx.TimeoutException as exc:
            raise ToolError(
                "Сервис не ответил вовремя. Попробуй ещё раз.", code="service_timeout", http_status=504
            ) from exc
        except httpx.HTTPError as exc:
            raise ToolError("Сервис недоступен. Попробуй ещё раз.") from exc

    def _initialize(self, token: str) -> None:
        self._session = None
        resp = self._post(
            {
                "jsonrpc": "2.0",
                "id": next(self._ids),
                "method": "initialize",
                "params": {
                    "protocolVersion": PROTOCOL_VERSION,
                    "capabilities": {},
                    "clientInfo": {"name": "ai-buddy-agent", "version": "1.0.0"},
                },
            },
            token,
        )
        if resp.status_code >= 400:
            raise ToolError(f"MCP {self.server}: initialize failed ({resp.status_code})")
        self._session = resp.headers.get("Mcp-Session-Id")
        self._post({"jsonrpc": "2.0", "method": "notifications/initialized"}, token)

    def call(self, tool: str, args: dict[str, Any], user_token: str) -> Any:
        name = tool.split(".", 1)[1]
        with self._lock:
            if self._session is None:
                self._initialize(user_token)
            payload = {
                "jsonrpc": "2.0",
                "id": next(self._ids),
                "method": "tools/call",
                "params": {"name": name, "arguments": args},
            }
            resp = self._post(payload, user_token)
            if resp.status_code in (400, 404) and self._session:
                self._initialize(user_token)  # сессия истекла на сервере → повторное рукопожатие один раз
                resp = self._post(payload, user_token)
        if resp.status_code in (401, 403):
            raise ToolDenied("Сервис отказал в доступе.")
        if resp.status_code >= 500:
            raise ToolError(f"Сервис {self.server} вернул ошибку {resp.status_code}.")
        msg = self._parse(resp)
        if "error" in msg:
            raise ToolError(str(msg["error"].get("message", "MCP error")), retryable=False)
        result = msg.get("result", {})
        if result.get("isError"):
            raise map_tool_error(result)
        if "structuredContent" in result:
            return result["structuredContent"]
        text = _first_text(result)
        try:
            return json.loads(text)
        except (TypeError, json.JSONDecodeError):
            return text


def map_tool_error(result: dict[str, Any]) -> ToolError:
    """contracts/mcp-tools.md: {"code": forbidden|not_found|validation_error|conflict|invalid_confirmation|unavailable}."""
    sc = result.get("structuredContent") or {}
    if not sc:
        try:
            sc = json.loads(_first_text(result))
        except (TypeError, json.JSONDecodeError):
            sc = {"code": "validation_error", "message": _first_text(result)}
    code = str(sc.get("code", "validation_error"))
    message = str(sc.get("message") or "Сервис отклонил запрос.")
    if code == "forbidden":
        return ToolDenied(message)
    if code == "invalid_confirmation":
        return ToolDenied(message, code="confirmation_invalid")
    if code == "unavailable":
        return ToolError(message, retryable=bool(sc.get("retryable", True)))
    return ToolValidationError(message, code="conflict" if code == "conflict" else "validation_failed")


def _first_text(result: dict[str, Any]) -> str:
    for item in result.get("content", []):
        if item.get("type") == "text":
            return str(item.get("text", ""))
    return ""
