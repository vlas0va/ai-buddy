"""Авторы ответов для S2. StubWriter извлекает текст и детерминирован; LLMWriter потоком выдаёт обоснованный ответ от любой
OpenAI-совместимой модели и может вызывать инструменты ЧТЕНИЯ роли пользователя. Оба получают одни и те же документы,
отфильтрованные по ACL.
"""

from __future__ import annotations

import json
import logging
import re
import time
from collections.abc import Iterator
from typing import Any

from ..auth import User
from ..config import AgentConfig
from ..errors import LLMError, ToolError
from ..tools.base import ToolGateway
from .openai_compat import OpenAICompatClient

log = logging.getLogger("agent.llm")

# «В базе знаний я этого не нашёл», хотя документ отвечает на вопрос: маленькая локальная модель иногда сдаётся
NOT_FOUND = re.compile(r"не\s+нашёл|не\s+нашел|нет\s+(информации|конкретн|данных)|не\s+содерж|не\s+указан", re.I)
STRONG_COVERAGE = 0.75

ROLE_RU = {
    "intern": "стажёр",
    "mentor": "наставник",
    "lead": "руководитель",
    "hrbp": "HR BP",
    "alumni": "выпускник стажировки",
}
TEAM_RU = {"delivery": "Доставка", "search": "Поиск"}


def user_block(user: User, open_tasks: list[str]) -> str:
    tasks = "; ".join(open_tasks[:5]) or "нет данных"
    return (
        f"Имя: {user.display_name or 'не указано'}. Роль: {ROLE_RU.get(user.role, user.role)}. "
        f"Команда: {TEAM_RU.get(user.team_id, user.team_id)}. Офис: {user.location or 'не указан'}. "
        f"Этап: {user.stage or 'не указан'}. "
        f"Открытые задачи плана: {tasks}."
    )


class StubWriter:
    name = "stub"

    def answer(
        self,
        question: str,
        hits: list[dict[str, Any]],
        user: User,
        task_hint: str = "",
        gateway: ToolGateway | None = None,
        history: list[dict[str, str]] | None = None,
        meta: dict[str, Any] | None = None,
    ) -> Iterator[str]:
        top = hits[0]
        if str(top.get("doc_id", "")).startswith("faq-"):  # ответ наставника, добавленный в базу знаний (отзыв 12.13)
            yield f"Наставник уже отвечал на такой вопрос:\n\n{top['snippet'].strip()}"
            if task_hint:
                yield f"\n\n{task_hint}"
            return
        yield f"По тестовой инструкции «{top['title']}»:\n\n{top['snippet'].strip()}"
        if len(hits) > 1 and hits[1].get("score", 0) >= 0.6 * top.get("score", 0):
            yield f"\n\nЕщё может пригодиться: «{hits[1]['title']}»."
        if task_hint:
            yield f"\n\n{task_hint}"


class LLMWriter:
    """Ответ пишет модель; stub подхватывает, когда модель не готова или сбоит (для эксперта по умолчанию A-Vibe: Ollama
    скачивает её в фоне при первом запуске, поэтому первые ответы приходят от stub).
    """

    PROBE_EVERY_S = 15.0  # как часто спрашивать сервер, появилась ли модель, пока её нет
    COOLDOWN_S = 30.0  # после сбоя сервера: какое-то время сразу отвечать stub
    SLOW_COOLDOWN_S = (
        300.0  # после таймаута (например, нет GPU): модель слишком медленная для чата, повтор через 5 минут
    )

    def __init__(
        self,
        config: AgentConfig,
        client: OpenAICompatClient,
        fallback: StubWriter | None = None,
        clock: Any = time.monotonic,
    ) -> None:
        self.config = config
        self.client = client
        self.fallback = fallback
        self.name = "openai_compat"
        self.clock = clock
        self._ready = False
        self._checked_at = -1e9
        self._down_until = -1e9
        self.state = "unknown"  # unknown | ready | loading | down | slow — показывается в /readyz
        llm = config.llm
        self.tool_calling = str(llm.get("tool_calling", "false")).lower() in ("1", "true", "yes", "on")
        self.max_iter = int(llm.get("max_tool_iterations", 3))

    def _messages(
        self,
        question: str,
        hits: list[dict[str, Any]],
        user: User,
        task_hint: str,
        history: list[dict[str, str]] | None,
    ) -> list[dict[str, Any]]:
        open_tasks = [t.title for t in user.tasks if t.status != "done"]
        system = self.config.prompt_text("system").replace("{user_block}", user_block(user, open_tasks))
        # сначала абзац, который отвечает (выбран по вопросу и офису пользователя), затем весь документ; без id
        # документов: чат показывает источники чипами, модель не должна печатать id
        docs = "\n\n".join(
            f'<doc title="{h["title"]}">\nГлавное по вопросу:\n{h["snippet"]}\n\nВесь документ:\n{h.get("body") or h["snippet"]}\n</doc>'
            for h in hits
        )
        prompt = (
            self.config.prompt_text("answer")
            .replace("{question}", question)
            .replace("{documents}", docs)
            .replace("{task_hint}", task_hint)
        )
        msgs: list[dict[str, Any]] = [{"role": "system", "content": system}]
        for h in (history or [])[-6:]:
            role = "assistant" if h.get("role") == "assistant" else "user"
            msgs.append({"role": role, "content": str(h.get("text", ""))[:800]})
        msgs.append({"role": "user", "content": prompt})
        return msgs

    def _tool_specs(self, user: User) -> list[dict[str, Any]]:
        return [
            {
                "type": "function",
                "function": {
                    "name": t.name.replace(".", "__"),
                    "description": t.description,
                    "parameters": t.input_schema or {"type": "object", "properties": {}},
                },
            }
            for t in self.config.tools_for_role(user.role)
        ]  # только инструменты ЧТЕНИЯ роли, никогда инструменты записи

    def answer(
        self,
        question: str,
        hits: list[dict[str, Any]],
        user: User,
        task_hint: str = "",
        gateway: ToolGateway | None = None,
        history: list[dict[str, str]] | None = None,
        meta: dict[str, Any] | None = None,
    ) -> Iterator[str]:
        if self.fallback and not self._available():
            if meta is not None:
                meta["llm_fallback"] = True
            yield (
                "(Модель A-Vibe ещё загружается, ответ сформирован в упрощённом режиме.)\n\n"
                if self.state == "loading"
                else "(Модель сейчас недоступна, ответ сформирован в упрощённом режиме.)\n\n"
            )
            yield from self.fallback.answer(question, hits, user, task_hint)
            return
        msgs = self._messages(question, hits, user, task_hint, history)
        try:
            if self.tool_calling and gateway is not None:
                msgs = self._run_tools(msgs, user, gateway)
            strong = bool(hits) and float(hits[0].get("coverage", 0) or 0) >= STRONG_COVERAGE
            stream = self.client.stream(msgs)
            head = ""
            for chunk in stream:  # придержать первое предложение: «не нашёл» при сильном документе заменяется
                head += chunk
                if len(head) >= 120 or any(p in head for p in ".!?\n"):
                    break
            if strong and self.fallback and NOT_FOUND.search(head):
                log.info("LLM said «not found» over a covering document; answering from the document")
                if meta is not None:
                    meta["llm_not_found_override"] = True
                yield from self.fallback.answer(question, hits, user, task_hint)
                return
            yield head
            yield from stream
        except LLMError as exc:
            # причина уходит в лог, чтобы «упрощённый режим» можно было разобрать (имя модели, адрес, таймаут)
            log.warning(
                "LLM failed, answering with the stub: %s (url=%s model=%s)", exc, self.client.url, self.client.model
            )
            if not self.fallback:
                raise
            self._failed(exc)
            if meta is not None:
                meta["llm_fallback"] = True
            yield "(Модель сейчас недоступна, ответ сформирован в упрощённом режиме.)\n\n"
            yield from self.fallback.answer(question, hits, user, task_hint)

    def _available(self) -> bool:
        now = self.clock()
        if now < self._down_until:
            return False
        if self._ready:
            return True
        if now - self._checked_at < self.PROBE_EVERY_S:
            return False
        self._checked_at = now
        listed = self.client.model_listed()
        if listed is False:
            self.state = "loading"
            return False
        self._ready, self.state = True, "ready"
        return True

    def _failed(self, exc: LLMError) -> None:
        slow = getattr(exc, "code", "") == "llm_timeout"
        self._ready = False
        self._checked_at = self.clock()
        self._down_until = self.clock() + (self.SLOW_COOLDOWN_S if slow else self.COOLDOWN_S)
        self.state = "slow" if slow else "down"

    def _run_tools(self, msgs: list[dict[str, Any]], user: User, gateway: ToolGateway) -> list[dict[str, Any]]:
        specs = self._tool_specs(user)
        for _ in range(self.max_iter):
            completion = self.client.complete(msgs, tools=specs)
            if not completion.tool_calls:
                return msgs
            msgs.append({"role": "assistant", "content": completion.content or "", "tool_calls": completion.tool_calls})
            for call in completion.tool_calls:
                fn = call.get("function", {})
                name = str(fn.get("name", "")).replace("__", ".")
                try:
                    args = json.loads(fn.get("arguments") or "{}")
                    result: Any = gateway.read(
                        name, **args
                    )  # gateway проверяет белый список ролей и режим только чтения
                except (ToolError, json.JSONDecodeError, TypeError) as exc:
                    result = {"error": str(exc)}
                msgs.append(
                    {
                        "role": "tool",
                        "tool_call_id": call.get("id", name),
                        "content": json.dumps(result, ensure_ascii=False)[:4000],
                    }
                )
        return msgs


def make_writer(config: AgentConfig, api_key: str, client: Any = None) -> StubWriter | LLMWriter:
    llm = config.llm
    if str(llm.get("provider", "stub")) != "openai_compat":
        return StubWriter()
    oc = OpenAICompatClient(
        base_url=str(llm.get("base_url", "")),
        api_key=api_key,
        model=str(llm.get("model", "")),
        timeout_s=float(llm.get("timeout_s", 30)),
        temperature=float(llm.get("temperature", 0.2)),
        max_tokens=int(llm.get("max_tokens", 700)),
        client=client,
        no_think=str(llm.get("no_think", "true")).lower() in ("1", "true", "yes", "on"),
    )
    fallback = StubWriter() if str(llm.get("fallback_to_stub", "true")).lower() in ("1", "true", "yes", "on") else None
    return LLMWriter(config, oc, fallback)
