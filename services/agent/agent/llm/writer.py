"""Answer writers for S2. StubWriter is extractive and deterministic; LLMWriter streams a grounded answer from any
OpenAI-compatible model and may call READ tools of the user's role. Both receive the same ACL-filtered documents."""

from __future__ import annotations

import json
from collections.abc import Iterator
from typing import Any

from ..auth import User
from ..config import AgentConfig
from ..errors import LLMError, ToolError
from ..tools.base import ToolGateway
from .openai_compat import OpenAICompatClient

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
        f"Команда: {TEAM_RU.get(user.team_id, user.team_id)}. Этап: {user.stage or 'не указан'}. "
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
        yield f"По тестовой инструкции «{top['title']}»:\n\n{top['snippet'].strip()}"
        if len(hits) > 1 and hits[1].get("score", 0) >= 0.6 * top.get("score", 0):
            yield f"\n\nЕщё может пригодиться: «{hits[1]['title']}»."
        if task_hint:
            yield f"\n\n{task_hint}"


class LLMWriter:
    def __init__(self, config: AgentConfig, client: OpenAICompatClient, fallback: StubWriter | None = None) -> None:
        self.config = config
        self.client = client
        self.fallback = fallback
        self.name = "openai_compat"
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
        docs = "\n\n".join(f'<doc id="{h["doc_id"]}" title="{h["title"]}">\n{h["snippet"]}\n</doc>' for h in hits)
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
        ]  # READ tools of the role only, never write tools

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
        msgs = self._messages(question, hits, user, task_hint, history)
        try:
            if self.tool_calling and gateway is not None:
                msgs = self._run_tools(msgs, user, gateway)
            yield from self.client.stream(msgs)
        except LLMError:
            if not self.fallback:
                raise
            if meta is not None:
                meta["llm_fallback"] = True
            yield "(Модель сейчас недоступна, ответ сформирован в упрощённом режиме.)\n\n"
            yield from self.fallback.answer(question, hits, user, task_hint)

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
                    result: Any = gateway.read(name, **args)  # gateway enforces role allowlist and read-only
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
