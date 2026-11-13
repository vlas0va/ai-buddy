"""Часы в рамках запроса: по умолчанию реальное время, можно переопределить для демо/e2e (AGENT_ALLOW_CLOCK_OVERRIDE) и
тестов.
"""

from __future__ import annotations

import contextvars
from datetime import date, datetime, timezone

_override: contextvars.ContextVar[date | None] = contextvars.ContextVar("today_override", default=None)
_default: date | None = None


def today() -> date:
    return _override.get() or _default or datetime.now(timezone.utc).astimezone().date()


def set_today(value: date | None) -> contextvars.Token:
    return _override.set(value)


def reset(token: contextvars.Token) -> None:
    _override.reset(token)


def set_default(value: date | None) -> None:
    """Значение по умолчанию для всего процесса (тесты, оценка)."""
    global _default
    _default = value
