"""Error types mapped to contract error codes (contracts/agent-stream.md, section 7)."""

from __future__ import annotations


class AgentError(Exception):
    code = "internal"
    http_status = 500
    retryable = True

    def __init__(
        self,
        message: str = "",
        *,
        code: str | None = None,
        retryable: bool | None = None,
        http_status: int | None = None,
    ) -> None:
        super().__init__(message or self.__class__.__doc__ or self.code)
        self.message = message or (self.__class__.__doc__ or self.code).strip()
        if code:
            self.code = code
        if retryable is not None:
            self.retryable = retryable
        if http_status is not None:
            self.http_status = http_status

    def to_dict(self) -> dict[str, object]:
        return {"code": self.code, "message": self.message, "retryable": self.retryable}


class Unauthorized(AgentError):
    """Нужна авторизация."""

    code, http_status, retryable = "unauthorized", 401, False


class Forbidden(AgentError):
    """Недостаточно прав для этой операции."""

    code, http_status, retryable = "no_rights", 403, False


class BadRequest(AgentError):
    """Некорректный запрос."""

    code, http_status, retryable = "bad_request", 400, False


class ToolError(AgentError):
    """Сервис временно недоступен. Попробуй ещё раз."""

    code, http_status, retryable = "service_unavailable", 502, True


class ToolValidationError(ToolError):
    """Сервис отклонил запрос."""

    code, http_status, retryable = "validation_failed", 409, False


class ToolDenied(ToolError):
    """Инструмент недоступен для этой роли."""

    code, http_status, retryable = "no_rights", 403, False


class LLMError(AgentError):
    """Модель не ответила. Попробуй ещё раз."""

    code, http_status, retryable = "llm_unavailable", 502, True
